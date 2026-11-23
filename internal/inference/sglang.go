package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/hardware"
	"github.com/opencsgs/csglite/internal/model"
)

// SGLangBackend is the backend name used by model settings, deployments and
// the runtime capability API. SGLang serves native Hugging Face Safetensors
// weights through its OpenAI-compatible HTTP API; it is never fed a GGUF.
const SGLangBackend = "sglang"

// LlamaBackend is the existing llama.cpp backend. GGUF models default to it.
const LlamaBackend = "llama"

const (
	sglangPythonEnv       = "CSGHUB_LITE_SGLANG_PYTHON"
	sglangDisabledEnv     = "CSGHUB_LITE_SGLANG_DISABLE"
	sglangROCmArchEnv     = "CSGHUB_LITE_SGLANG_ROCM_ARCH"
	sglangReadyTimeoutEnv = "CSGHUB_LITE_SGLANG_READY_TIMEOUT"
)

const (
	defaultSGLangReadyTimeout = 30 * time.Minute
	sglangProbeTimeout        = 20 * time.Second
)

// SGLangDevice is one accelerator PyTorch can actually see inside this
// process, which already honours CUDA_VISIBLE_DEVICES.
type SGLangDevice struct {
	Index       int
	Name        string
	MemoryTotal uint64
}

// SGLangRuntime describes the native SGLang runtime on this machine.
// Installed, Available and Running are separate states: the Python package can
// be installed on a CPU-only container, where it is installed but not usable.
// It deliberately has no external base-URL mode: an already running
// fixed-model server is a provider, not a per-model native runtime.
type SGLangRuntime struct {
	Known     bool
	Installed bool
	Available bool
	Python    string
	Version   string
	CUDA      bool
	ROCM      bool
	Devices   []SGLangDevice
	Reason    string
}

type sglangProbeState struct {
	mu         sync.Mutex
	started    bool
	generation uint64
	done       chan struct{}
	result     *SGLangRuntime
	root       string
}

var (
	sglangState = &sglangProbeState{}
	// sglangRuntimeProbe is replaceable in tests so detection does not need a
	// real SGLang installation. It receives the first server's storage root.
	sglangRuntimeProbe = probeSGLangRuntime
	// sglangProbeCommand and sglangExecCommand are replaceable in tests.
	sglangProbeCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, name, args...)
	}
	sglangExecCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command(name, args...)
	}
)

// StartSGLangRuntimeProbe triggers detection in the background. Callers that
// must not block (heartbeats, node status) use SGLangRuntimeInfoCached after
// this; callers that need a definitive answer (a model load or an explicit
// backend save) use SGLangRuntimeInfo, which waits for the bounded probe.
func StartSGLangRuntimeProbe() {
	StartSGLangRuntimeProbeWithRoot("")
}

// StartSGLangRuntimeProbeWithRoot starts the one process-wide probe using the
// first caller's storage root. Later servers do not overwrite it, so parallel
// server tests cannot cross-write caches. The probe only reads the runtime and
// keeps any bytecode/temp output under this root.
func StartSGLangRuntimeProbeWithRoot(root string) {
	sglangState.mu.Lock()
	defer sglangState.mu.Unlock()
	if sglangState.started {
		return
	}
	sglangState.started = true
	sglangState.root = strings.TrimSpace(root)
	sglangState.generation++
	generation := sglangState.generation
	done := make(chan struct{})
	sglangState.done = done
	probeRoot := sglangState.root
	go func() {
		probed := sglangRuntimeProbe(probeRoot)
		sglangState.mu.Lock()
		if sglangState.generation == generation {
			sglangState.result = &probed
		}
		// Always close this probe's own channel. A reset may have created a
		// newer generation while this probe was running; its waiters still
		// need to be released, and its stale result must not replace the new
		// one.
		close(done)
		sglangState.mu.Unlock()
	}()
}

// SGLangRuntimeInfoCached returns the last detection result without waiting.
// known is false while the first probe is still running.
func SGLangRuntimeInfoCached() (SGLangRuntime, bool) {
	sglangState.mu.Lock()
	defer sglangState.mu.Unlock()
	if sglangState.result != nil {
		return *sglangState.result, true
	}
	return SGLangRuntime{}, false
}

// SGLangRuntimeInfo returns the detection result, starting and waiting for a
// bounded probe when needed.
func SGLangRuntimeInfo() SGLangRuntime {
	for {
		StartSGLangRuntimeProbe()
		sglangState.mu.Lock()
		done := sglangState.done
		sglangState.mu.Unlock()
		if done == nil {
			// A reset raced with Start; start the new generation.
			continue
		}
		<-done
		sglangState.mu.Lock()
		result := sglangState.result
		currentDone := sglangState.done
		sglangState.mu.Unlock()
		if result != nil {
			return *result
		}
		if currentDone != done {
			// This generation was reset while waiting; wait for the new one.
			continue
		}
		return SGLangRuntime{Known: true, Reason: "SGLang runtime detection produced no result"}
	}
}

// ResetSGLangRuntimeCache drops the cached detection result. It is used by
// tests and by a runtime installation flow that just changed the environment.
func ResetSGLangRuntimeCache() {
	sglangState.mu.Lock()
	sglangState.started = false
	sglangState.generation++
	sglangState.done = nil
	sglangState.result = nil
	sglangState.mu.Unlock()
}

func probeSGLangRuntime(storageRoot string) SGLangRuntime {
	if value := strings.TrimSpace(os.Getenv(sglangDisabledEnv)); value != "" {
		if disabled, err := strconv.ParseBool(value); err == nil && disabled {
			return SGLangRuntime{Known: true, Reason: "SGLang was disabled with " + sglangDisabledEnv + "=1"}
		}
	}
	// Native managed SGLang needs a CUDA/ROCm Linux host. macOS and Windows
	// keep llama.cpp; the Docker image remains the SGLang option there.
	if runtime.GOOS != "linux" {
		return SGLangRuntime{Known: true, Reason: "native SGLang managed inference requires Linux with CUDA or ROCm; use llama.cpp on this platform or a remote Linux SGLang host (the SGLang Docker image is Linux-only)"}
	}

	var reasons []string
	for _, python := range sglangPythonCandidates() {
		probed := probeSGLangPython(python, storageRoot)
		if probed.Installed {
			return probed
		}
		if probed.Reason != "" {
			reasons = append(reasons, fmt.Sprintf("%s: %s", python, probed.Reason))
		}
	}
	reason := "SGLang is not installed for the discovered Python interpreter"
	if len(reasons) == 1 {
		reason += " (" + reasons[0] + ")"
	} else if len(reasons) > 1 {
		reason += " (" + strings.Join(reasons, "; ") + ")"
	} else if len(reasons) == 0 {
		reason = "no Python 3 interpreter was found; install SGLang or set " + sglangPythonEnv
	}
	return SGLangRuntime{Known: true, Reason: reason}
}

const sglangProbeScript = `import json, sys
info = {"installed": False, "version": "", "cuda": False, "rocm": False, "devices": [], "error": ""}
try:
    import sglang
    info["installed"] = True
    info["version"] = getattr(sglang, "__version__", "")
except Exception as exc:
    info["error"] = "%s: %s" % (type(exc).__name__, exc)
    print(json.dumps(info))
    sys.exit(1)
try:
    import torch
    info["rocm"] = bool(getattr(torch.version, "hip", None))
    info["cuda"] = bool(torch.cuda.is_available())
    if info["cuda"]:
        for index in range(torch.cuda.device_count()):
            props = torch.cuda.get_device_properties(index)
            info["devices"].append({"index": index, "name": props.name, "total": int(props.total_memory),
                                    "arch": getattr(props, "gcnArchName", "")})
except Exception as exc:
    info["error"] = "%s: %s" % (type(exc).__name__, exc)
print(json.dumps(info))`

func probeSGLangPython(python, storageRoot string) SGLangRuntime {
	ctx, cancel := context.WithTimeout(context.Background(), sglangProbeTimeout)
	defer cancel()
	cmd := sglangProbeCommand(ctx, python, "-c", sglangProbeScript)
	cmd.Env = sglangProcessEnv(storageRoot)
	output, runErr := cmd.CombinedOutput()

	var payload struct {
		Installed bool   `json:"installed"`
		Version   string `json:"version"`
		CUDA      bool   `json:"cuda"`
		ROCM      bool   `json:"rocm"`
		Error     string `json:"error"`
		Devices   []struct {
			Index int    `json:"index"`
			Name  string `json:"name"`
			Total uint64 `json:"total"`
			Arch  string `json:"arch"`
		} `json:"devices"`
	}
	text := strings.TrimSpace(string(output))
	jsonStart := strings.IndexByte(text, '{')
	if jsonStart >= 0 {
		_ = json.Unmarshal([]byte(text[jsonStart:]), &payload)
	}
	if !payload.Installed {
		reason := strings.TrimSpace(payload.Error)
		if reason == "" {
			reason = lastNonEmptyLine(text)
		}
		if reason == "" && runErr != nil {
			reason = runErr.Error()
		}
		if reason == "" {
			reason = "SGLang is not importable"
		}
		return SGLangRuntime{Known: true, Python: python, Reason: reason}
	}

	probed := SGLangRuntime{Known: true, Installed: true, Python: python, Version: payload.Version, CUDA: payload.CUDA, ROCM: payload.ROCM}
	if probed.Version == "" {
		probed.Version = "unknown"
	}
	for _, device := range payload.Devices {
		probed.Devices = append(probed.Devices, SGLangDevice{Index: device.Index, Name: device.Name, MemoryTotal: device.Total})
	}
	switch {
	case payload.Error != "":
		probed.Reason = "SGLang is installed but PyTorch could not be probed: " + payload.Error
	case !probed.CUDA:
		probed.Reason = "SGLang is installed but PyTorch reports no visible CUDA/ROCm GPU"
	case len(probed.Devices) == 0:
		probed.Reason = "SGLang is installed but PyTorch reports no usable GPU device"
	default:
		if target := strings.TrimSpace(os.Getenv(sglangROCmArchEnv)); target != "" {
			if !probed.ROCM {
				probed.Reason = fmt.Sprintf("SGLang kernels for %s require ROCm PyTorch", target)
				return probed
			}
			for _, device := range payload.Devices {
				// ROCm appends feature suffixes such as :sramecc+:xnack-.
				arch, _, _ := strings.Cut(strings.TrimSpace(device.Arch), ":")
				if arch == target {
					continue
				}
				if arch == "" {
					arch = "unknown architecture"
				}
				probed.Reason = fmt.Sprintf("SGLang ROCm kernels target %s, but GPU %d reports %s", target, device.Index, arch)
				return probed
			}
		}
		probed.Available = true
	}
	return probed
}

// sglangPythonCandidates returns platform-aware interpreters to probe. An
// explicit override wins; the Docker image's /opt/sglang interpreter is next so
// a minimal PATH still finds the pinned runtime; then the Lite-managed runtime
// and finally PATH and standard install locations.
func sglangPythonCandidates() []string {
	seen := map[string]bool{}
	var candidates []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return
		}
		seen[path] = true
		candidates = append(candidates, path)
	}

	add(os.Getenv(sglangPythonEnv))
	if runtime.GOOS == "linux" {
		add("/opt/sglang/bin/python")
		add("/opt/sglang/bin/python3")
	}
	if home, err := config.AppHome(); err == nil {
		add(filepath.Join(home, "runtime", "sglang", "bin", "python3"))
		add(filepath.Join(home, "runtime", "sglang", "bin", "python"))
	}
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			add(path)
		}
	}
	switch runtime.GOOS {
	case "linux":
		add("/usr/local/bin/python3")
		add("/usr/bin/python3")
	case "darwin":
		add("/opt/homebrew/bin/python3")
		add("/usr/local/bin/python3")
		add("/usr/bin/python3")
	case "windows":
		add(`C:\Python312\python.exe`)
		add(`C:\Python311\python.exe`)
	}
	return candidates
}

// SGLangConfigured reports whether this installation or its environment
// explicitly provides a native SGLang runtime. A configured runtime that fails
// to probe must be reported, never silently replaced by a GGUF conversion.
func SGLangConfigured() bool {
	if value := strings.TrimSpace(os.Getenv(sglangDisabledEnv)); value != "" {
		if disabled, err := strconv.ParseBool(value); err == nil && disabled {
			return false
		}
	}
	if strings.TrimSpace(os.Getenv(sglangPythonEnv)) != "" {
		return true
	}
	if value := strings.TrimSpace(os.Getenv("CSGHUB_LITE_SGLANG_REQUIRE")); value != "" {
		if required, err := strconv.ParseBool(value); err == nil && required {
			return true
		}
	}
	var candidates []string
	if runtime.GOOS == "linux" {
		candidates = append(candidates, "/opt/sglang/bin/python", "/opt/sglang/bin/python3")
	}
	if home, err := config.AppHome(); err == nil {
		candidates = append(candidates, filepath.Join(home, "runtime", "sglang", "bin", "python3"), filepath.Join(home, "runtime", "sglang", "bin", "python"))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func defaultSGLangStorageRoot() string {
	home, err := config.AppHome()
	if err != nil {
		return ""
	}
	return home
}

// sglangProcessEnv keeps every subprocess temporary file, cache and bytecode
// under the given Lite storage root. Inherited system /tmp or cache variables
// are replaced, not preserved: otherwise a host setting would silently move
// runtime state outside the storage root. An empty root uses the app home.
func sglangProcessEnv(root string) []string {
	if strings.TrimSpace(root) == "" {
		root = defaultSGLangStorageRoot()
	}
	env := os.Environ()
	if root == "" {
		return env
	}
	paths := map[string]string{
		"TMPDIR":                              filepath.Join(root, config.TmpDir),
		"TMP":                                 filepath.Join(root, config.TmpDir),
		"TEMP":                                filepath.Join(root, config.TmpDir),
		"HF_HOME":                             filepath.Join(root, "cache", "huggingface"),
		"XDG_CACHE_HOME":                      filepath.Join(root, "cache"),
		"SGLANG_CACHE_DIR":                    filepath.Join(root, "cache", "sglang"),
		"SGLANG_JIT_CACHE_DIR":                filepath.Join(root, "cache", "sglang", "jit"),
		"FLASHINFER_WORKSPACE_BASE":           filepath.Join(root, "cache", "sglang"),
		"TORCHINDUCTOR_CACHE_DIR":             filepath.Join(root, "cache", "inductor"),
		"TRITON_CACHE_DIR":                    filepath.Join(root, "cache", "triton"),
		"TORCH_EXTENSIONS_DIR":                filepath.Join(root, "cache", "torch-extensions"),
		"CUDA_CACHE_PATH":                     filepath.Join(root, "cache", "cuda"),
		"PYTHONPYCACHEPREFIX":                 filepath.Join(root, "cache", "pycache"),
		"SGLANG_WEIGHT_CACHE_SOCKET_TEMPLATE": filepath.Join(root, config.TmpDir, "sglang_weight_cache_{device_uuid}.sock"),
		"SGLANG_WEIGHT_CACHE_READY_TEMPLATE":  filepath.Join(root, config.TmpDir, "sglang_weight_cache_{device_uuid}.ready"),
	}
	for _, dir := range []string{
		paths["TMPDIR"],
		paths["HF_HOME"],
		paths["XDG_CACHE_HOME"],
		paths["SGLANG_CACHE_DIR"],
		paths["SGLANG_JIT_CACHE_DIR"],
		paths["FLASHINFER_WORKSPACE_BASE"],
		paths["TORCHINDUCTOR_CACHE_DIR"],
		paths["TRITON_CACHE_DIR"],
		paths["TORCH_EXTENSIONS_DIR"],
		paths["CUDA_CACHE_PATH"],
		paths["PYTHONPYCACHEPREFIX"],
	} {
		_ = os.MkdirAll(dir, 0o700)
	}
	for key, value := range paths {
		env = setEnv(env, key, value)
	}
	env = setEnv(env, "PYTHONUNBUFFERED", "1")
	// Compilation concurrency follows the container's CPU budget, not every
	// host core. Explicit user values always win.
	jobs := strconv.Itoa(hardware.RecommendedMaxJobs())
	env = appendEnvDefault(env, "MAX_JOBS", jobs)
	env = appendEnvDefault(env, "CMAKE_BUILD_PARALLEL_LEVEL", jobs)
	env = appendEnvDefault(env, "TORCHINDUCTOR_COMPILE_THREADS", jobs)
	return env
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// NormalizeBackend validates a requested inference backend. "" and "auto"
// mean the format default; legacy spellings are accepted for API
// compatibility.
func NormalizeBackend(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "", nil
	case "llama", "llama.cpp", "llama-cpp", "llamacpp":
		return LlamaBackend, nil
	case SGLangBackend, "sglang-native":
		return SGLangBackend, nil
	default:
		return "", fmt.Errorf("unsupported inference backend %q; use llama, sglang, or an empty value for automatic", value)
	}
}

// hasActualGGUF reports whether a GGUF is really present. A manifest can still
// say Safetensors after a conversion removed the source weights, so the
// directory is the authority.
func hasActualGGUF(modelDir string) bool {
	if _, format, err := model.FindModelFile(modelDir); err == nil && format == model.FormatGGUF {
		return true
	}
	matches, _ := filepath.Glob(filepath.Join(modelDir, "*.gguf"))
	return len(matches) > 0
}

// hasNativeHFWeights reports whether the directory still contains the native
// Hugging Face weights SGLang needs.
func hasNativeHFWeights(modelDir string) bool {
	if _, err := os.Stat(filepath.Join(modelDir, "config.json")); err != nil {
		return false
	}
	if convert.HasConvertibleHFWeights(modelDir) || convert.NeedsConversion(modelDir) {
		return true
	}
	matches, _ := filepath.Glob(filepath.Join(modelDir, "*.safetensors"))
	return len(matches) > 0
}

// modelFormatForBackend reports the weights layout a load would see. Actual
// weights win over a stale manifest: a converted model whose source weights
// were removed is GGUF even if its manifest still says Safetensors.
func modelFormatForBackend(modelDir string, lm *model.LocalModel) model.Format {
	if hasActualGGUF(modelDir) {
		return model.FormatGGUF
	}
	if hasNativeHFWeights(modelDir) {
		return model.FormatSafeTensors
	}
	if lm != nil {
		if lm.Format == model.FormatGGUF || lm.Format == model.FormatSafeTensors || lm.Format == model.FormatPyTorch {
			return lm.Format
		}
	}
	return ""
}

// isTextGenerationModel keeps the default SGLang path limited to text chat.
// Diffusion, speech recognition and speech synthesis weights are never routed
// to the text runtime just because they are Safetensors.
func isTextGenerationModel(modelDir string, lm *model.LocalModel) bool {
	if lm != nil {
		switch strings.ToLower(strings.TrimSpace(lm.PipelineTag)) {
		case "text-generation", "text2text-generation", "image-text-to-text":
			return true
		case "":
			// Fall through to config inspection.
		default:
			return false
		}
	}
	cfg := readSGLangModelConfig(modelDir)
	for _, arch := range cfg.Architectures {
		if model.IsASRArchitecture(arch) || model.IsTTSArchitecture(arch) || model.IsDiffusionGGUFArchitecture(arch) {
			return false
		}
	}
	return hasNativeHFWeights(modelDir)
}

// EffectiveBackend reports the backend a load would choose without validating
// that it can start. A persisted GGUF dtype/quantization keeps the model on
// llama.cpp: SGLang cannot serve that quantization, and silently ignoring it
// would be wrong. An explicit floating dtype does not force llama.cpp.
func EffectiveBackend(modelDir string, lm *model.LocalModel, requested, dtype string) string {
	normalized, err := NormalizeBackend(requested)
	if err != nil {
		return ""
	}
	if normalized != "" {
		return normalized
	}
	format := modelFormatForBackend(modelDir, lm)
	if format != model.FormatSafeTensors && format != model.FormatPyTorch {
		return LlamaBackend
	}
	if _, _, err := sglangDTypeFromSetting(dtype); err != nil {
		return LlamaBackend
	}
	if !isTextGenerationModel(modelDir, lm) {
		return LlamaBackend
	}
	if info := SGLangRuntimeInfo(); info.Available || info.Installed || SGLangConfigured() {
		return SGLangBackend
	}
	return LlamaBackend
}

// ResolveEngineBackend validates the backend a load asked for. An explicit
// SGLang request never falls back to llama.cpp: a missing runtime, missing
// native weights or a GGUF-only quantization produce an error the caller can
// show, rather than silently serving a converted model the user did not ask
// for.
func ResolveEngineBackend(modelDir string, lm *model.LocalModel, requested, dtype string) (string, error) {
	normalized, err := NormalizeBackend(requested)
	if err != nil {
		return "", err
	}
	format := modelFormatForBackend(modelDir, lm)
	switch normalized {
	case LlamaBackend:
		return LlamaBackend, nil
	case SGLangBackend:
		if _, _, err := sglangDTypeFromSetting(dtype); err != nil {
			return "", err
		}
		if format == model.FormatGGUF && !hasNativeHFWeights(modelDir) {
			return "", fmt.Errorf("%w: SGLang requires native Safetensors weights, but this model has only GGUF; choose the llama backend", ErrUnsupportedFormat)
		}
		if !hasNativeHFWeights(modelDir) {
			return "", fmt.Errorf("%w: SGLang requires native Safetensors weights, but they are missing (they may have been removed after GGUF conversion); re-download the model or choose the llama backend", ErrUnsupportedFormat)
		}
		if !isTextGenerationModel(modelDir, lm) {
			return "", fmt.Errorf("%w: SGLang backend serves text generation and chat only; this model's task is not text generation", ErrUnsupportedFormat)
		}
		if info := SGLangRuntimeInfo(); !info.Available {
			return "", fmt.Errorf("SGLang backend requested but unavailable: %s", info.Reason)
		}
		return SGLangBackend, nil
	}
	switch format {
	case model.FormatSafeTensors, model.FormatPyTorch:
		if _, _, err := sglangDTypeFromSetting(dtype); err == nil && isTextGenerationModel(modelDir, lm) {
			info := SGLangRuntimeInfo()
			if info.Available {
				return SGLangBackend, nil
			}
			if info.Installed || SGLangConfigured() {
				return "", fmt.Errorf("SGLang is installed or configured but unavailable: %s", info.Reason)
			}
		}
		return LlamaBackend, nil
	default:
		return LlamaBackend, nil
	}
}

// RuntimeCapability describes one inference backend's state on this machine.
// Installed, Available and Running are separate: a Python package can be
// installed on a CPU-only host and still be unusable.
type RuntimeCapability struct {
	Backend     string
	Known       bool
	Installed   bool
	Available   bool
	Running     int
	Version     string
	Reason      string
	CUDA        bool
	ROCM        bool
	Devices     []SGLangDevice
	MaxJobs     int
	Formats     []string
	Tasks       []string
	ToolCalling string
}

// RuntimeCapabilitiesCached reports every backend without waiting for a
// pending SGLang probe. Node heartbeats use it so a slow import cannot delay
// cluster status.
func RuntimeCapabilitiesCached() []RuntimeCapability {
	llama := llamaRuntimeCapability()
	sglang := SGLangRuntime{Reason: "SGLang runtime detection is in progress"}
	if cached, known := SGLangRuntimeInfoCached(); known {
		sglang = cached
	}
	return []RuntimeCapability{llama, sglangRuntimeCapability(sglang)}
}

// RuntimeCapabilities waits for SGLang detection. Model settings and backend
// validation use it when a definitive answer matters.
func RuntimeCapabilities() []RuntimeCapability {
	llama := llamaRuntimeCapability()
	return []RuntimeCapability{llama, sglangRuntimeCapability(SGLangRuntimeInfo())}
}

func llamaRuntimeCapability() RuntimeCapability {
	capability := RuntimeCapability{
		Backend:     LlamaBackend,
		Known:       true,
		Formats:     []string{"gguf", "safetensors"},
		Tasks:       []string{"chat", "embedding"},
		ToolCalling: "model-dependent",
		MaxJobs:     hardware.RecommendedMaxJobs(),
	}
	binary := findLlamaBinary()
	if binary == "" {
		capability.Reason = "llama-server is not installed; install it from Settings or set CSGHUB_LITE_LLAMA_SERVER"
		return capability
	}
	capability.Installed = true
	capability.Available = true
	capability.Version = LlamaServerVersion()
	return capability
}

func sglangRuntimeCapability(runtimeInfo SGLangRuntime) RuntimeCapability {
	capability := RuntimeCapability{
		Backend:     SGLangBackend,
		Known:       runtimeInfo.Known,
		Installed:   runtimeInfo.Installed,
		Available:   runtimeInfo.Available,
		Version:     runtimeInfo.Version,
		Reason:      runtimeInfo.Reason,
		CUDA:        runtimeInfo.CUDA,
		ROCM:        runtimeInfo.ROCM,
		Devices:     runtimeInfo.Devices,
		MaxJobs:     hardware.RecommendedMaxJobs(),
		Formats:     []string{"safetensors"},
		Tasks:       []string{"chat"},
		ToolCalling: "model-dependent",
	}
	if !capability.Known && capability.Reason == "" {
		capability.Reason = "SGLang runtime detection is in progress"
	}
	return capability
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}
