package inference

import (
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
