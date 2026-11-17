package inference

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/opencsgs/csglite/internal/hardware"
)

const (
	sglangTPEnv          = "CSGHUB_LITE_SGLANG_TP_SIZE"
	sglangCompatTPEnv    = "SGLANG_TP_SIZE"
	sglangBaseGPUEnv     = "CSGHUB_LITE_SGLANG_BASE_GPU_ID"
	sglangCompatBaseGPU  = "SGLANG_BASE_GPU_ID"
	sglangMemFractionEnv = "CSGHUB_LITE_SGLANG_MEM_FRACTION"
	sglangExtraArgsEnv   = "CSGHUB_LITE_SGLANG_EXTRA_ARGS"
	sglangCompatExtraEnv = "SGLANG_EXTRA_ARGS"
)

const (
	defaultSGLangContext      = 8192
	expandedSGLangContext     = 16384
	minSGLangContext          = 1024
	minSGLangMemFraction      = 0.05
	maxSGLangMemFraction      = 0.90
	sglangStaticOverheadBytes = 1536 << 20
)

// SGLangLaunchOptions is the resolved input for one SGLang process. Model
// overrides arrive already resolved; GPU information is injected so the plan
// is testable without a GPU. StorageRoot keeps this engine's logs, temp files
// and caches independent of other servers in the same process.
type SGLangLaunchOptions struct {
	ModelDir      string
	ModelName     string
	ModelIdentity string
	StorageRoot   string
	NumCtx        int
	NumParallel   int
	DType         string
	Port          int
	GPUs          []hardware.GPU
	// Devices is the runtime's PyTorch-visible device list. It is preferred
	// over nvidia-smi because it already honours CUDA_VISIBLE_DEVICES order.
	Devices []SGLangDevice
}

// SGLangPlan is the exact process configuration. It is also what the settings
// API reports, so the UI shows the limits that are actually passed to SGLang.
// Sources records why each automatic value was chosen.
type SGLangPlan struct {
	Args               []string
	Env                []string
	ContextLength      int
	MaxRunningRequests int
	MaxTotalTokens     int
	MemFractionStatic  float64
	TensorParallel     int
	BaseGPU            int
	DType              string
	ToolCallParser     string
	ReasoningParser    string
	ToolCalling        bool
	ToolCallingReason  string
	MaxJobs            int
	Sources            map[string]string
}

type sglangModelConfig struct {
	Architectures         []string           `json:"architectures"`
	ModelType             string             `json:"model_type"`
	TorchDType            string             `json:"torch_dtype"`
	DType                 string             `json:"dtype"`
	NumHiddenLayers       int                `json:"num_hidden_layers"`
	NumAttentionHeads     int                `json:"num_attention_heads"`
	NumKeyValueHeads      int                `json:"num_key_value_heads"`
	HeadDim               int                `json:"head_dim"`
	HiddenSize            int                `json:"hidden_size"`
	MaxPositionEmbeddings int                `json:"max_position_embeddings"`
	NameOrPath            string             `json:"_name_or_path"`
	TextConfig            *sglangModelConfig `json:"text_config"`
}

func readSGLangModelConfig(modelDir string) sglangModelConfig {
	var cfg sglangModelConfig
	data, err := os.ReadFile(filepath.Join(modelDir, "config.json"))
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)
	// Multimodal wrappers such as Qwen3.5 keep the language model settings in
	// text_config; the outer object has no layer/head counts.
	if cfg.TextConfig != nil && cfg.NumHiddenLayers == 0 {
		nested := *cfg.TextConfig
		if nested.Architectures == nil {
			nested.Architectures = cfg.Architectures
		}
		if nested.ModelType == "" {
			nested.ModelType = cfg.ModelType
		}
		if nested.NameOrPath == "" {
			nested.NameOrPath = cfg.NameOrPath
		}
		if nested.MaxPositionEmbeddings == 0 {
			nested.MaxPositionEmbeddings = cfg.MaxPositionEmbeddings
		}
		return nested
	}
	return cfg
}

func readSGLangChatTemplate(modelDir string) string {
	data, err := os.ReadFile(filepath.Join(modelDir, "tokenizer_config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		ChatTemplate json.RawMessage `json:"chat_template"`
	}
	if json.Unmarshal(data, &cfg) != nil || len(cfg.ChatTemplate) == 0 {
		return ""
	}
	var template string
	if json.Unmarshal(cfg.ChatTemplate, &template) == nil {
		return template
	}
	var entries []struct {
		Template string `json:"template"`
	}
	if json.Unmarshal(cfg.ChatTemplate, &entries) == nil {
		parts := make([]string, 0, len(entries))
		for _, entry := range entries {
			parts = append(parts, entry.Template)
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// ResolveSGLangNumCtx picks the context length SGLang is started with. An
// explicit request or model setting wins but is capped to the model's declared
// maximum; otherwise the model maximum is used up to a conservative 16K so a
// long-context model does not reserve an unbounded KV pool.
func ResolveSGLangNumCtx(modelDir string, requested int) int {
	modelMax := readSGLangModelConfig(modelDir).MaxPositionEmbeddings
	if modelMax > 0 && modelMax < minSGLangContext {
		modelMax = 0
	}
	if requested >= minSGLangContext {
		if modelMax > 0 && requested > modelMax {
			return modelMax
		}
		return requested
	}
	switch {
	case modelMax >= expandedSGLangContext:
		return expandedSGLangContext
	case modelMax >= defaultSGLangContext:
		return defaultSGLangContext
	case modelMax > 0:
		return modelMax
	default:
		return defaultSGLangContext
	}
}

// ResolveSGLangNumParallel resolves the SGLang concurrency limit. It maps to
// --max-running-requests, not tensor parallelism, and follows the same
// precedence as llama.cpp: request > per-model setting > global setting > 1.
func ResolveSGLangNumParallel(requested, modelSetting, globalSetting int) int {
	switch {
	case requested >= 1:
		return requested
	case modelSetting >= 1:
		return modelSetting
	case globalSetting >= 1:
		return globalSetting
	default:
		return 1
	}
}

// sglangDTypeFromSetting maps a persisted llama.cpp dtype to SGLang's --dtype.
// Floating settings are accepted; a GGUF quantization is rejected clearly
// instead of being silently ignored.
func sglangDTypeFromSetting(setting string) (string, bool, error) {
	switch strings.ToLower(strings.TrimSpace(setting)) {
	case "":
		return "", false, nil
	case "auto":
		return "", false, nil
	case "f16", "fp16", "half", "float16":
		return "float16", true, nil
	case "bf16", "bfloat16":
		return "bfloat16", true, nil
	case "f32", "fp32", "float", "float32":
		return "float32", true, nil
	default:
		return "", false, fmt.Errorf("%w: dtype/quantization %q applies to GGUF and llama.cpp; SGLang accepts auto, float16, bfloat16 or float32", ErrUnsupportedFormat, strings.TrimSpace(setting))
	}
}

// sglangDTypeFromModel follows SGLang's documented auto behavior: BF16
// checkpoints keep BF16, FP16 checkpoints keep FP16, and FP32 checkpoints run
// as FP16 rather than being forced into FP32 kernels.
func sglangDTypeFromModel(cfg sglangModelConfig) string {
	value := strings.ToLower(strings.TrimSpace(cfg.TorchDType))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(cfg.DType))
	}
	switch value {
	case "bfloat16", "bf16":
		return "bfloat16"
	case "float16", "half", "fp16":
		return "float16"
	default:
		return ""
	}
}

// PlanSGLangLaunch builds stable, bounded process arguments from the model
// metadata, visible GPUs and explicit overrides. Identity flags (model, host,
// port, API key) are owned by Lite and cannot be overridden through
// SGLANG_EXTRA_ARGS; tuning flags can, and the reported plan reflects them.
func PlanSGLangLaunch(opts SGLangLaunchOptions) (SGLangPlan, error) {
	gpus := sglangPlanGPUs(opts.Devices, opts.GPUs)
	cfg := readSGLangModelConfig(opts.ModelDir)

	plan := SGLangPlan{
		ContextLength:      ResolveSGLangNumCtx(opts.ModelDir, opts.NumCtx),
		MaxRunningRequests: ResolveSGLangNumParallel(opts.NumParallel, 0, 0),
		TensorParallel:     1,
		BaseGPU:            0,
		MaxJobs:            sglangMaxJobs(),
		Sources:            map[string]string{},
	}
	if opts.NumCtx >= minSGLangContext {
		plan.Sources["context_length"] = "request or model setting (resolved)"
	} else {
		plan.Sources["context_length"] = "model maximum / default"
	}
	if opts.NumParallel >= 1 {
		plan.Sources["max_running_requests"] = "request, model or global setting (resolved)"
	} else {
		plan.Sources["max_running_requests"] = "default"
	}
	plan.Sources["tensor_parallel"] = "default single GPU"
	plan.Sources["dtype"] = "model config"

	extra, err := sglangExtraArgs()
	if err != nil {
		return SGLangPlan{}, err
	}
	if err := sglangRejectDuplicateManagedFlags(extra); err != nil {
		return SGLangPlan{}, err
	}
	extraFlags := sglangFlagSet(extra)

	if value, ok := sglangFlagValue(extra, "--context-length"); ok {
		parsed, err := parseSGLangTokenCount(value)
		if err != nil {
			return SGLangPlan{}, fmt.Errorf("--context-length: %w", err)
		}
		plan.ContextLength = parsed
		plan.Sources["context_length"] = "SGLANG_EXTRA_ARGS"
	}
	if value, ok := sglangFlagValue(extra, "--max-running-requests"); ok {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 1 {
			return SGLangPlan{}, fmt.Errorf("--max-running-requests must be a positive integer")
		}
		plan.MaxRunningRequests = parsed
		plan.Sources["max_running_requests"] = "SGLANG_EXTRA_ARGS"
	}
	if value, ok := sglangFlagValue(extra, "--max-total-tokens"); ok {
		parsed, err := parseSGLangTokenCount(value)
		if err != nil {
			return SGLangPlan{}, fmt.Errorf("--max-total-tokens: %w", err)
		}
		if parsed < plan.ContextLength {
			return SGLangPlan{}, fmt.Errorf("--max-total-tokens=%d is smaller than the context length %d; a single request could not fit", parsed, plan.ContextLength)
		}
		plan.MaxTotalTokens = parsed
		plan.Sources["max_total_tokens"] = "SGLANG_EXTRA_ARGS"
	} else {
		plan.MaxTotalTokens = plan.ContextLength * plan.MaxRunningRequests
		plan.Sources["max_total_tokens"] = "context x concurrency"
	}
	if plan.TensorParallel, err = sglangTensorParallel(extra, gpus); err != nil {
		return SGLangPlan{}, err
	}
	if extraFlags["--tp-size"] || extraFlags["--tensor-parallel-size"] {
		plan.Sources["tensor_parallel"] = "SGLANG_EXTRA_ARGS"
	} else if plan.TensorParallel > 1 {
		plan.Sources["tensor_parallel"] = "SGLANG_TP_SIZE"
	}
	if plan.BaseGPU, err = sglangBaseGPU(extra); err != nil {
		return SGLangPlan{}, err
	}
	if len(gpus) > 0 && plan.BaseGPU >= len(gpus) {
		return SGLangPlan{}, fmt.Errorf("--base-gpu-id=%d is outside the %d visible GPU(s)", plan.BaseGPU, len(gpus))
	}
	if extraFlags["--base-gpu-id"] {
		plan.Sources["base_gpu"] = "SGLANG_EXTRA_ARGS"
	}

	if value, ok := sglangFlagValue(extra, "--dtype"); ok {
		normalized, err := validateSGLangDType(value)
		if err != nil {
			return SGLangPlan{}, err
		}
		plan.DType = normalized
		plan.Sources["dtype"] = "SGLANG_EXTRA_ARGS"
	} else if settingDType, explicit, err := sglangDTypeFromSetting(opts.DType); err != nil {
		return SGLangPlan{}, err
	} else if explicit {
		plan.DType = settingDType
		plan.Sources["dtype"] = "model setting"
	} else {
		plan.DType = sglangDTypeFromModel(cfg)
	}

	identity := strings.TrimSpace(opts.ModelIdentity)
	if identity == "" {
		identity = opts.ModelName
	}
	if value, ok := sglangFlagValue(extra, "--tool-call-parser"); ok {
		parser, supported, reason := validateSGLangToolParser(value)
		if parser == "" {
			return SGLangPlan{}, fmt.Errorf("%w: %s", ErrUnsupportedFormat, reason)
		}
		plan.ToolCallParser = parser
		plan.ToolCalling = supported
		plan.ToolCallingReason = reason
		plan.Sources["tool_call_parser"] = "SGLANG_EXTRA_ARGS"
	} else {
		plan.ToolCallParser, plan.ToolCalling, plan.ToolCallingReason = SelectSGLangToolParser(opts.ModelDir, identity)
		if plan.ToolCallParser != "" {
			plan.Sources["tool_call_parser"] = "model family and chat template"
		}
	}
	if value, ok := sglangFlagValue(extra, "--reasoning-parser"); ok {
		parser, err := validateSGLangReasoningParser(value)
		if err != nil {
			return SGLangPlan{}, err
		}
		plan.ReasoningParser = parser
		plan.Sources["reasoning_parser"] = "SGLANG_EXTRA_ARGS"
	} else {
		plan.ReasoningParser = SelectSGLangReasoningParser(opts.ModelDir, identity)
		if plan.ReasoningParser != "" {
			plan.Sources["reasoning_parser"] = "model family and chat template"
		}
	}

	if extraFlags["--mem-fraction-static"] {
		value, _ := sglangFlagValue(extra, "--mem-fraction-static")
		fraction, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || fraction <= 0 || fraction > 1 {
			return SGLangPlan{}, fmt.Errorf("--mem-fraction-static must be a number between 0 and 1")
		}
		plan.MemFractionStatic = fraction
		plan.Sources["mem_fraction_static"] = "SGLANG_EXTRA_ARGS"
	} else {
		kvWidth := sglangKVElementBytes(plan.DType, extra)
		fraction, source, err := sglangMemFraction(opts.ModelDir, cfg, plan, gpus, kvWidth)
		if err != nil {
			return SGLangPlan{}, err
		}
		plan.MemFractionStatic = fraction
		if plan.MemFractionStatic > 0 {
			plan.Sources["mem_fraction_static"] = source
		}
	}

	args := []string{
		"-m", "sglang.launch_server",
		"--model-path", opts.ModelDir,
		"--served-model-name", opts.ModelName,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(opts.Port),
	}
	addFlag := func(flag, value string) {
		if extraFlags[flag] {
			return
		}
		args = append(args, flag, value)
	}
	addFlag("--context-length", strconv.Itoa(plan.ContextLength))
	addFlag("--max-total-tokens", strconv.Itoa(plan.MaxTotalTokens))
	addFlag("--max-running-requests", strconv.Itoa(plan.MaxRunningRequests))
	if !extraFlags["--tp-size"] && !extraFlags["--tensor-parallel-size"] {
		args = append(args, "--tp-size", strconv.Itoa(plan.TensorParallel))
	}
	if plan.BaseGPU > 0 {
		addFlag("--base-gpu-id", strconv.Itoa(plan.BaseGPU))
	}
	if plan.MemFractionStatic > 0 {
		addFlag("--mem-fraction-static", strconv.FormatFloat(plan.MemFractionStatic, 'f', 3, 64))
	}
	if plan.DType != "" {
		addFlag("--dtype", plan.DType)
	}
	if plan.ToolCallParser != "" {
		addFlag("--tool-call-parser", plan.ToolCallParser)
	}
	if plan.ReasoningParser != "" {
		addFlag("--reasoning-parser", plan.ReasoningParser)
	}
	args = append(args, extra...)
	plan.Args = args
	plan.Env = sglangProcessEnv(opts.StorageRoot)
	return plan, nil
}

func sglangMaxJobs() int {
	if value := strings.TrimSpace(os.Getenv("MAX_JOBS")); value != "" {
		if jobs, err := strconv.Atoi(value); err == nil && jobs > 0 {
			return jobs
		}
	}
	return hardware.RecommendedMaxJobs()
}

func validateSGLangDType(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "auto":
		return "", nil
	case "half", "float16":
		return "float16", nil
	case "bfloat16":
		return "bfloat16", nil
	case "float", "float32":
		return "float32", nil
	default:
		return "", fmt.Errorf("--dtype %q is not a floating dtype SGLang accepts", value)
	}
}

var sglangToolParserNames = map[string]bool{
	"auto": true, "apertus2509": true, "cohere_command4": true, "deepseekv3": true,
	"deepseekv31": true, "deepseekv32": true, "deepseekv4": true, "dots": true,
	"glm": true, "glm45": true, "glm47": true, "gpt-oss": true, "kimi_k2": true,
	"kimi_k3": true, "lfm2": true, "ling3": true, "llama3": true, "mimo": true,
	"minicpm5": true, "mistral": true, "muse": true, "poolside_v1": true,
	"pythonic": true, "qwen": true, "qwen25": true, "qwen3_coder": true,
	"spark25": true, "step3": true, "step3p5": true, "minimax-m2": true,
	"minimax-m3": true, "trinity": true, "interns1": true, "hermes": true,
	"hunyuan": true, "gigachat3": true, "gemma4": true, "inkling": true,
}

var sglangReasoningParserNames = map[string]bool{
	"auto": true, "apertus2509": true, "deepseek-r1": true, "deepseek-v3": true,
	"deepseek-v4": true, "dots": true, "glm45": true, "ling3": true,
	"hunyuan": true, "gpt-oss": true, "kimi": true, "kimi_k2": true,
	"kimi_k3": true, "mimo": true, "muse": true, "poolside_v1": true,
	"qwen3": true, "qwen3-thinking": true, "minimax": true,
	"minimax-append-think": true, "minimax-m3": true, "step3": true,
	"step3p5": true, "mistral": true, "nemotron_3": true, "interns1": true,
	"gemma4": true, "inkling": true, "cohere_command4": true,
}

func validateSGLangToolParser(value string) (string, bool, string) {
	parser := strings.ToLower(strings.TrimSpace(value))
	if !sglangToolParserNames[parser] {
		return "", false, fmt.Sprintf("unknown SGLang --tool-call-parser %q", value)
	}
	if parser == "auto" {
		return "auto", false, "SGLang auto parser selection is not verified for this model; function calling is not advertised as supported"
	}
	return parser, true, "explicit SGLang tool-call parser override"
}

func validateSGLangReasoningParser(value string) (string, error) {
	parser := strings.ToLower(strings.TrimSpace(value))
	if !sglangReasoningParserNames[parser] {
		return "", fmt.Errorf("unknown SGLang --reasoning-parser %q", value)
	}
	if parser == "auto" {
		return "", nil
	}
	return parser, nil
}

// sglangExtraArgs reads the advanced process arguments. Both flag/value pairs
// and --flag=value are supported; the Docker image and older deployments
// already use SGLANG_EXTRA_ARGS.
func sglangExtraArgs() ([]string, error) {
	raw := strings.TrimSpace(os.Getenv(sglangExtraArgsEnv))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv(sglangCompatExtraEnv))
	}
	if raw == "" {
		return nil, nil
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("SGLANG_EXTRA_ARGS must be a JSON array of strings: %w", err)
	}
	reserved := map[string]bool{
		"--model": true, "--model-path": true, "--host": true, "--port": true,
		"--served-model-name": true, "--api-key": true, "--admin-api-key": true,
		"--config": true,
	}
	expectValue := false
	for _, arg := range args {
		if arg == "" || arg == "--" {
			return nil, fmt.Errorf("SGLANG_EXTRA_ARGS contains an empty or positional argument %q", arg)
		}
		if strings.HasPrefix(arg, "-") {
			name := arg
			if index := strings.IndexByte(arg, '='); index >= 0 {
				name = arg[:index]
			}
			if reserved[name] {
				return nil, fmt.Errorf("SGLANG_EXTRA_ARGS must not override %s; model, host, port and API key are managed by CSGLite", name)
			}
			expectValue = !strings.Contains(arg, "=")
			continue
		}
		if !expectValue {
			return nil, fmt.Errorf("SGLANG_EXTRA_ARGS value %q has no preceding flag", arg)
		}
		expectValue = false
	}
	return args, nil
}

func sglangRejectDuplicateManagedFlags(args []string) error {
	managed := map[string]bool{
		"--context-length": true, "--max-total-tokens": true,
		"--max-running-requests": true, "--tp-size": true,
		"--tensor-parallel-size": true, "--base-gpu-id": true,
		"--dtype": true, "--tool-call-parser": true,
		"--reasoning-parser": true, "--mem-fraction-static": true,
		"--kv-cache-dtype": true,
	}
	counts := map[string]int{}
	for _, arg := range args {
		name := arg
		if index := strings.IndexByte(arg, '='); index >= 0 {
			name = arg[:index]
		}
		if managed[name] {
			counts[name]++
		}
	}
	for name, count := range counts {
		if count > 1 {
			return fmt.Errorf("SGLANG_EXTRA_ARGS sets %s more than once", name)
		}
	}
	if counts["--tp-size"]+counts["--tensor-parallel-size"] > 1 {
		return fmt.Errorf("SGLANG_EXTRA_ARGS sets both --tp-size and --tensor-parallel-size")
	}
	return nil
}

func sglangFlagValue(args []string, names ...string) (string, bool) {
	for i, arg := range args {
		for _, name := range names {
			if arg == name && i+1 < len(args) {
				return args[i+1], true
			}
			if strings.HasPrefix(arg, name+"=") {
				return strings.TrimPrefix(arg, name+"="), true
			}
		}
	}
	return "", false
}

func sglangFlagSet(args []string) map[string]bool {
	flags := make(map[string]bool, len(args))
	for _, arg := range args {
		name := arg
		if index := strings.IndexByte(arg, '='); index >= 0 {
			name = arg[:index]
		}
		flags[name] = true
	}
	return flags
}

// parseSGLangTokenCount parses the suffixes the pinned SGLang CLI accepts:
// standard SI (k, M, G, T) and IEC (Ki, Mi, Gi, Ti), case-sensitive.
func parseSGLangTokenCount(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty token count")
	}
	multipliers := []struct {
		suffix string
		factor float64
	}{
		{"Ti", 1 << 40}, {"Gi", 1 << 30}, {"Mi", 1 << 20}, {"Ki", 1 << 10},
		{"T", 1e12}, {"G", 1e9}, {"M", 1e6}, {"k", 1e3},
	}
	factor := float64(1)
	number := value
	for _, item := range multipliers {
		if strings.HasSuffix(value, item.suffix) {
			factor = item.factor
			number = strings.TrimSuffix(value, item.suffix)
			break
		}
	}
	parsed, err := strconv.ParseFloat(number, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%q is not a valid token count", value)
	}
	return int(parsed * factor), nil
}

// sglangPlanGPUs prefers the runtime's PyTorch-visible devices. That list is
// already in CUDA_VISIBLE_DEVICES order and includes ROCm devices, so a
// physical nvidia-smi index the container cannot see is never recommended.
func sglangPlanGPUs(devices []SGLangDevice, gpus []hardware.GPU) []hardware.GPU {
	if len(devices) > 0 {
		out := make([]hardware.GPU, 0, len(devices))
		for index, device := range devices {
			out = append(out, hardware.GPU{Index: index, Name: device.Name, MemoryTotal: device.MemoryTotal})
		}
		return out
	}
	return sglangVisibleGPUs(gpus)
}

func sglangTensorParallel(extra []string, gpus []hardware.GPU) (int, error) {
	// A single GPU is the default: tensor parallelism changes the model
	// partition and needs every selected GPU to hold a shard.
	if value, ok := sglangFlagValue(extra, "--tp-size", "--tensor-parallel-size"); ok {
		tp, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || tp < 1 || tp > 64 {
			return 0, fmt.Errorf("--tp-size must be an integer between 1 and 64")
		}
		if len(gpus) > 0 && tp > len(gpus) {
			return 0, fmt.Errorf("--tp-size=%d requires %d visible GPUs, but only %d are reported", tp, tp, len(gpus))
		}
		return tp, nil
	}
	raw := strings.TrimSpace(os.Getenv(sglangTPEnv))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv(sglangCompatTPEnv))
	}
	if raw == "" {
		return 1, nil
	}
	tp, err := strconv.Atoi(raw)
	if err != nil || tp < 1 || tp > 64 {
		return 0, fmt.Errorf("SGLANG_TP_SIZE must be an integer between 1 and 64")
	}
	if len(gpus) > 0 && tp > len(gpus) {
		return 0, fmt.Errorf("SGLANG_TP_SIZE=%d requires %d visible GPUs, but only %d are reported", tp, tp, len(gpus))
	}
	return tp, nil
}

func sglangBaseGPU(extra []string) (int, error) {
	if value, ok := sglangFlagValue(extra, "--base-gpu-id"); ok {
		index, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || index < 0 || index > 1024 {
			return 0, fmt.Errorf("--base-gpu-id must be a non-negative integer")
		}
		return index, nil
	}
	raw := strings.TrimSpace(os.Getenv(sglangBaseGPUEnv))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv(sglangCompatBaseGPU))
	}
	if raw == "" {
		return 0, nil
	}
	index, err := strconv.Atoi(raw)
	if err != nil || index < 0 || index > 1024 {
		return 0, fmt.Errorf("SGLANG_BASE_GPU_ID must be a non-negative integer")
	}
	return index, nil
}

// sglangMemFraction estimates the static memory this model needs and returns a
// fraction of the selected GPUs' VRAM. This keeps a small model from taking an
// entire 80GB card and still leaves room for CUDA graphs and activations.
func sglangMemFraction(modelDir string, cfg sglangModelConfig, plan SGLangPlan, gpus []hardware.GPU, kvElementBytes uint64) (float64, string, error) {
	if raw := strings.TrimSpace(os.Getenv(sglangMemFractionEnv)); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value < minSGLangMemFraction || value > maxSGLangMemFraction {
			return 0, "", fmt.Errorf("%s must be a number between %.2f and %.2f", sglangMemFractionEnv, minSGLangMemFraction, maxSGLangMemFraction)
		}
		return value, sglangMemFractionEnv, nil
	}
	selected := gpus
	start := plan.BaseGPU
	if start < 0 || start >= len(gpus) {
		start = 0
	}
	if plan.TensorParallel > 1 && start+plan.TensorParallel <= len(gpus) {
		selected = gpus[start : start+plan.TensorParallel]
	} else if len(gpus) > start {
		selected = gpus[start : start+1]
	}
	var totalVRAM uint64
	for _, gpu := range selected {
		totalVRAM += gpu.MemoryTotal
	}
	if totalVRAM == 0 {
		return 0, "", nil
	}
	weights := sglangWeightsBytes(modelDir)
	kvBytes := uint64(plan.MaxTotalTokens) * sglangKVBytesPerToken(cfg, kvElementBytes)
	// CUDA context, CUDA graphs and activation buffers grow with the card, so
	// a flat 512MiB is not credible on an 80GB GPU. Reserve at least 1.5GiB and
	// 3% of the selected VRAM. This remains a planning estimate; it has not
	// been hardware-validated on the QA A100.
	overhead := uint64(totalVRAM / 32)
	if overhead < sglangStaticOverheadBytes {
		overhead = sglangStaticOverheadBytes
	}
	required := float64(weights+kvBytes+overhead) * 1.15
	fraction := required / float64(totalVRAM)
	if fraction < minSGLangMemFraction {
		fraction = minSGLangMemFraction
	}
	if fraction > maxSGLangMemFraction {
		fraction = maxSGLangMemFraction
	}
	return float64(int(fraction*1000)) / 1000, "estimated from weights, KV pool and visible VRAM", nil
}

func sglangWeightsBytes(modelDir string) uint64 {
	var total uint64
	entries, err := os.ReadDir(modelDir)
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".safetensors") {
			continue
		}
		// Follow symlinks so a cache that links shards into the model
		// directory is sized by the real weights, not the link length.
		info, err := os.Stat(filepath.Join(modelDir, entry.Name()))
		if err == nil {
			total += uint64(info.Size())
		}
	}
	return total
}

// sglangKVBytesPerToken estimates the KV cache cost for one token in bytes.
// Unknown metadata returns 0 so no memory fraction is guessed.
func sglangKVElementBytes(dtype string, extra []string) uint64 {
	if value, ok := sglangFlagValue(extra, "--kv-cache-dtype"); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "fp8_e5m2", "fp8_e4m3", "mxfp8", "nvfp4", "fp4_mx_block16", "fp4_e2m1":
			return 1
		case "bf16", "bfloat16":
			return 2
		case "auto":
			// Fall through to the model dtype.
		default:
			return 2
		}
	}
	if strings.EqualFold(strings.TrimSpace(dtype), "float32") {
		return 4
	}
	return 2
}

func sglangKVBytesPerToken(cfg sglangModelConfig, elementBytes uint64) uint64 {
	if cfg.NumHiddenLayers <= 0 || cfg.NumAttentionHeads <= 0 || cfg.HiddenSize <= 0 {
		return 0
	}
	kvHeads := cfg.NumKeyValueHeads
	if kvHeads <= 0 {
		kvHeads = cfg.NumAttentionHeads
	}
	headDim := cfg.HeadDim
	if headDim <= 0 && cfg.NumAttentionHeads > 0 {
		headDim = cfg.HiddenSize / cfg.NumAttentionHeads
	}
	if headDim <= 0 {
		return 0
	}
	// K and V, elementBytes per element (4 for an FP32 cache, 2 for bf16/fp16,
	// 1 for FP8/NVFP4).
	return uint64(2*cfg.NumHiddenLayers*kvHeads*headDim) * elementBytes
}

// sglangVisibleGPUs honours CUDA_VISIBLE_DEVICES so memory sizing and the
// tensor-parallel check use the devices SGLang will actually see. UUID/MIG
// entries cannot be mapped back to nvidia-smi indices and are left unfiltered.
func sglangVisibleGPUs(gpus []hardware.GPU) []hardware.GPU {
	raw := strings.TrimSpace(os.Getenv("CUDA_VISIBLE_DEVICES"))
	if raw == "" || len(gpus) == 0 {
		return gpus
	}
	allowed := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		if index, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			allowed[index] = true
		}
	}
	if len(allowed) == 0 {
		return gpus
	}
	var visible []hardware.GPU
	for _, gpu := range gpus {
		if allowed[gpu.Index] {
			visible = append(visible, gpu)
		}
	}
	if len(visible) == 0 {
		return gpus
	}
	return visible
}

var (
	sglangBaseModelPattern   = regexp.MustCompile(`(^|[-_/. ])(base|pretrained|completion|pt)([-_/. ]|$)`)
	sglangChatVariantPattern = regexp.MustCompile(`(^|[-_/. ])(instruct|chat|coder|it)([-_/. ]|$)`)
)

func sglangLooksBase(name string) bool {
	return sglangBaseModelPattern.MatchString(strings.ToLower(name))
}

// sglangLooksChatVariant requires a whole token so a namespace such as
// "unit" or "kite" cannot be mistaken for an instruction-tuned alias.
func sglangLooksChatVariant(name string) bool {
	return sglangChatVariantPattern.MatchString(strings.ToLower(name))
}

func chatTemplateSupportsTools(template string) bool {
	lower := strings.ToLower(template)
	if lower == "" {
		return false
	}
	markers := []string{"tool_call", "tool_calls", "[available_tools]", "<function=", "function_call"}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func chatTemplateUsesQwenXML(template string) bool {
	lower := strings.ToLower(template)
	return strings.Contains(lower, "<function=") && strings.Contains(lower, "<parameter=")
}

func chatTemplateSuggestsReasoning(template string) bool {
	lower := strings.ToLower(template)
	return strings.Contains(lower, "<think") || strings.Contains(lower, "reasoning_content") || strings.Contains(lower, "thinking")
}

// SelectSGLangToolParser maps a model family to one of the parser names the
// pinned SGLang 0.5.19 --tool-call-parser flag accepts. It requires evidence
// beyond a family substring: a chat/instruct variant or a chat template with
// tool markers. Base/pretrained models and random-weight fixtures are reported
// unsupported with a reason.
func SelectSGLangToolParser(modelDir, modelName string) (string, bool, string) {
	cfg := readSGLangModelConfig(modelDir)
	template := readSGLangChatTemplate(modelDir)
	haystack := strings.ToLower(strings.Join([]string{
		modelName, cfg.ModelType, cfg.NameOrPath, strings.Join(cfg.Architectures, " "),
	}, " "))
	architecture := strings.Join(cfg.Architectures, ",")
	templateTools := chatTemplateSupportsTools(template)
	base := sglangLooksBase(haystack)
	chatVariant := sglangLooksChatVariant(haystack)
	// A real chat template is stronger evidence than a model-name alias. If
	// the template exists but renders no tools, a name containing "chat" must
	// not turn the model into a supported tool caller. With no template, the
	// chat/instruct token is the only available evidence.
	credible := templateTools
	if template == "" {
		credible = chatVariant
	}
	credible = credible && !base

	unsupported := func() (string, bool, string) {
		reason := fmt.Sprintf("SGLang tool-call parser is unknown or unverified for this model (architecture %q); function calling is not advertised as supported", architecture)
		if base {
			reason = fmt.Sprintf("SGLang function calling is not advertised for base/pretrained model %q (architecture %q)", modelName, architecture)
		} else if template != "" && !templateTools {
			reason = fmt.Sprintf("the model's chat template has no tool-call markup (architecture %q); function calling is not advertised as supported", architecture)
		}
		return "", false, reason
	}

	if strings.Contains(haystack, "deepseek") && (strings.Contains(haystack, "r1") || strings.Contains(haystack, "distill")) {
		return unsupported()
	}

	switch {
	case strings.Contains(haystack, "qwen3.5") || strings.Contains(haystack, "qwen3_5") ||
		strings.Contains(haystack, "qwen3-coder") || strings.Contains(haystack, "qwen3_coder"):
		if credible {
			return "qwen3_coder", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "qwen3") || strings.Contains(haystack, "qwen2.5") || strings.Contains(haystack, "qwen2_5"):
		if credible {
			return "qwen25", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "deepseek") && (strings.Contains(haystack, "r1") || strings.Contains(haystack, "distill")):
		// DeepSeek-R1 and its distills do not carry a tool-call template.
		return unsupported()
	case strings.Contains(haystack, "deepseek") && (strings.Contains(haystack, "v3.2") || strings.Contains(haystack, "v32")):
		if credible {
			return "deepseekv32", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "deepseek") && (strings.Contains(haystack, "v3.1") || strings.Contains(haystack, "v31")):
		if credible {
			return "deepseekv31", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "deepseek") && strings.Contains(haystack, "v4"):
		if credible {
			return "deepseekv4", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "deepseek"):
		if credible {
			return "deepseekv3", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "glm-4.7") || strings.Contains(haystack, "glm4.7") || strings.Contains(haystack, "glm47"):
		if credible {
			return "glm47", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "glm-4.5") || strings.Contains(haystack, "glm4.5") || strings.Contains(haystack, "glm45"):
		if credible {
			return "glm45", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "glm"):
		if credible {
			return "glm", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "kimi-k2") || strings.Contains(haystack, "kimi_k2"):
		if !base {
			return "kimi_k2", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "kimi-k3") || strings.Contains(haystack, "kimi_k3"):
		if !base {
			return "kimi_k3", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "gpt-oss") || strings.Contains(haystack, "gpt_oss"):
		if !base {
			return "gpt-oss", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "minimax-m2") || strings.Contains(haystack, "minimax_m2"):
		if !base {
			return "minimax-m2", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "minimax-m3") || strings.Contains(haystack, "minimax_m3"):
		if !base {
			return "minimax-m3", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "step-3.5") || strings.Contains(haystack, "step3p5") || strings.Contains(haystack, "step3.5"):
		if credible {
			return "step3p5", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "step-3") || strings.Contains(haystack, "step3"):
		if credible {
			return "step3", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "mistral") || strings.Contains(haystack, "mixtral"):
		// Old Mistral/Mixtral base models have no tool template. Only a
		// template that actually renders tools is credible.
		if credible {
			return "mistral", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "hermes"):
		if !base {
			return "hermes", true, ""
		}
		return unsupported()
	// Llama 3.x uses the llama3 parser; the architecture alone is not enough,
	// because Llama 2 and random-weight Llama fixtures share it.
	case strings.Contains(haystack, "llama-3") || strings.Contains(haystack, "llama3") || strings.Contains(haystack, "llama_3"):
		if credible {
			return "llama3", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "gemma-4") || strings.Contains(haystack, "gemma4"):
		if credible {
			return "gemma4", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "intern"):
		if credible {
			return "interns1", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "hunyuan"):
		if credible {
			return "hunyuan", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "cohere_command4") || strings.Contains(haystack, "command-r"):
		if credible {
			return "cohere_command4", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "minicpm5"):
		if credible {
			return "minicpm5", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "mimo"):
		if credible {
			return "mimo", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "dots"):
		if credible {
			return "dots", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "ling3"):
		if credible {
			return "ling3", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "lfm2"):
		if credible {
			return "lfm2", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "muse"):
		if credible {
			return "muse", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "poolside"):
		if credible {
			return "poolside_v1", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "spark25"):
		if credible {
			return "spark25", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "trinity"):
		if credible {
			return "trinity", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "gigachat3"):
		if credible {
			return "gigachat3", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "inkling"):
		if credible {
			return "inkling", true, ""
		}
		return unsupported()
	case strings.Contains(haystack, "apertus2509"):
		if credible {
			return "apertus2509", true, ""
		}
		return unsupported()
	default:
		return unsupported()
	}
}

// SelectSGLangReasoningParser picks --reasoning-parser only where the model
// family or chat template shows a reasoning format.
func SelectSGLangReasoningParser(modelDir, modelName string) string {
	cfg := readSGLangModelConfig(modelDir)
	template := readSGLangChatTemplate(modelDir)
	haystack := strings.ToLower(strings.Join([]string{
		modelName, cfg.ModelType, cfg.NameOrPath, strings.Join(cfg.Architectures, " "),
	}, " "))
	reasoning := chatTemplateSuggestsReasoning(template)
	switch {
	case strings.Contains(haystack, "deepseek") && strings.Contains(haystack, "r1"):
		return "deepseek-r1"
	case strings.Contains(haystack, "deepseek") && strings.Contains(haystack, "v4"):
		return "deepseek-v4"
	case strings.Contains(haystack, "deepseek") && (strings.Contains(haystack, "v3") || reasoning):
		return "deepseek-v3"
	case strings.Contains(haystack, "qwen3-thinking"):
		return "qwen3-thinking"
	case strings.Contains(haystack, "qwen3") && reasoning:
		return "qwen3"
	case strings.Contains(haystack, "glm-4.5") || strings.Contains(haystack, "glm45") || strings.Contains(haystack, "glm-4.7") || strings.Contains(haystack, "glm47"):
		return "glm45"
	case strings.Contains(haystack, "kimi_k2") || strings.Contains(haystack, "kimi-k2"):
		return "kimi_k2"
	case strings.Contains(haystack, "kimi_k3") || strings.Contains(haystack, "kimi-k3"):
		return "kimi_k3"
	case strings.Contains(haystack, "gpt-oss") || strings.Contains(haystack, "gpt_oss"):
		return "gpt-oss"
	case strings.Contains(haystack, "minimax-m3") || strings.Contains(haystack, "minimax_m3"):
		return "minimax-m3"
	case strings.Contains(haystack, "minimax"):
		return "minimax"
	case strings.Contains(haystack, "step3p5") || strings.Contains(haystack, "step-3.5"):
		return "step3p5"
	case strings.Contains(haystack, "step3") || strings.Contains(haystack, "step-3"):
		return "step3"
	case strings.Contains(haystack, "hunyuan") && reasoning:
		return "hunyuan"
	case strings.Contains(haystack, "intern") && reasoning:
		return "interns1"
	default:
		return ""
	}
}

// SGLangPlanForModel builds the reported plan for a model without starting a
// process. numParallel must already be resolved with the same global and
// per-model precedence the load uses.
func SGLangPlanForModel(modelDir, modelName, storageRoot string, numCtx, numParallel int) (SGLangPlan, error) {
	gpus, _ := hardware.QueryNVIDIAGPUs()
	var devices []SGLangDevice
	if runtimeInfo := SGLangRuntimeInfo(); runtimeInfo.Available {
		devices = runtimeInfo.Devices
	}
	return PlanSGLangLaunch(SGLangLaunchOptions{
		ModelDir:      modelDir,
		ModelName:     modelName,
		ModelIdentity: modelName,
		StorageRoot:   storageRoot,
		NumCtx:        numCtx,
		NumParallel:   numParallel,
		Port:          0,
		GPUs:          gpus,
		Devices:       devices,
	})
}

// modelIdentityForLoad is filled by the server from the original repository;
// deployments use an isolated served alias but keep their source identity for
// model-family defaults.
