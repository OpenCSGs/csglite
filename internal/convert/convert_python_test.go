package convert

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRepairPlanForConverterFailureGGUF(t *testing.T) {
	output := `
INFO:hf-to-gguf:Model architecture: Gemma4ForConditionalGeneration
model_arch = gguf.MODEL_ARCH.GEMMA4
AttributeError: GEMMA4. Did you mean: 'GEMMA'?
`

	got := repairPlanForConverterFailure(output)
	want := converterRepairPlan{
		installBundledGGUFPy: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repairPlanForConverterFailure() = %#v, want %#v", got, want)
	}
}

func TestRepairPlanForConverterFailureMissingGGUFModule(t *testing.T) {
	output := `
Traceback (most recent call last):
  File "convert_hf_to_gguf.py", line 30, in <module>
    import gguf
ModuleNotFoundError: No module named 'gguf'
`

	got := repairPlanForConverterFailure(output)
	want := converterRepairPlan{
		installBundledGGUFPy: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repairPlanForConverterFailure() = %#v, want %#v", got, want)
	}
}

func TestRepairPlanForConverterFailureTransformers(t *testing.T) {
	output := `
The checkpoint you are trying to load has model type "gemma4" but Transformers does not recognize this architecture.
You can update Transformers with the command "pip install --upgrade transformers".
`

	got := repairPlanForConverterFailure(output)
	want := converterRepairPlan{upgradePackages: []string{"transformers"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repairPlanForConverterFailure() = %#v, want %#v", got, want)
	}
}

func TestRepairPlanForConverterFailureSentencePiece(t *testing.T) {
	output := `
Traceback (most recent call last):
  File "convert_hf_to_gguf.py", line 1652, in _create_vocab_sentencepiece
    from sentencepiece import SentencePieceProcessor
ModuleNotFoundError: No module named 'sentencepiece'
`

	got := repairPlanForConverterFailure(output)
	want := converterRepairPlan{upgradePackages: []string{"sentencepiece"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repairPlanForConverterFailure() = %#v, want %#v", got, want)
	}
}

func TestRepairPlanForConverterFailureProtobuf(t *testing.T) {
	output := `
Traceback (most recent call last):
  File "sentencepiece_model_pb2.py", line 5, in <module>
    from google.protobuf.internal import builder as _builder
ModuleNotFoundError: No module named 'google'
`

	got := repairPlanForConverterFailure(output)
	want := converterRepairPlan{upgradePackages: []string{"protobuf"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repairPlanForConverterFailure() = %#v, want %#v", got, want)
	}
}

func TestRepairPlanForConverterFailureDeduplicates(t *testing.T) {
	output := `
The checkpoint you are trying to load has model type "gemma4" but Transformers does not recognize this architecture.
You can update Transformers with the command "pip install --upgrade transformers".
model_arch = gguf.MODEL_ARCH.GEMMA4
AttributeError: GEMMA4. Did you mean: 'GEMMA'?
`

	got := repairPlanForConverterFailure(output)
	want := converterRepairPlan{
		installBundledGGUFPy: true,
		upgradePackages:      []string{"transformers"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repairPlanForConverterFailure() = %#v, want %#v", got, want)
	}
}

func TestPythonDepsInstallHintUsesManagedVenv(t *testing.T) {
	got := pythonDepsInstallHintForGOOS("darwin")
	if strings.Contains(got, "pip3 install") {
		t.Fatalf("pythonDepsInstallHintForGOOS(darwin) should not suggest global pip3 install: %q", got)
	}
	for _, want := range []string{
		"python3 -m venv ~/.csghub-lite/tools/python",
		"~/.csghub-lite/tools/python/bin/python -m pip install --upgrade --index-url https://mirrors.aliyun.com/pypi/simple pip",
		"~/.csghub-lite/tools/python/bin/python -m pip install --index-url https://mirrors.aliyun.com/pypi/simple --find-links https://mirrors.aliyun.com/pytorch-wheels/cpu torch",
		"~/.csghub-lite/tools/python/bin/python -m pip install --index-url https://mirrors.aliyun.com/pypi/simple safetensors transformers sentencepiece protobuf",
		"csghub-lite automatically tries the official PyTorch CPU index if the Aliyun mirror is unavailable.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("pythonDepsInstallHintForGOOS(darwin) missing %q in %q", want, got)
		}
	}
}

func TestPythonNotFoundOrUnsupportedMessageMentionsOldVersion(t *testing.T) {
	got := pythonNotFoundOrUnsupportedMessage("/usr/bin/python3 (Python 3.8.18)")
	if !strings.Contains(got, "Python 3.8.18") || !strings.Contains(got, "requires Python 3.9+") {
		t.Fatalf("unsupported Python message = %q", got)
	}
}

func TestPythonDepsInstallHintUsesManagedVenvOnWindows(t *testing.T) {
	got := pythonDepsInstallHintForGOOS("windows")
	for _, want := range []string{
		`py -m venv "%USERPROFILE%\.csghub-lite\tools\python"`,
		`"%USERPROFILE%\.csghub-lite\tools\python\Scripts\python.exe" -m pip install --upgrade --index-url https://mirrors.aliyun.com/pypi/simple pip`,
		`"%USERPROFILE%\.csghub-lite\tools\python\Scripts\python.exe" -m pip install --index-url https://mirrors.aliyun.com/pypi/simple --find-links https://mirrors.aliyun.com/pytorch-wheels/cpu torch`,
		`"%USERPROFILE%\.csghub-lite\tools\python\Scripts\python.exe" -m pip install --index-url https://mirrors.aliyun.com/pypi/simple safetensors transformers sentencepiece protobuf`,
		"csghub-lite automatically tries the official PyTorch CPU index if the Aliyun mirror is unavailable.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("pythonDepsInstallHintForGOOS(windows) missing %q in %q", want, got)
		}
	}
}

func TestHintForConverterScriptFailureUsesEmbeddedGGUFPy(t *testing.T) {
	output := `
INFO:hf-to-gguf:Model architecture: Gemma4ForConditionalGeneration
model_arch = gguf.MODEL_ARCH.GEMMA4
AttributeError: GEMMA4. Did you mean: 'GEMMA'?
`
	got := hintForConverterScriptFailure(output)
	if !strings.Contains(got, "includes matching `gguf-py`") || strings.Contains(got, "git+") {
		t.Fatalf("hintForConverterScriptFailure() should describe the embedded package: %q", got)
	}
}

func TestConverterErrorfIncludesBundledVersion(t *testing.T) {
	got := converterErrorf("example failure").Error()
	if !strings.Contains(got, "Converter version: llama.cpp "+BundledConverterLLamacppRef) {
		t.Fatalf("converterErrorf() missing llama.cpp tag: %q", got)
	}
	if !strings.Contains(got, fmt.Sprintf("bundled revision %d", bundledConverterRevision)) {
		t.Fatalf("converterErrorf() missing bundled revision: %q", got)
	}
}

func TestConverterErrorfUsesCustomConverterSource(t *testing.T) {
	t.Setenv("CSGHUB_LITE_CONVERTER_URL", "https://example.com/convert_hf_to_gguf.py")
	got := converterErrorf("example failure").Error()
	if !strings.Contains(got, "Converter source: CSGHUB_LITE_CONVERTER_URL=https://example.com/convert_hf_to_gguf.py") {
		t.Fatalf("converterErrorf() missing custom converter source: %q", got)
	}
}

func TestEnsureSentenceTransformerPoolingConfigBackfillsBGE(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bge-m3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}
	modules := `[
  {"idx":0,"name":"0","path":"","type":"sentence_transformers.models.Transformer"},
  {"idx":1,"name":"1","path":"1_Pooling","type":"sentence_transformers.models.Pooling"}
]`
	if err := os.WriteFile(filepath.Join(dir, "modules.json"), []byte(modules), 0o644); err != nil {
		t.Fatalf("write modules.json: %v", err)
	}

	if err := ensureSentenceTransformerPoolingConfig(dir); err != nil {
		t.Fatalf("ensureSentenceTransformerPoolingConfig() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "1_Pooling", "config.json"))
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode generated config: %v", err)
	}
	if cfg["pooling_mode"] != "cls" || cfg["pooling_mode_cls_token"] != true || cfg["pooling_mode_mean_tokens"] != false {
		t.Fatalf("generated pooling config = %#v, want cls pooling", cfg)
	}
}

func TestEnsureSentenceTransformerPoolingConfigPreservesExisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nomic-embed-text-v1.5")
	poolingDir := filepath.Join(dir, "1_Pooling")
	if err := os.MkdirAll(poolingDir, 0o755); err != nil {
		t.Fatalf("mkdir pooling dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules.json"), []byte(`[{"path":"1_Pooling","type":"sentence_transformers.models.Pooling"}]`), 0o644); err != nil {
		t.Fatalf("write modules.json: %v", err)
	}
	existing := []byte(`{"pooling_mode":"mean","custom":true}`)
	if err := os.WriteFile(filepath.Join(poolingDir, "config.json"), existing, 0o644); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	if err := ensureSentenceTransformerPoolingConfig(dir); err != nil {
		t.Fatalf("ensureSentenceTransformerPoolingConfig() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(poolingDir, "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(got) != string(existing) {
		t.Fatalf("existing config was changed: %s", got)
	}
}

func TestFormatConverterFailureIncludesConverterVersion(t *testing.T) {
	got := formatConverterFailure(fmt.Errorf("exit status 1"), "Traceback\nline2", "").Error()
	if !strings.Contains(got, "Converter version: llama.cpp "+BundledConverterLLamacppRef) {
		t.Fatalf("formatConverterFailure() missing llama.cpp tag: %q", got)
	}
}
