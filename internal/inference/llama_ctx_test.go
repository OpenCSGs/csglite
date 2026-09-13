package inference

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveNumCtxUsesExplicitRequest(t *testing.T) {
	dir := t.TempDir()
	if got := ResolveNumCtx(dir, 12288); got != 12288 {
		t.Fatalf("ResolveNumCtx returned %d, want %d", got, 12288)
	}
}

func TestResolveNumCtxUsesEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_CTX", "24576")

	if got := ResolveNumCtx(dir, 0); got != 24576 {
		t.Fatalf("ResolveNumCtx returned %d, want %d", got, 24576)
	}
}

func TestResolveNumCtxUsesEnvOverrideBeforeModelMax(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"max_position_embeddings":40960}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_CTX", "24576")
	t.Setenv(useModelMaxCtxEnv, "true")

	if got := ResolveNumCtx(dir, 0); got != 24576 {
		t.Fatalf("ResolveNumCtx returned %d, want %d", got, 24576)
	}
}

func TestResolveNumCtxWithModelSettingPrefersRequest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_CTX", "16384")

	if got := ResolveNumCtxWithModelSetting(dir, 12288, 65536, false); got != 12288 {
		t.Fatalf("ResolveNumCtxWithModelSetting returned %d, want %d", got, 12288)
	}
}

func TestResolveNumCtxWithModelSettingBeatsGlobal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_CTX", "16384")

	if got := ResolveNumCtxWithModelSetting(dir, 0, 65536, false); got != 65536 {
		t.Fatalf("ResolveNumCtxWithModelSetting returned %d, want %d", got, 65536)
	}
}

func TestResolveNumCtxWithModelSettingFallsBackToGlobal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_CTX", "16384")

	if got := ResolveNumCtxWithModelSetting(dir, 0, 0, false); got != 16384 {
		t.Fatalf("ResolveNumCtxWithModelSetting returned %d, want %d", got, 16384)
	}
}

func TestResolveNumCtxWithModelSettingKeepsExistingLogicWhenUnset(t *testing.T) {
	dir := t.TempDir()

	if got := ResolveNumCtxWithModelSetting(dir, 0, 0, false); got != defaultLlamaCtxSize {
		t.Fatalf("ResolveNumCtxWithModelSetting returned %d, want %d", got, defaultLlamaCtxSize)
	}
}

func TestResolveNumCtxWithModelSettingIgnoresTooSmallSetting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_CTX", "16384")

	if got := ResolveNumCtxWithModelSetting(dir, 0, 512, false); got != 16384 {
		t.Fatalf("ResolveNumCtxWithModelSetting returned %d, want %d", got, 16384)
	}
}

func TestResolveNumCtxCapsEnvOverrideAtModelMax(t *testing.T) {
	dir := t.TempDir()
	// Qwen3-Embedding-0.6B tops out at 32768; a global 128K override must not
	// make llama-server reserve a KV cache four times larger than the model
	// can ever use.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"max_position_embeddings":32768}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_CTX", "131072")

	if got := ResolveNumCtx(dir, 0); got != 32768 {
		t.Fatalf("ResolveNumCtx returned %d, want %d", got, 32768)
	}
}

func TestResolveNumCtxCapsEnvOverrideAtGGUFModelMax(t *testing.T) {
	dir := t.TempDir()
	if err := writeMinimalGGUFContextLength(filepath.Join(dir, "model.gguf"), 32768); err != nil {
		t.Fatalf("write gguf: %v", err)
	}
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_CTX", "131072")

	if got := ResolveNumCtx(dir, 0); got != 32768 {
		t.Fatalf("ResolveNumCtx returned %d, want %d", got, 32768)
	}
}

func TestResolveNumCtxKeepsEnvOverrideWhenModelMaxUnknown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_CTX", "131072")

	if got := ResolveNumCtx(dir, 0); got != 131072 {
		t.Fatalf("ResolveNumCtx returned %d, want %d", got, 131072)
	}
}

func TestResolveNumCtxUsesModelMaxWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"max_position_embeddings":40960}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	t.Setenv(useModelMaxCtxEnv, "true")

	if got := ResolveNumCtx(dir, 0); got != 40960 {
		t.Fatalf("ResolveNumCtx returned %d, want %d", got, 40960)
	}
}

func TestResolveNumCtxUsesPersistedModelMaxSetting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"max_position_embeddings":40960}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	if got := ResolveNumCtxWithModelMax(dir, 0, true); got != 40960 {
		t.Fatalf("ResolveNumCtxWithModelMax returned %d, want %d", got, 40960)
	}
}

func TestResolveNumCtxEnvOverridesPersistedModelMaxSetting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"max_position_embeddings":40960}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	t.Setenv(useModelMaxCtxEnv, "false")

	if got := ResolveNumCtxWithModelMax(dir, 0, true); got != 16384 {
		t.Fatalf("ResolveNumCtxWithModelMax returned %d, want %d", got, 16384)
	}
}

func TestResolveNumCtxUsesNestedModelMaxWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"text_config":{"max_position_embeddings":262144}}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	t.Setenv(useModelMaxCtxEnv, "true")

	if got := ResolveNumCtx(dir, 0); got != 262144 {
		t.Fatalf("ResolveNumCtx returned %d, want %d", got, 262144)
	}
}

func TestResolveNumCtxUsesGGUFModelMaxWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	if err := writeMinimalGGUFContextLength(filepath.Join(dir, "model.gguf"), 131072); err != nil {
		t.Fatalf("write gguf: %v", err)
	}
	t.Setenv(useModelMaxCtxEnv, "true")

	if got := ResolveNumCtx(dir, 0); got != 131072 {
		t.Fatalf("ResolveNumCtx returned %d, want %d", got, 131072)
	}
}

func TestResolveNumCtxExpandsFromModelConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"max_position_embeddings":40960}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	if got := ResolveNumCtx(dir, 0); got != 16384 {
		t.Fatalf("ResolveNumCtx returned %d, want %d", got, 16384)
	}
}

func TestResolveNumCtxFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()

	if got := ResolveNumCtx(dir, 0); got != 8192 {
		t.Fatalf("ResolveNumCtx returned %d, want %d", got, 8192)
	}
}

func TestResolveNumParallelFallsBackToSingleSlot(t *testing.T) {
	if got := ResolveNumParallel(0); got != 1 {
		t.Fatalf("ResolveNumParallel returned %d, want 1", got)
	}
}

func TestResolveEmbeddingPoolingUsesBGECLS(t *testing.T) {
	if got := ResolveEmbeddingPooling("BAAI/bge-m3"); got != "cls" {
		t.Fatalf("ResolveEmbeddingPooling returned %q, want cls", got)
	}
}

func TestResolveEmbeddingPoolingForCommonFamilies(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"Qwen/Qwen3-Embedding-8B", "last"},
		{"Qwen/Qwen3-Embedding-0.6B", "last"},
		{"Alibaba-NLP/gte-Qwen2-7B-instruct", "last"},
		{"Alibaba-NLP/gte-large-en-v1.5", "cls"},
		{"intfloat/multilingual-e5-large-instruct", "mean"},
		{"nomic-ai/nomic-embed-text-v1.5", "mean"},
		{"jinaai/jina-embeddings-v2-base-en", "mean"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := ResolveEmbeddingPooling(tt.model); got != tt.want {
				t.Fatalf("ResolveEmbeddingPooling(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestResolveEmbeddingPoolingUsesEnvOverride(t *testing.T) {
	t.Setenv("CSGHUB_LITE_LLAMA_EMBEDDING_POOLING", "mean")
	if got := ResolveEmbeddingPooling("BAAI/bge-m3"); got != "mean" {
		t.Fatalf("ResolveEmbeddingPooling returned %q, want mean", got)
	}
}

func TestResolveNGPULayersUsesExplicitRequest(t *testing.T) {
	if got := ResolveNGPULayers(42); got != 42 {
		t.Fatalf("ResolveNGPULayers returned %d, want %d", got, 42)
	}
}

func TestResolveNGPULayersKeepsZeroForCPUOnly(t *testing.T) {
	if got := ResolveNGPULayers(0); got != 0 {
		t.Fatalf("ResolveNGPULayers returned %d, want 0", got)
	}
}

func TestResolveNGPULayersLeavesUnsetForAutoFit(t *testing.T) {
	// Unset must stay -1 so llama-server's fit feature can auto-adjust
	// GPU offload; forcing a value disables that adjustment.
	if got := ResolveNGPULayers(-1); got != unsetNGPULayers {
		t.Fatalf("ResolveNGPULayers returned %d, want %d", got, unsetNGPULayers)
	}
}

func TestNormalizeNGPULayersRejectsLessThanUnset(t *testing.T) {
	if _, err := NormalizeNGPULayers(-2); err == nil {
		t.Fatal("expected invalid n_gpu_layers error")
	}
}

func writeMinimalGGUFContextLength(path string, contextLength uint32) error {
	var buf bytes.Buffer
	buf.WriteString("GGUF")
	if err := binary.Write(&buf, binary.LittleEndian, uint32(3)); err != nil {
		return err
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint64(0)); err != nil {
		return err
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint64(1)); err != nil {
		return err
	}
	key := []byte("llama.context_length")
	if err := binary.Write(&buf, binary.LittleEndian, uint64(len(key))); err != nil {
		return err
	}
	if _, err := buf.Write(key); err != nil {
		return err
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint32(4)); err != nil { // GGUF_TYPE_UINT32
		return err
	}
	if err := binary.Write(&buf, binary.LittleEndian, contextLength); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// llama-server allocates the context per slot, so an embedding model given far
// more context than it can consume reserves KV cache it will never use, and the
// waste multiplies with the slot count. 512-token encoders are the norm, so the
// cap has to honour limits below the 1024 floor the chat path uses.
func TestCapNumCtxToEmbeddingModelMax(t *testing.T) {
	cases := []struct {
		name   string
		maxPos string
		numCtx int
		want   int
	}{
		{name: "caps to a 512-token encoder", maxPos: "512", numCtx: 8192, want: 512},
		{name: "leaves a context inside the limit alone", maxPos: "512", numCtx: 256, want: 256},
		{name: "caps a long-context embedding model too", maxPos: "8192", numCtx: 32768, want: 8192},
		{name: "ignores an implausibly small limit", maxPos: "8", numCtx: 8192, want: 8192},
		{name: "keeps the value when the limit is unknown", maxPos: "", numCtx: 8192, want: 8192},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.maxPos != "" {
				body := `{"max_position_embeddings":` + tc.maxPos + `}`
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := CapNumCtxToEmbeddingModelMax(dir, tc.numCtx); got != tc.want {
				t.Fatalf("CapNumCtxToEmbeddingModelMax(%d) = %d, want %d", tc.numCtx, got, tc.want)
			}
		})
	}
}
