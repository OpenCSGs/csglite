package inference

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/model"
)

func TestDeploymentEmbeddingPoolingUsesArtifact(t *testing.T) {
	t.Setenv("CSGHUB_LITE_LLAMA_EMBEDDING_POOLING", "")
	for _, tc := range []struct{ directory, file, alias, want string }{
		{"Qwen3-Embedding-0.6B", "model.gguf", "deployment/test-id", "last"},
		{"bge-m3", "model.gguf", "deployment/test-id", "cls"},
		{"multilingual-e5-large", "model.gguf", "deployment/test-id", "mean"},
		{"gte-Qwen2-7B-instruct", "model.gguf", "deployment/test-id", "last"},
		{"custom", "Qwen3-Embedding-0.6B-f16.gguf", "deployment/test-id", "last"},
		{"custom", "model.gguf", "Qwen/Qwen3-Embedding-0.6B", "last"},
	} {
		t.Run(tc.directory+"/"+tc.file, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.directory, tc.file)
			if got := resolveEmbeddingPoolingForModel(path, tc.alias); got != tc.want {
				t.Fatalf("pooling = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDeploymentEmbeddingPoolingPrefersGGUFMetadata(t *testing.T) {
	t.Setenv("CSGHUB_LITE_LLAMA_EMBEDDING_POOLING", "")
	for _, tc := range []struct {
		pooling uint32
		want    string
	}{{1, "mean"}, {2, "cls"}, {3, "last"}, {4, "rank"}} {
		t.Run(tc.want, func(t *testing.T) {
			var data bytes.Buffer
			data.WriteString("GGUF")
			write := func(value any) {
				t.Helper()
				if err := binary.Write(&data, binary.LittleEndian, value); err != nil {
					t.Fatal(err)
				}
			}
			stringValue := func(value string) { write(uint64(len(value))); data.WriteString(value) }
			write(uint32(3))
			write(uint64(0))
			write(uint64(2))
			stringValue("general.architecture")
			write(uint32(8)) // GGUF_TYPE_STRING
			stringValue("qwen3")
			stringValue("qwen3.pooling_type")
			write(uint32(4)) // GGUF_TYPE_UINT32
			write(tc.pooling)
			path := filepath.Join(t.TempDir(), "Qwen3-Embedding-0.6B.gguf")
			if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := resolveEmbeddingPoolingForModel(path, "deployment/test-id"); got != tc.want {
				t.Fatalf("pooling = %q, want GGUF metadata %q", got, tc.want)
			}
			t.Setenv("CSGHUB_LITE_LLAMA_EMBEDDING_POOLING", "cls")
			if got := resolveEmbeddingPoolingForModel(path, "deployment/test-id"); got != "cls" {
				t.Fatalf("explicit pooling override = %q, want cls", got)
			}
		})
	}
}

func TestEmbeddingDeploymentRuntimeIntegration(t *testing.T) {
	dir := os.Getenv("CSGHUB_LITE_TEST_EMBEDDING_MODEL_DIR")
	if dir == "" {
		t.Skip("set CSGHUB_LITE_TEST_EMBEDDING_MODEL_DIR to a local Qwen3-Embedding-0.6B model directory")
	}
	if _, ok, err := convert.FindGGUFForDType(dir, "f16"); err != nil || !ok {
		t.Fatalf("integration test requires an existing f16 GGUF: %v", err)
	}
	t.Setenv("CSGHUB_LITE_LLAMA_EMBEDDING_POOLING", "")
	lm := &model.LocalModel{Namespace: "deployment", Name: "pooling-integration", Format: model.FormatGGUF}
	engine, err := LoadEmbeddingEngineWithProgress(dir, lm, nil, false, 4096, 5, -1, "", "", "f16")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	if engine.ModelName() != "deployment/pooling-integration" {
		t.Fatalf("deployment alias was not preserved: %s", engine.ModelName())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	response, err := engine.(EmbeddingsProxier).Embeddings(ctx, map[string]interface{}{
		"model": "deployment/pooling-integration", "input": []string{"hello world", "你好"}, "encoding_format": "float",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("embedding request status = %d", response.StatusCode)
	}
	var result struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 2 {
		t.Fatalf("embedding count = %d, want 2", len(result.Data))
	}
	for _, item := range result.Data {
		if len(item.Embedding) != 1024 {
			t.Fatalf("embedding dimensions = %d, want 1024", len(item.Embedding))
		}
		for _, value := range item.Embedding {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				t.Fatal("embedding contains a non-finite value")
			}
		}
	}
}
