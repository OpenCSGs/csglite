package tts

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/imagegen"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

//go:embed worker/tts_worker.py
var ttsWorkerScript []byte

// WorkerError carries the status the worker replied with, so a caller's mistake
// -- an unknown voice, an unsupported format -- reaches the client as a 4xx
// instead of being flattened into a server error.
type WorkerError struct {
	StatusCode int
	Message    string
}

func (e *WorkerError) Error() string {
	return e.Message
}

// ClientFault reports whether the worker blamed the request rather than itself.
func (e *WorkerError) ClientFault() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500
}

// workerError unwraps the worker's JSON error body so the message is the reason
// rather than a nested blob.
func workerError(status int, body []byte) *WorkerError {
	message := strings.TrimSpace(string(body))
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && strings.TrimSpace(payload.Error) != "" {
		message = strings.TrimSpace(payload.Error)
	}
	return &WorkerError{StatusCode: status, Message: message}
}

// PythonEngine runs one text-to-speech model in a separate Python process that
// serves HTTP on a loopback port, the same arrangement asr.PythonEngine uses.
type PythonEngine struct {
	modelName string
	modelDir  string
	runtime   *imagegen.RuntimeManager
	cmd       *exec.Cmd
	exitCh    chan error
	port      int
	client    *http.Client
	stderr    *tailBuffer
}

// tailBuffer keeps the last bytes written to it while passing everything
// through, so a worker that dies during startup can report why. The worker
// explains conditions the caller needs to act on -- a model that emits audio
// codec tokens but ships no decoder, for instance -- and without this the API
// would only surface "exit status 1".
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit}
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
	}
	return len(p), nil
}

// lastError returns the most useful line of captured output: Python reports the
// cause on the final line of a traceback.
func (t *tailBuffer) lastError() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.buf)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "File \"") || strings.HasPrefix(line, "Traceback") {
			continue
		}
		if index := strings.Index(line, "Error: "); index >= 0 {
			return strings.TrimSpace(line[index+len("Error: "):])
		}
		return line
	}
	return ""
}

func NewPythonEngine(ctx context.Context, modelName, modelDir string, runtimeManager *imagegen.RuntimeManager) (*PythonEngine, error) {
	if runtimeManager == nil {
		var err error
		runtimeManager, err = imagegen.NewTTSRuntimeManager()
		if err != nil {
			return nil, err
		}
	}
	if err := runtimeManager.EnsureTTSReady(ctx); err != nil {
		return nil, err
	}
	if err := runtimeManager.EnsureModelTTSPackages(ctx, modelName, modelDir); err != nil {
		return nil, err
	}
	// A backend whose dependencies cannot coexist with the others gets a private
	// overlay holding only the conflicting packages; everything else -- torch,
	// numpy, transformers -- is still reused from the shared venv.
	overlayDir, err := runtimeManager.EnsureTTSOverlay(ctx, model.TTSBackendFor(modelDir, modelName))
	if err != nil {
		return nil, err
	}
	if err := writeTTSWorkerScript(runtimeManager.RootDir()); err != nil {
		return nil, err
	}
	port, err := findFreePort()
	if err != nil {
		return nil, err
	}
	workerPath := filepath.Join(runtimeManager.RootDir(), "tts_worker.py")
	hardware := string(imagegen.DetectHardware())
	cmd := exec.Command(runtimeManager.PythonPath(), workerPath,
		"--model-dir", modelDir, "--model-name", modelName,
		"--port", strconv.Itoa(port), "--hardware", hardware)
	tempDir, err := liteTempDir()
	if err != nil {
		return nil, err
	}
	cmd.Env = imagegen.WithPythonPath(withTempDir(os.Environ(), tempDir), overlayDir)
	cmd.Stdout = os.Stdout
	stderr := newTailBuffer(16 << 10)
	cmd.Stderr = io.MultiWriter(os.Stderr, stderr)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting TTS worker: %w", err)
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
		stderr:    stderr,
	}
	if err := engine.waitReady(ctx); err != nil {
		_ = engine.Close()
		return nil, err
	}
	return engine, nil
}

func (e *PythonEngine) Speak(ctx context.Context, req api.OpenAIAudioSpeechRequest) (*Audio, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url("/speak"), bytes.NewReader(body))
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
		return nil, workerError(resp.StatusCode, respBody)
	}
	var out struct {
		Audio      string `json:"audio"`
		Format     string `json:"format"`
		SampleRate int    `json:"sample_rate"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decoding TTS worker response: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(out.Audio)
	if err != nil {
		return nil, fmt.Errorf("decoding TTS worker audio: %w", err)
	}
	return &Audio{Data: data, Format: out.Format, SampleRate: out.SampleRate}, nil
}

func (e *PythonEngine) SpeakStream(ctx context.Context, req api.OpenAIAudioSpeechRequest, onChunk func(Chunk) error) error {
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url("/speak_stream"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return readErr
		}
		return workerError(resp.StatusCode, respBody)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var frame struct {
			Audio string `json:"audio"`
			Done  bool   `json:"done"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(line, &frame); err != nil {
			return fmt.Errorf("decoding TTS worker stream response: %w", err)
		}
		if frame.Error != "" {
			return fmt.Errorf("TTS worker stream error: %s", frame.Error)
		}
		chunk := Chunk{Done: frame.Done}
		if frame.Audio != "" {
			data, err := base64.StdEncoding.DecodeString(frame.Audio)
			if err != nil {
				return fmt.Errorf("decoding TTS worker stream audio: %w", err)
			}
			chunk.Data = data
		}
		if err := onChunk(chunk); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading TTS worker stream response: %w", err)
	}
	return nil
}

func (e *PythonEngine) Info(ctx context.Context) (*api.SpeechVoicesResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, e.url("/health"), nil)
	if err != nil {
		return nil, err
	}
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
		return nil, workerError(resp.StatusCode, respBody)
	}
	var out api.SpeechVoicesResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decoding TTS worker health response: %w", err)
	}
	out.Model = e.modelName
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
			if reason := e.stderr.lastError(); reason != "" {
				return fmt.Errorf("TTS worker failed to start: %s", reason)
			}
			if err != nil {
				return fmt.Errorf("TTS worker exited before becoming ready: %w", err)
			}
			return fmt.Errorf("TTS worker exited before becoming ready")
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
	return fmt.Errorf("timeout waiting for TTS worker")
}

func (e *PythonEngine) url(path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", e.port, path)
}

func writeTTSWorkerScript(runtimeDir string) error {
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runtimeDir, "tts_worker.py"), ttsWorkerScript, 0o644)
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

func findFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
