package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/hardware"
	"github.com/opencsgs/csglite/internal/logutil"
)

// sglangEngine runs SGLang's OpenAI-compatible server and reuses the existing
// OpenAI request and streaming logic for chat. It adds process lifecycle,
// health and startup diagnostics on top. Exactly one goroutine owns Wait for
// the process lifetime; Close signals the whole process group and waits for
// that goroutine rather than calling Wait again.
type sglangEngine struct {
	*openAIEngine
	cmd       *exec.Cmd
	port      int
	modelName string
	plan      SGLangPlan
	logBuf    *cappedWriter
	logFile   *os.File

	waitDone chan struct{}
	exitMu   sync.Mutex
	exitErr  error
	closing  bool

	runtimeMu  sync.Mutex
	runtimeErr error

	closeOnce sync.Once
}

func newSGLangEngine(ctx context.Context, modelDir, modelName, modelIdentity, storageRoot string, numCtx, numParallel int, dtype string, verbose bool, progress ConvertProgressFunc, deploymentArgs string) (*sglangEngine, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info := SGLangRuntimeInfo()
	if !info.Available {
		return nil, fmt.Errorf("SGLang backend unavailable: %s", info.Reason)
	}

	port, err := findFreePort()
	if err != nil {
		return nil, fmt.Errorf("allocating SGLang port: %w", err)
	}
	gpus, _ := hardware.QueryNVIDIAGPUs()
	plan, err := PlanSGLangLaunch(SGLangLaunchOptions{
		DeploymentArgs: deploymentArgs,
		RuntimeVersion: info.Version,
		ModelDir:       modelDir,
		ModelName:      modelName,
		ModelIdentity:  modelIdentity,
		StorageRoot:    storageRoot,
		NumCtx:         numCtx,
		NumParallel:    numParallel,
		DType:          dtype,
		Port:           port,
		GPUs:           gpus,
		Devices:        info.Devices,
	})
	if err != nil {
		return nil, err
	}

	engine := &sglangEngine{modelName: modelName, port: port, plan: plan}
	cmd := sglangExecCommand(info.Python, plan.Args...)
	configureSGLangProcess(cmd)
	// Stdout/Stderr are Go pipes. If the parent exits but a worker keeps them
	// open, Wait would otherwise block forever; WaitDelay bounds that and lets
	// the single waiter report the exit so Close can reap the group.
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = plan.Env
	engine.cmd = cmd

	log.Printf("SGLANG: starting model=%q python=%s port=%d ctx=%d max_running=%d max_total_tokens=%d tp=%d mem_fraction=%.3f dtype=%q tool_parser=%q",
		modelName, info.Python, port, plan.ContextLength, plan.MaxRunningRequests, plan.MaxTotalTokens,
		plan.TensorParallel, plan.MemFractionStatic, plan.DType, plan.ToolCallParser)

	if config.FileLoggingEnabled() {
		if path, err := sglangLogPath(storageRoot); err != nil {
			log.Printf("warning: could not resolve SGLang log path: %v", err)
		} else if file, err := logutil.OpenAppendFile(path); err != nil {
			log.Printf("warning: could not open SGLang log file %s: %v", path, err)
		} else {
			engine.logFile = file
		}
	}
	engine.logBuf = newCappedWriter(64 * 1024)
	engine.logBuf.observe = engine.observeRuntimeLog
	outputs := []io.Writer{engine.logBuf}
	if verbose {
		outputs = append(outputs, os.Stderr)
	}
	if engine.logFile != nil {
		outputs = append(outputs, engine.logFile)
	}
	cmd.Stdout = io.MultiWriter(outputs...)
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		engine.closeLog()
		return nil, fmt.Errorf("starting SGLang: %w", err)
	}

	// One waiter for the whole lifetime. Nothing else may call Wait.
	engine.waitDone = make(chan struct{})
	go func() {
		err := cmd.Wait()
		engine.exitMu.Lock()
		engine.exitErr = err
		unexpected := !engine.closing
		engine.exitMu.Unlock()
		if unexpected {
			engine.failRuntime(fmt.Errorf("SGLang process exited; retry deployment to restart"))
		}
		close(engine.waitDone)
	}()

	engine.openAIEngine = newSGLangOpenAIEngine(fmt.Sprintf("http://127.0.0.1:%d", port), modelName)

	if progress != nil {
		progress("Starting SGLang", 0, 0)
	}
	if err := engine.waitForReady(ctx, sglangReadyTimeout(), progress); err != nil {
		log.Printf("SGLANG: failed model=%q port=%d: %v", modelName, port, err)
		_ = engine.Close()
		return nil, fmt.Errorf("SGLang failed to start: %w", err)
	}
	if err := engine.RuntimeError(); err != nil {
		_ = engine.Close()
		return nil, err
	}
	// Read actual capacity from the worker, including when SGLang caps an
	// explicit pool size to the GPU budget.
	if engine.plan.MaxTotalTokens == 0 || readSGLangModelConfig(modelDir).hasHybridAttention() {
		if capacity := sglangTokenCapacity(ctx, port); capacity > 0 {
			engine.plan.MaxTotalTokens = capacity
			engine.plan.Sources["max_total_tokens"] = "reported by SGLang worker"
		}
	}
	log.Printf("SGLANG: ready model=%q url=http://127.0.0.1:%d", modelName, port)
	return engine, nil
}

func sglangTokenCapacity(ctx context.Context, port int) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/get_server_info", port), nil)
	if err != nil {
		return 0
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var info struct {
		InternalStates []struct {
			MemoryUsage struct {
				TokenCapacity int `json:"token_capacity"`
			} `json:"memory_usage"`
		} `json:"internal_states"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info) != nil {
		return 0
	}
	capacity := 0
	for _, state := range info.InternalStates {
		n := state.MemoryUsage.TokenCapacity
		if n > 0 && (capacity == 0 || n < capacity) {
			capacity = n
		}
	}
	return capacity
}

func newSGLangOpenAIEngine(baseURL, modelName string) *openAIEngine {
	baseURL = strings.TrimRight(baseURL, "/")
	return &openAIEngine{
		baseURL:                baseURL,
		chatCompletionsURL:     baseURL + "/v1/chat/completions",
		embeddingsURL:          baseURL + "/v1/embeddings",
		modelName:              modelName,
		disableThinking:        false,
		extendedSamplingParams: true,
		client:                 &http.Client{Timeout: 0},
	}
}

func sglangReadyTimeout() time.Duration {
	if value := strings.TrimSpace(os.Getenv(sglangReadyTimeoutEnv)); value != "" {
		if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
			return duration
		}
		if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return defaultSGLangReadyTimeout
}

func (e *sglangEngine) waitForReady(ctx context.Context, timeout time.Duration, progress ConvertProgressFunc) error {
	deadline := time.Now().Add(timeout)
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", e.port)
	client := &http.Client{Timeout: 3 * time.Second}

	start := time.Now()
	lastBeat := time.Time{}
	for time.Now().Before(deadline) {
		if progress != nil && time.Since(lastBeat) >= 2*time.Second {
			progress("Loading model with SGLang", int(time.Since(start).Seconds()), 0)
			lastBeat = time.Now()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.waitDone:
			return e.startupFailure(e.processExitMessage())
		default:
		}
		if err := e.RuntimeError(); err != nil {
			return err
		}
		// /v1/models can answer before the model is actually ready, so only
		// /health counts as readiness.
		if sglangHealthReady(client, healthURL) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return e.startupFailure(fmt.Sprintf("timeout waiting for SGLang /health to become ready (waited %v; large models need more time -- set %s=45m)", timeout, sglangReadyTimeoutEnv))
}

func sglangHealthReady(client *http.Client, url string) bool {
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (e *sglangEngine) processExitMessage() string {
	e.exitMu.Lock()
	err := e.exitErr
	e.exitMu.Unlock()
	message := "SGLang exited unexpectedly"
	if err != nil {
		message += ": " + err.Error()
	}
	return message
}

func (e *sglangEngine) startupFailure(message string) error {
	if e.logBuf != nil {
		if tail := strings.TrimSpace(e.logBuf.String()); tail != "" {
			message += "\n\nSGLang output:\n" + tail
		}
	}
	return errors.New(message)
}

func (e *sglangEngine) observeRuntimeLog(output string) {
	lower := strings.ToLower(output)
	for _, marker := range []string{
		"cuda out of memory",
		"outofmemoryerror",
		"segmentation fault",
		"aborted (core dumped)",
		"nccl error",
	} {
		if strings.Contains(lower, marker) {
			e.failRuntime(fmt.Errorf("SGLang reported a terminal runtime failure: %s", marker))
			return
		}
	}
}

func (e *sglangEngine) failRuntime(err error) {
	if err == nil {
		return
	}
	e.runtimeMu.Lock()
	if e.runtimeErr == nil {
		e.runtimeErr = err
	}
	e.runtimeMu.Unlock()
}

// RuntimeError reports a terminal backend failure, including an unexpected
// process exit, so the server's health checks can mark a deployment failed.
func (e *sglangEngine) RuntimeError() error {
	e.runtimeMu.Lock()
	defer e.runtimeMu.Unlock()
	return e.runtimeErr
}

func (e *sglangEngine) SupportsNativeToolStreaming() bool { return true }

// EnginePlan returns the exact process plan this engine started with.
func (e *sglangEngine) EnginePlan() SGLangPlan { return e.plan }

// ToolCallingSupported reports the model's SGLang parser capability. The proxy
// paths use it to refuse tool requests for an unverified model instead of
// silently returning prose that no agent can execute.
func (e *sglangEngine) ToolCallingSupported() (bool, string) {
	return e.plan.ToolCalling, e.plan.ToolCallingReason
}

func (e *sglangEngine) ModelName() string { return e.modelName }

// Close stops the whole owned process group: SIGTERM first, then SIGKILL after
// a bounded grace period. It never calls Wait, so the single waiter goroutine
// remains the only owner of the process exit status.
func (e *sglangEngine) Close() error {
	e.closeOnce.Do(func() {
		e.exitMu.Lock()
		e.closing = true
		e.exitMu.Unlock()

		if e.cmd != nil && e.cmd.Process != nil {
			_ = signalSGLangProcess(e.cmd, false)
			// The parent may exit promptly while a worker ignores TERM. Wait
			// for the parent, then make sure the whole owned group is gone and
			// force it down if not. This never targets another process group.
			e.waitForExit(15 * time.Second)
			if sglangGroupAlive(e.cmd) {
				e.waitForGroupExit(3 * time.Second)
			}
			if sglangGroupAlive(e.cmd) {
				_ = signalSGLangProcess(e.cmd, true)
			}
			e.waitForGroupExit(5 * time.Second)
		}
		e.closeLog()
	})
	return nil
}

func (e *sglangEngine) waitForGroupExit(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !sglangGroupAlive(e.cmd) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func sglangLogPath(storageRoot string) (string, error) {
	if strings.TrimSpace(storageRoot) == "" {
		storageRoot = defaultSGLangStorageRoot()
	}
	if storageRoot == "" {
		return "", fmt.Errorf("storage root is not configured")
	}
	dir := filepath.Join(storageRoot, config.LogsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, config.SGLangServerLogFileName), nil
}

func (e *sglangEngine) waitForExit(timeout time.Duration) bool {
	if e.waitDone == nil {
		return true
	}
	select {
	case <-e.waitDone:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (e *sglangEngine) closeLog() {
	if e.logFile != nil {
		_ = e.logFile.Close()
		e.logFile = nil
	}
}
