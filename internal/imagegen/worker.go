package imagegen

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/opencsgs/csglite/pkg/api"
)

//go:embed worker/diffusers_worker.py
var diffusersWorkerScript []byte

type DiffusersEngine struct {
	modelName string
	modelDir  string
	runtime   *RuntimeManager
	cmd       *exec.Cmd
	exitCh    chan error
	port      int
	client    *http.Client
}

func NewDiffusersEngine(ctx context.Context, modelName, modelDir string, runtimeManager *RuntimeManager) (*DiffusersEngine, error) {
	if runtimeManager == nil {
		var err error
		runtimeManager, err = NewRuntimeManager()
		if err != nil {
			return nil, err
		}
	}
	if err := runtimeManager.EnsureReady(ctx); err != nil {
		return nil, err
	}
	if err := writeWorkerScript(runtimeManager.RootDir()); err != nil {
		return nil, err
	}
	port, err := findFreePort()
	if err != nil {
		return nil, err
	}
	workerPath := filepath.Join(runtimeManager.RootDir(), "diffusers_worker.py")
	cmd := exec.CommandContext(ctx, runtimeManager.PythonPath(), workerPath, "--model-dir", modelDir, "--model-name", modelName, "--port", strconv.Itoa(port))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting Diffusers worker: %w", err)
	}
	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
		close(exitCh)
	}()
	engine := &DiffusersEngine{
		modelName: modelName,
		modelDir:  modelDir,
		runtime:   runtimeManager,
		cmd:       cmd,
		exitCh:    exitCh,
		port:      port,
		client:    &http.Client{Timeout: 5 * time.Minute},
	}
	if err := engine.waitReady(ctx); err != nil {
		_ = engine.Close()
		return nil, err
	}
	return engine, nil
}

func (e *DiffusersEngine) Generate(ctx context.Context, req api.OpenAIImagesGenerationRequest) (*api.OpenAIImagesGenerationResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url("/generate"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Diffusers worker error %d: %s", resp.StatusCode, string(respBody))
	}
	var out api.OpenAIImagesGenerationResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decoding Diffusers worker response: %w", err)
	}
	return &out, nil
}

func (e *DiffusersEngine) Close() error {
	if e.cmd == nil || e.cmd.Process == nil {
		return nil
	}
	_ = e.cmd.Process.Kill()
	if e.exitCh != nil {
		select {
		case <-e.exitCh:
		case <-time.After(5 * time.Second):
		}
	}
	return nil
}

func (e *DiffusersEngine) ModelName() string {
	return e.modelName
}

func (e *DiffusersEngine) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-e.exitCh:
			if err != nil {
				return fmt.Errorf("Diffusers worker exited before becoming ready: %w", err)
			}
			return fmt.Errorf("Diffusers worker exited before becoming ready")
		default:
		}
		resp, err := e.client.Get(e.url("/health"))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for Diffusers worker")
}

func (e *DiffusersEngine) url(path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", e.port, path)
}

func writeWorkerScript(runtimeDir string) error {
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runtimeDir, "diffusers_worker.py"), diffusersWorkerScript, 0o644)
}

func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
