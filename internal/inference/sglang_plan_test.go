package inference

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/hardware"
	"github.com/opencsgs/csglite/internal/model"
)

func writeSGLangTestModel(t *testing.T, config string, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	if config != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("weights"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const tinyLlamaConfig = `{
  "architectures": ["LlamaForCausalLM"],
  "model_type": "llama",
  "torch_dtype": "bfloat16",
  "num_hidden_layers": 2,
  "num_attention_heads": 4,
  "num_key_value_heads": 4,
  "hidden_size": 256,
  "max_position_embeddings": 8192
}`

func a100() []hardware.GPU {
	return []hardware.GPU{{Index: 0, Name: "NVIDIA A100-SXM4-80GB", MemoryTotal: 80 << 30}}
}

func TestPlanSGLangLaunchUsesBoundedDefaultsOnOneGPU(t *testing.T) {
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	plan, err := PlanSGLangLaunch(SGLangLaunchOptions{
		ModelDir: dir, ModelName: "tiny-llama", Port: 30000, GPUs: a100(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ContextLength != 8192 || plan.MaxRunningRequests != 1 || plan.MaxTotalTokens != 8192 {
		t.Fatalf("plan = %+v, want 8192 ctx, 1 running, 8192 total", plan)
	}
	if plan.TensorParallel != 1 {
		t.Fatalf("tensor parallel = %d, want 1", plan.TensorParallel)
	}
	if plan.MemFractionStatic < minSGLangMemFraction || plan.MemFractionStatic > 0.2 {
		t.Fatalf("mem fraction = %v, want a small bounded value", plan.MemFractionStatic)
	}
	if plan.ToolCalling || plan.ToolCallParser != "" {
		t.Fatalf("random-weight tiny llama advertised tool calling: %+v", plan)
	}
	joined := strings.Join(plan.Args, " ")
	for _, want := range []string{
		"-m sglang.launch_server", "--model-path " + dir, "--served-model-name tiny-llama",
		"--host 127.0.0.1", "--port 30000", "--context-length 8192",
		"--max-total-tokens 8192", "--max-running-requests 1", "--tp-size 1",
		"--dtype bfloat16", "--mem-fraction-static",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "--tool-call-parser") {
		t.Fatalf("unknown model must not receive a tool parser: %q", joined)
	}
}

func TestPlanSGLangLaunchHonoursModelMaxAndOverrides(t *testing.T) {
	dir := writeSGLangTestModel(t, `{"architectures":["Qwen2ForCausalLM"],"model_type":"qwen2","num_hidden_layers":4,"num_attention_heads":8,"num_key_value_heads":2,"hidden_size":512,"max_position_embeddings":4096}`, "model.safetensors")
	plan, err := PlanSGLangLaunch(SGLangLaunchOptions{
		ModelDir: dir, ModelName: "Qwen2.5-1B-Instruct", NumCtx: 32768, NumParallel: 4, Port: 1, GPUs: a100(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ContextLength != 4096 {
		t.Fatalf("context = %d, want model maximum 4096", plan.ContextLength)
	}
	if plan.MaxRunningRequests != 4 || plan.MaxTotalTokens != 4096*4 {
		t.Fatalf("parallel/total = %d/%d", plan.MaxRunningRequests, plan.MaxTotalTokens)
	}
	if plan.ToolCallParser != "qwen25" || !plan.ToolCalling {
		t.Fatalf("qwen2.5 parser = %q supported=%t", plan.ToolCallParser, plan.ToolCalling)
	}
}

func TestPlanSGLangLaunchRejectsGGUFQuantization(t *testing.T) {
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	_, err := PlanSGLangLaunch(SGLangLaunchOptions{ModelDir: dir, ModelName: "m", DType: "q4_k_m", Port: 1, GPUs: a100()})
	if err == nil || !strings.Contains(err.Error(), "applies to GGUF") {
		t.Fatalf("error = %v, want incompatible dtype rejection", err)
	}
}

func TestPlanSGLangLaunchTensorParallelNeedsVisibleGPUs(t *testing.T) {
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	t.Setenv(sglangTPEnv, "2")
	if _, err := PlanSGLangLaunch(SGLangLaunchOptions{ModelDir: dir, ModelName: "m", Port: 1, GPUs: a100()}); err == nil {
		t.Fatal("tensor parallel 2 accepted with one visible GPU")
	}
	two := append(a100(), hardware.GPU{Index: 1, Name: "NVIDIA A100-SXM4-80GB", MemoryTotal: 80 << 30})
	plan, err := PlanSGLangLaunch(SGLangLaunchOptions{ModelDir: dir, ModelName: "m", Port: 1, GPUs: two})
	if err != nil {
		t.Fatal(err)
	}
	if plan.TensorParallel != 2 {
		t.Fatalf("tensor parallel = %d, want 2", plan.TensorParallel)
	}
}

func TestPlanSGLangLaunchExtraArgsCannotOverrideIdentity(t *testing.T) {
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	t.Setenv(sglangExtraArgsEnv, `["--port","1"]`)
	if _, err := PlanSGLangLaunch(SGLangLaunchOptions{ModelDir: dir, ModelName: "m", Port: 2, GPUs: a100()}); err == nil {
		t.Fatal("extra args overrode the managed port")
	}
	t.Setenv(sglangExtraArgsEnv, `["--context-length","4096","--tool-call-parser","hermes"]`)
	plan, err := PlanSGLangLaunch(SGLangLaunchOptions{ModelDir: dir, ModelName: "m", Port: 2, GPUs: a100()})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Args, " ")
	if strings.Count(joined, "--context-length") != 1 || !strings.Contains(joined, "--context-length 4096") {
		t.Fatalf("explicit context override not honoured exactly once: %q", joined)
	}
	if !strings.Contains(joined, "--tool-call-parser hermes") {
		t.Fatalf("explicit parser override missing: %q", joined)
	}
}

func TestSelectSGLangToolParserFamilies(t *testing.T) {
	cases := []struct {
		name   string
		config string
		model  string
		want   string
	}{
		{"qwen3 coder", `{"architectures":["Qwen3MoeForCausalLM"],"model_type":"qwen3_moe"}`, "Qwen3-Coder-30B", "qwen3_coder"},
		{"qwen3", `{"architectures":["Qwen3ForCausalLM"],"model_type":"qwen3"}`, "Qwen3-8B-Instruct", "qwen25"},
		{"deepseek v31", `{"architectures":["DeepseekV3ForCausalLM"],"model_type":"deepseek_v3"}`, "DeepSeek-V3.1-Instruct", "deepseekv31"},
		{"llama3", `{"architectures":["LlamaForCausalLM"],"model_type":"llama"}`, "Meta-Llama-3.1-8B-Instruct", "llama3"},
		{"glm45", `{"architectures":["Glm4MoeForCausalLM"],"model_type":"glm4_moe"}`, "GLM-4.5-Chat", "glm45"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeSGLangTestModel(t, tc.config, "model.safetensors")
			parser, supported, reason := SelectSGLangToolParser(dir, tc.model)
			if !supported || parser != tc.want {
				t.Fatalf("parser = %q supported=%t reason=%q, want %q", parser, supported, reason, tc.want)
			}
		})
	}
}

func TestSelectSGLangToolParserRejectsBaseModels(t *testing.T) {
	dir := writeSGLangTestModel(t, `{"architectures":["Qwen2ForCausalLM"],"model_type":"qwen2"}`, "model.safetensors")
	parser, supported, reason := SelectSGLangToolParser(dir, "Qwen2.5-7B-Base")
	if supported || parser != "" || !strings.Contains(reason, "base") {
		t.Fatalf("base model advertised tools: parser=%q supported=%t reason=%q", parser, supported, reason)
	}
}

func TestPlanSGLangLaunchFiltersCUDAVisibleDevices(t *testing.T) {
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	t.Setenv("CUDA_VISIBLE_DEVICES", "1")
	gpus := append(a100(), hardware.GPU{Index: 1, Name: "NVIDIA A100-SXM4-80GB", MemoryTotal: 40 << 30})
	plan, err := PlanSGLangLaunch(SGLangLaunchOptions{ModelDir: dir, ModelName: "m", Port: 1, GPUs: gpus})
	if err != nil {
		t.Fatal(err)
	}
	// The visible GPU has 40GB; the fraction must be computed from it rather
	// than the first physical 80GB card.
	required := float64(sglangWeightsBytes(dir)+uint64(plan.MaxTotalTokens)*sglangKVBytesPerToken(readSGLangModelConfig(dir), 2)+sglangStaticOverheadBytes) * 1.2
	want := required / float64(40<<30)
	if want < minSGLangMemFraction {
		want = minSGLangMemFraction
	}
	if diff := plan.MemFractionStatic - float64(int(want*1000))/1000; diff > 0.002 || diff < -0.002 {
		t.Fatalf("mem fraction = %v, want about %v", plan.MemFractionStatic, want)
	}
}

func TestSGLangProcessEnvUsesConfiguredStorageRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", "/host/tmp")
	env := sglangProcessEnv(root)
	values := map[string]string{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	if values["TMPDIR"] != filepath.Join(root, "tmp") {
		t.Fatalf("TMPDIR = %q, want storage root temp", values["TMPDIR"])
	}
	for _, key := range []string{"HF_HOME", "SGLANG_CACHE_DIR", "TRITON_CACHE_DIR", "TORCH_EXTENSIONS_DIR", "CUDA_CACHE_PATH", "PYTHONPYCACHEPREFIX"} {
		if !strings.HasPrefix(values[key], root) {
			t.Fatalf("%s = %q, want under %s", key, values[key], root)
		}
	}
	if values["MAX_JOBS"] == "" {
		t.Fatal("MAX_JOBS was not set for the SGLang process")
	}
}

func TestResolveEngineBackendUsesActualWeights(t *testing.T) {
	// A stale Safetensors manifest after conversion removed the source weights
	// must route to llama.cpp, not SGLang.
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.gguf")
	lm := &model.LocalModel{Format: model.FormatSafeTensors, PipelineTag: "text-generation"}
	if got := EffectiveBackend(dir, lm, "", ""); got != LlamaBackend {
		t.Fatalf("effective backend = %q, want llama for an actual GGUF", got)
	}
	if _, err := ResolveEngineBackend(dir, lm, SGLangBackend, ""); err == nil {
		t.Fatal("explicit SGLang accepted a GGUF-only directory")
	}

	// Explicit SGLang with native weights but no runtime reports the reason.
	dir = writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	t.Setenv(sglangDisabledEnv, "1")
	ResetSGLangRuntimeCache()
	t.Cleanup(ResetSGLangRuntimeCache)
	if _, err := ResolveEngineBackend(dir, lm, SGLangBackend, ""); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("error = %v, want unavailable reason", err)
	}
	if got := EffectiveBackend(dir, lm, "", ""); got != LlamaBackend {
		t.Fatalf("auto effective backend = %q, want llama fallback", got)
	}
}

func TestResolveEngineBackendRejectsNonChatModality(t *testing.T) {
	dir := writeSGLangTestModel(t, `{"architectures":["WhisperForConditionalGeneration"],"model_type":"whisper"}`, "model.safetensors")
	lm := &model.LocalModel{Format: model.FormatSafeTensors, PipelineTag: "automatic-speech-recognition"}
	if got := EffectiveBackend(dir, lm, "", ""); got != LlamaBackend {
		t.Fatalf("effective backend = %q, want llama for non-chat modality", got)
	}
}

func TestConfiguredSGLangNeverFallsBackToGGUFConversion(t *testing.T) {
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	lm := &model.LocalModel{Format: model.FormatSafeTensors, PipelineTag: "text-generation"}
	t.Setenv("CSGHUB_LITE_SGLANG_PYTHON", "/nonexistent/sglang-python")
	ResetSGLangRuntimeCache()
	t.Cleanup(ResetSGLangRuntimeCache)
	if got := EffectiveBackend(dir, lm, "", ""); got != SGLangBackend {
		t.Fatalf("effective backend = %q, want sglang while configured", got)
	}
	if _, err := ResolveEngineBackend(dir, lm, "", ""); err == nil || !strings.Contains(err.Error(), "configured but unavailable") {
		t.Fatalf("error = %v, want configured-but-unavailable", err)
	}
	if got, err := ResolveEngineBackend(dir, lm, LlamaBackend, ""); err != nil || got != LlamaBackend {
		t.Fatalf("explicit llama = %q, %v; want llama", got, err)
	}
}

func TestSGLangProbeResetReleasesWaitersAndIgnoresStaleResult(t *testing.T) {
	original := sglangRuntimeProbe
	t.Cleanup(func() {
		sglangRuntimeProbe = original
		ResetSGLangRuntimeCache()
	})
	started := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	sglangRuntimeProbe = func(string) SGLangRuntime {
		calls++
		if calls == 1 {
			close(started)
			<-release
			return SGLangRuntime{Known: true, Installed: true, Available: true, Version: "old"}
		}
		return SGLangRuntime{Known: true, Installed: true, Available: false, Version: "new"}
	}
	ResetSGLangRuntimeCache()
	StartSGLangRuntimeProbe()
	<-started

	waiterDone := make(chan SGLangRuntime, 1)
	go func() { waiterDone <- SGLangRuntimeInfo() }()
	time.Sleep(50 * time.Millisecond)
	ResetSGLangRuntimeCache()
	close(release)

	select {
	case got := <-waiterDone:
		if got.Version == "old" {
			t.Fatalf("stale probe result reached a waiter: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter blocked after a probe cache reset")
	}
	final := SGLangRuntimeInfo()
	if final.Version != "new" || final.Available {
		t.Fatalf("final runtime = %+v, want the new generation result", final)
	}
}

func TestLegacyLoaderForcesLlamaWhenSGLangIsAvailable(t *testing.T) {
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.gguf")
	lm := &model.LocalModel{Format: model.FormatGGUF, PipelineTag: "text-generation"}
	original := sglangRuntimeProbe
	sglangRuntimeProbe = func(string) SGLangRuntime {
		return SGLangRuntime{Known: true, Installed: true, Available: true, Version: "test"}
	}
	ResetSGLangRuntimeCache()
	t.Cleanup(func() {
		sglangRuntimeProbe = original
		ResetSGLangRuntimeCache()
	})
	_, err := LoadEngineWithSpeculativeProgress(dir, lm, nil, false, 0, 1, -1, "", "", "", SpeculativeConfig{})
	if err == nil {
		t.Fatal("legacy llama load unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "llama-server") || strings.Contains(err.Error(), "SGLang failed to start") {
		t.Fatalf("legacy loader did not stay on llama.cpp: %v", err)
	}
}

func TestInstalledSGLangWithoutGPUDoesNotFallBackToConversion(t *testing.T) {
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	lm := &model.LocalModel{Format: model.FormatSafeTensors, PipelineTag: "text-generation"}
	original := sglangRuntimeProbe
	sglangRuntimeProbe = func(string) SGLangRuntime {
		return SGLangRuntime{Known: true, Installed: true, Available: false, Reason: "PyTorch reports no visible CUDA/ROCm GPU"}
	}
	ResetSGLangRuntimeCache()
	t.Cleanup(func() {
		sglangRuntimeProbe = original
		ResetSGLangRuntimeCache()
	})
	if got := EffectiveBackend(dir, lm, "", ""); got != SGLangBackend {
		t.Fatalf("effective backend = %q, want sglang for an installed runtime", got)
	}
	if _, err := ResolveEngineBackend(dir, lm, "", ""); err == nil || !strings.Contains(err.Error(), "installed or configured but unavailable") {
		t.Fatalf("error = %v, want installed-but-unavailable", err)
	}
}

func TestSGLangProbeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_SGLANG_PROBE_HELPER") != "1" {
		return
	}
	fmt.Print(os.Getenv("SGLANG_PROBE_JSON"))
	os.Exit(0)
}

func TestProbeSGLangPythonParsesRuntimeJSON(t *testing.T) {
	t.Setenv("GO_WANT_SGLANG_PROBE_HELPER", "1")
	original := sglangProbeCommand
	sglangProbeCommand = func(context.Context, string, ...string) *exec.Cmd {
		return exec.Command(os.Args[0], "-test.run=TestSGLangProbeHelperProcess")
	}
	t.Cleanup(func() { sglangProbeCommand = original })

	t.Setenv("SGLANG_PROBE_JSON", `{"installed":true,"version":"0.5.19","cuda":true,"rocm":false,"devices":[{"index":0,"name":"NVIDIA A100-SXM4-80GB","total":85899345920}]}`)
	probed := probeSGLangPython(os.Args[0], t.TempDir())
	if !probed.Known || !probed.Installed || !probed.Available || probed.Version != "0.5.19" || len(probed.Devices) != 1 {
		t.Fatalf("probed = %+v", probed)
	}
	if probed.Devices[0].MemoryTotal != 85899345920 {
		t.Fatalf("device memory = %d", probed.Devices[0].MemoryTotal)
	}

	t.Setenv("SGLANG_PROBE_JSON", `{"installed":true,"version":"0.5.19","cuda":false,"rocm":false,"devices":[],"error":""}`)
	probed = probeSGLangPython(os.Args[0], t.TempDir())
	if !probed.Installed || probed.Available || !strings.Contains(probed.Reason, "no visible CUDA/ROCm GPU") {
		t.Fatalf("CPU-only probe = %+v", probed)
	}
}

func TestDeploymentAliasKeepsOriginalModelIdentityForParser(t *testing.T) {
	dir := writeSGLangTestModel(t, `{"architectures":["Qwen2ForCausalLM"],"model_type":"qwen2","num_hidden_layers":4,"num_attention_heads":8,"num_key_value_heads":2,"hidden_size":512,"max_position_embeddings":32768}`, "model.safetensors")
	template := `{"chat_template":"{% if tools %}<tool_call>{\"name\":\"x\",\"arguments\":{}}</tool_call>{% endif %}"}`
	if err := os.WriteFile(filepath.Join(dir, "tokenizer_config.json"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanSGLangLaunch(SGLangLaunchOptions{
		ModelDir: dir, ModelName: "deployment/abc", ModelIdentity: "Qwen/Qwen2.5-7B-Instruct", Port: 1, GPUs: a100(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ToolCallParser != "qwen25" || !plan.ToolCalling {
		t.Fatalf("deployment alias lost model identity: parser=%q supported=%t reason=%q", plan.ToolCallParser, plan.ToolCalling, plan.ToolCallingReason)
	}
	withoutIdentity, err := PlanSGLangLaunch(SGLangLaunchOptions{
		ModelDir: dir, ModelName: "deployment/abc", Port: 1, GPUs: a100(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if withoutIdentity.ToolCalling {
		t.Fatalf("alias-only plan unexpectedly advertised tools: %+v", withoutIdentity)
	}
}

func TestPlanSGLangSourcesReflectActualOverrides(t *testing.T) {
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	plan, err := PlanSGLangLaunch(SGLangLaunchOptions{ModelDir: dir, ModelName: "tiny-llama", NumCtx: 4096, Port: 1, GPUs: a100()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Sources["context_length"], "model-max") {
		t.Fatalf("explicit request context mislabeled: %v", plan.Sources)
	}
	t.Setenv(sglangMemFractionEnv, "0.25")
	plan, err = PlanSGLangLaunch(SGLangLaunchOptions{ModelDir: dir, ModelName: "tiny-llama", Port: 1, GPUs: a100()})
	if err != nil {
		t.Fatal(err)
	}
	if plan.MemFractionStatic != 0.25 || plan.Sources["mem_fraction_static"] != sglangMemFractionEnv {
		t.Fatalf("env mem fraction not reflected: %+v %v", plan.MemFractionStatic, plan.Sources)
	}
	t.Setenv(sglangMemFractionEnv, "not-a-number")
	if _, err := PlanSGLangLaunch(SGLangLaunchOptions{ModelDir: dir, ModelName: "tiny-llama", Port: 1, GPUs: a100()}); err == nil {
		t.Fatal("invalid mem fraction env was silently ignored")
	}
}

func TestLegacyLoaderForcesLlamaForSafetensorsWithoutProbingSGLang(t *testing.T) {
	dir := writeSGLangTestModel(t, `{"architectures":["UnsupportedTestArchitecture"],"max_position_embeddings":8192}`, "model.safetensors")
	lm := &model.LocalModel{Format: model.FormatSafeTensors, PipelineTag: "text-generation"}
	original := sglangRuntimeProbe
	sglangRuntimeProbe = func(string) SGLangRuntime {
		t.Fatal("legacy llama load probed SGLang")
		return SGLangRuntime{}
	}
	ResetSGLangRuntimeCache()
	t.Cleanup(func() {
		sglangRuntimeProbe = original
		ResetSGLangRuntimeCache()
	})
	_, err := LoadEngineWithSpeculativeProgress(dir, lm, nil, false, 0, 1, -1, "", "", "", SpeculativeConfig{})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("error = %v, want a llama.cpp converter rejection without an SGLang probe", err)
	}
}

func TestStartSGLangRuntimeProbeUsesFirstStorageRoot(t *testing.T) {
	original := sglangRuntimeProbe
	var gotRoot string
	sglangRuntimeProbe = func(root string) SGLangRuntime {
		gotRoot = root
		return SGLangRuntime{Known: true, Installed: true, Available: false}
	}
	ResetSGLangRuntimeCache()
	t.Cleanup(func() {
		sglangRuntimeProbe = original
		ResetSGLangRuntimeCache()
	})
	root := t.TempDir()
	StartSGLangRuntimeProbeWithRoot(root)
	_ = SGLangRuntimeInfo()
	if gotRoot != root {
		t.Fatalf("probe root = %q, want %q", gotRoot, root)
	}
	// A second server must not replace the first probe's root.
	StartSGLangRuntimeProbeWithRoot(t.TempDir())
	_ = SGLangRuntimeInfo()
	if gotRoot != root {
		t.Fatalf("second server replaced probe root: %q", gotRoot)
	}
}

func TestQwenHybridPlanUsesStandaloneTemplateAndWorkerCapacity(t *testing.T) {
	dir := writeSGLangTestModel(t, `{"architectures":["Qwen3_5ForConditionalGeneration"],"text_config":{"model_type":"qwen3_5_text","num_hidden_layers":4,"num_attention_heads":24,"num_key_value_heads":4,"head_dim":256,"hidden_size":5120,"max_position_embeddings":262144,"layer_types":["linear_attention","linear_attention","linear_attention","full_attention"]}}`, "model.safetensors")
	if err := os.WriteFile(filepath.Join(dir, "chat_template.jinja"), []byte("{% if tools %}<tool_call><function=example><parameter=name>{% endif %}<think>"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(sglangExtraArgsEnv, "")
	t.Setenv(sglangCompatExtraEnv, "")
	plan, err := PlanSGLangLaunch(SGLangLaunchOptions{
		ModelDir: dir, ModelName: "deployment/test", ModelIdentity: "Qwen/Qwen3.8-27B",
		NumCtx: 160000, NumParallel: 10, GPUs: a100(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ToolCallParser != "qwen3_coder" || plan.ReasoningParser != "qwen3" {
		t.Fatalf("parsers tool=%q reasoning=%q", plan.ToolCallParser, plan.ReasoningParser)
	}
	if plan.MaxTotalTokens != 0 || sglangFlagSet(plan.Args)["--max-total-tokens"] {
		t.Fatalf("hybrid capacity should be profiled by SGLang: %v", plan.Args)
	}
	if got := sglangKVBytesPerToken(readSGLangModelConfig(dir), 2); got != 4096 {
		t.Fatalf("full-attention KV bytes/token = %d, want 4096", got)
	}
}

func TestSGLangDeploymentTuningOverridesNodeArguments(t *testing.T) {
	dir := writeSGLangTestModel(t, `{"model_type":"qwen2"}`, "model.safetensors")
	t.Setenv(sglangExtraArgsEnv, `["--chunked-prefill-size=8192","--mem-fraction-static","0.80","--speculative-algorithm","NEXTN"]`)
	plan, err := PlanSGLangLaunch(SGLangLaunchOptions{
		ModelDir: dir, ModelName: "qwen", NumCtx: 4096,
		DeploymentArgs: "--chunked-prefill-size 4096\n--mem-fraction-static=0.85 --speculative-algorithm none",
	})
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := sglangFlagValue(plan.Args, "--chunked-prefill-size"); value != "4096" || plan.MemFractionStatic != .85 || sglangFlagSet(plan.Args)["--speculative-algorithm"] {
		t.Fatalf("unexpected tuning flags: %v", plan.Args)
	}
	for _, raw := range []string{"--model-path /root/model", "--port 40000", "--trust-remote-code true", "--chunked-prefill-size", "--max-total-tokens 1024 --max-total-tokens=2048", "--mem-fraction-static NaN"} {
		if _, err := ParseSGLangDeploymentArgs(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestQwen38AutomaticMTPRespectsTestedLimits(t *testing.T) {
	dir := writeSGLangTestModel(t, `{"architectures":["Qwen3_5ForConditionalGeneration"],"text_config":{"dtype":"bfloat16","mtp_num_hidden_layers":1,"num_hidden_layers":4,"num_attention_heads":24,"num_key_value_heads":4,"head_dim":256,"hidden_size":5120,"max_position_embeddings":262144,"layer_types":["linear_attention","linear_attention","linear_attention","full_attention"]}}`, "model.safetensors")
	t.Setenv(sglangExtraArgsEnv, "")
	t.Setenv(sglangCompatExtraEnv, "")
	t.Setenv(sglangMemFractionEnv, "")
	opts := SGLangLaunchOptions{ModelDir: dir, ModelIdentity: "Qwen/Qwen3.8-27B",
		RuntimeVersion: "0.5.19", NumCtx: 8192, NumParallel: 4,
		Devices: []SGLangDevice{{Name: "NVIDIA A100-SXM4-80GB", MemoryTotal: 80 << 30}},
	}
	plan, err := PlanSGLangLaunch(opts)
	if err != nil {
		t.Fatal(err)
	}
	if alg, _ := sglangFlagValue(plan.Args, "--speculative-algorithm"); alg != "NEXTN" ||
		plan.MaxTotalTokens != 32768 || plan.MemFractionStatic != .90 {
		t.Fatalf("wrong A100 MTP plan: %v", plan.Args)
	}
	opts.DeploymentArgs = "--speculative-algorithm none"
	plan, err = PlanSGLangLaunch(opts)
	if err != nil || sglangFlagSet(plan.Args)["--speculative-algorithm"] {
		t.Fatalf("manual opt-out failed: %v %v", plan.Args, err)
	}
	opts.DeploymentArgs, opts.NumCtx = "", 160000
	plan, err = PlanSGLangLaunch(opts)
	if err != nil || sglangFlagSet(plan.Args)["--speculative-algorithm"] {
		t.Fatalf("MTP escaped tested context: %v %v", plan.Args, err)
	}
}
