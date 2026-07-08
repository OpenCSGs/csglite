package embedding

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/imagegen"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/logutil"
)

//go:embed worker/embedding_worker.py
var embeddingWorkerScript []byte

type PythonEngine struct {
	modelName string
	modelDir  string
	runtime   *imagegen.RuntimeManager
	cmd       *exec.Cmd
	exitCh    chan error
	port      int
	client    *http.Client
	logBuf    *logutil.TailWriter
	logFile   *os.File
}

func NewPythonEngine(ctx context.Context, modelName, modelDir string, runtimeManager *imagegen.RuntimeManager) (*PythonEngine, error) {
	if runtimeManager == nil {
		var err error
		runtimeManager, err = imagegen.NewEmbeddingRuntimeManager()
		if err != nil {
			return nil, err
		}
	}
	if err := runtimeManager.EnsureEmbeddingReady(ctx); err != nil {
		return nil, err
	}
	if err := writeEmbeddingWorkerScript(runtimeManager.RootDir()); err != nil {
		return nil, err
	}
	port, err := findFreePort()
	if err != nil {
		return nil, err
	}
	tempDir, err := liteTempDir()
	if err != nil {
		return nil, err
	}
	workerPath := filepath.Join(runtimeManager.RootDir(), "embedding_worker.py")
	storageRoot := storageRootFromModelDir(modelDir)
	cmd := exec.Command(runtimeManager.PythonPath(), workerPath, "--model-dir", modelDir, "--model-name", modelName, "--port", strconv.Itoa(port), "--storage-root", storageRoot, "--temp-dir", tempDir)
	cmd.Env = withTempDir(os.Environ(), tempDir)
	logBuf := logutil.NewTailWriter(64 * 1024)
	stdout := io.Writer(logBuf)
	stderr := io.Writer(logBuf)
	var logFile *os.File
	if config.FileLoggingEnabled() {
		if path, err := config.EmbeddingWorkerLogPath(); err != nil {
			log.Printf("warning: could not resolve embedding worker log path: %v", err)
		} else if file, err := logutil.OpenAppendFile(path); err != nil {
			log.Printf("warning: could not open embedding worker log file %s: %v", path, err)
		} else {
			logFile = file
			stdout = io.MultiWriter(stdout, file)
			stderr = io.MultiWriter(stderr, file)
		}
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return nil, fmt.Errorf("starting embedding worker: %w", err)
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
		logBuf:    logBuf,
		logFile:   logFile,
	}
	if err := engine.waitReady(ctx); err != nil {
		_ = engine.Close()
		// The runtime passed its readiness check but the worker still died;
		// force the next attempt to re-run the real import verification.
		runtimeManager.InvalidateEmbeddingImportCheck()
		return nil, err
	}
	return engine, nil
}

func (e *PythonEngine) Embeddings(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url("/v1/embeddings"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding worker request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, inference.NewHTTPStatusError(resp.StatusCode, string(respBody))
	}
	return resp, nil
}

func (e *PythonEngine) Generate(context.Context, string, inference.Options, inference.TokenCallback) (string, error) {
	return "", fmt.Errorf("embedding worker does not support text generation")
}

func (e *PythonEngine) Chat(context.Context, []inference.Message, inference.Options, inference.TokenCallback) (string, error) {
	return "", fmt.Errorf("embedding worker does not support chat")
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
	if e.logFile != nil {
		_ = e.logFile.Close()
		e.logFile = nil
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
				return fmt.Errorf("%s", e.workerStartError("embedding worker exited before becoming ready: "+err.Error()))
			}
			return fmt.Errorf("%s", e.workerStartError("embedding worker exited before becoming ready"))
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
	return fmt.Errorf("%s", e.workerStartError("timeout waiting for embedding worker"))
}

func (e *PythonEngine) url(path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", e.port, path)
}

func (e *PythonEngine) workerStartError(msg string) string {
	if e.logBuf != nil {
		if tail := strings.TrimSpace(e.logBuf.String()); tail != "" {
			msg += "\n\nembedding worker output:\n" + tail
		}
	}
	return msg
}

func writeEmbeddingWorkerScript(runtimeDir string) error {
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runtimeDir, "embedding_worker.py"), embeddingWorkerScript, 0o644)
}

func liteTempDir() (string, error) {
	home, err := config.AppHome()
	if err != nil {
		return "", err
	}
	tempDir := config.TempDirForStorage(home)
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return "", err
	}
	return tempDir, nil
}

func withTempDir(env []string, tempDir string) []string {
	return append(env, "TMPDIR="+tempDir, "TMP="+tempDir, "TEMP="+tempDir)
}

func storageRootFromModelDir(modelDir string) string {
	dir := filepath.Clean(modelDir)
	// Expected layout is <storage>/models/<namespace>/<name>.
	for i := 0; i < 3; i++ {
		dir = filepath.Dir(dir)
	}
	return dir
}

func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
