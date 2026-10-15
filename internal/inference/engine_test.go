package inference

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/model"
)

func TestLoadEngine_SafeTensorsAutoConvert(t *testing.T) {
	if !convert.PythonConverterAvailable() {
		t.Skip("Python conversion environment is not available")
	}
	dir := t.TempDir()
	// SafeTensors without config.json should fail during conversion (missing config).
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write weights: %v", err)
	}

	lm := &model.LocalModel{
		Namespace: "test",
		Name:      "model",
		Format:    model.FormatSafeTensors,
	}

	_, err := LoadEngine(dir, lm)
	if err == nil {
		t.Fatal("expected error for SafeTensors model without config.json")
	}
	// Should report auto-conversion failure (not ErrUnsupportedFormat).
	if strings.Contains(err.Error(), "auto-converting SafeTensors") {
		return // expected
	}
	t.Errorf("unexpected error: %v", err)
}

func TestLoadEngine_UnsupportedSafeTensorsArchitectureDoesNotConvert(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write weights: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"architectures":["JinaEmbeddingsV5OmniModel"]}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	lm := &model.LocalModel{
		Namespace: "jinaai",
		Name:      "jina-embeddings-v5-omni-nano",
		Format:    model.FormatSafeTensors,
	}

	_, err := LoadEmbeddingEngineWithProgress(dir, lm, nil, false, 0, 0, -1, "", "", "")
	if err == nil {
		t.Fatal("expected unsupported architecture error")
	}
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("error = %v, want ErrUnsupportedFormat", err)
	}
	if !strings.Contains(err.Error(), "JinaEmbeddingsV5OmniModel") {
		t.Fatalf("error = %q, want architecture name", err.Error())
	}
	if strings.Contains(err.Error(), "auto-converting SafeTensors") {
		t.Fatalf("error = %q, should fail before conversion", err.Error())
	}
}

func TestReadHFArchitectureRecognizesDecisionCheckpoint(t *testing.T) {
	dir := t.TempDir()
	for rel, content := range map[string]string{
		"config.json":          `{"model_type":"laya","architectures":["SomeFutureLayaName"]}`,
		"encoder/config.json":  `{"architectures":["ModernBertModel"]}`,
		"rl_agent_config.json": `{"head_layers":2}`,
	} {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	arch, ok, err := readHFArchitecture(dir)
	if err != nil || !ok || arch != convert.DecisionCheckpointArchitecture {
		t.Fatalf("readHFArchitecture = %q, %t, %v", arch, ok, err)
	}
	if err := ensureConvertibleHFArchitecture(dir); err != nil {
		t.Fatalf("decision checkpoint refused: %v", err)
	}
}

func TestDecisionOnlyEngineRefusesTextGeneration(t *testing.T) {
	engine := &llamaEngine{modelName: "org/laya", decisionType: "laya"}
	_, err := engine.postChatCompletion(t.Context(), map[string]interface{}{})
	if HTTPStatusCode(err) != 400 || !strings.Contains(HTTPErrorMessage(err), "/v1/decisions") {
		t.Fatalf("chat on a laya engine: %v", err)
	}
	if (&llamaEngine{decisionType: "openjev"}).errDecisionOnly() != nil {
		t.Fatal("openjev generates text and must not be refused")
	}
	if args := decisionOnlyBatchArgs(8192); strings.Join(args, " ") != "--batch-size 8192 --ubatch-size 8192" {
		t.Fatalf("batch args = %v", args)
	}
}

func TestLoadEngine_NoModelFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	lm := &model.LocalModel{
		Namespace: "test",
		Name:      "model",
		Format:    model.FormatUnknown,
	}

	_, err := LoadEngine(dir, lm)
	if err == nil {
		t.Fatal("expected error when no model file exists")
	}
}

func TestShouldRemoveConvertedGGUF(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "keep oom failure",
			err:  errors.New("llama-server failed to start: ggml_backend_cuda_buffer_type_alloc_buffer: allocating 6999.05 MiB on device 2: cudaMalloc failed: out of memory"),
			want: false,
		},
		{
			name: "keep timeout failure",
			err:  errors.New("llama-server failed to start: timeout waiting for llama-server to be ready"),
			want: false,
		},
		{
			name: "remove corrupt gguf failure",
			err:  errors.New("llama-server failed to start: invalid magic characters 0x00 0x00 0x00 0x00"),
			want: true,
		},
		{
			name: "remove truncated gguf failure",
			err:  errors.New("llama-server failed to start: tensor data is not within file bounds (truncated)"),
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRemoveConvertedGGUF(tc.err); got != tc.want {
				t.Fatalf("shouldRemoveConvertedGGUF(%q) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
