package asr

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

	"github.com/opencsgs/csghub-lite/internal/imagegen"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

//go:embed worker/asr_worker.py
var asrWorkerScript []byte

type PythonEngine struct {
	modelName string
	modelDir  string
	runtime   *imagegen.RuntimeManager
	cmd       *exec.Cmd
	exitCh    chan error
	port      int
	client    *http.Client
}

func NewPythonEngine(ctx context.Context, modelName, modelDir string, runtimeManager *imagegen.RuntimeManager) (*PythonEngine, error) {
	if runtimeManager == nil {
		var err error
		runtimeManager, err = imagegen.NewRuntimeManager()
		if err != nil {
			return nil, err
		}
	}
	if err := runtimeManager.EnsureASRReady(ctx); err != nil {
		return nil, err
	}
	if err := writeASRWorkerScript(runtimeManager.RootDir()); err != nil {
		return nil, err
	}
	port, err := findFreePort()
	if err != nil {
		return nil, err
	}
	workerPath := filepath.Join(runtimeManager.RootDir(), "asr_worker.py")
	hardware := string(imagegen.DetectHardware())
	cmd := exec.Command(runtimeManager.PythonPath(), workerPath, "--model-dir", modelDir, "--model-name", modelName, "--port", strconv.Itoa(port), "--hardware", hardware)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting ASR worker: %w", err)
	}
	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
		close(exitCh)
	}()
	engine := &PythonEngine{
		modelName: modelName,
		modelDir:  modelDir,
		runtime:   runtimeManager,
		cmd:       cmd,
		exitCh:    exitCh,
		port:      port,
		client:    &http.Client{Timeout: 30 * time.Minute},
	}
	if err := engine.waitReady(ctx); err != nil {
		_ = engine.Close()
		return nil, err
	}
	return engine, nil
}

func (e *PythonEngine) Transcribe(ctx context.Context, req api.OpenAIAudioTranscriptionRequest) (*api.OpenAIAudioTranscriptionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url("/transcribe"), bytes.NewReader(body))
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
		return nil, fmt.Errorf("ASR worker error %d: %s", resp.StatusCode, string(respBody))
	}
	var out api.OpenAIAudioTranscriptionResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decoding ASR worker response: %w", err)
	}
	return &out, nil
}

func (e *PythonEngine) Close() error {
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

func (e *PythonEngine) ModelName() string {
	return e.modelName
}

func (e *PythonEngine) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-e.exitCh:
			if err != nil {
				return fmt.Errorf("ASR worker exited before becoming ready: %w", err)
			}
			return fmt.Errorf("ASR worker exited before becoming ready")
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
	return fmt.Errorf("timeout waiting for ASR worker")
}

func (e *PythonEngine) url(path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", e.port, path)
}

func writeASRWorkerScript(runtimeDir string) error {
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runtimeDir, "asr_worker.py"), asrWorkerScript, 0o644)
}

func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
