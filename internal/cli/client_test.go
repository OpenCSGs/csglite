package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestPreloadModelIncludesRequestedContextOptions(t *testing.T) {
	var got api.LoadRequest

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/load" {
			t.Fatalf("path = %s, want /api/load", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"status\":\"ready\"}\n\n")
	}))
	defer ts.Close()

	if err := preloadModel(ts.URL, "Qwen/Qwen3-0.6B-GGUF", 131072, 1, 40, "q8_0", "bf16", "q8_0", "-1"); err != nil {
		t.Fatalf("preloadModel returned error: %v", err)
	}

	if got.Model != "Qwen/Qwen3-0.6B-GGUF" {
		t.Fatalf("model = %q, want Qwen/Qwen3-0.6B-GGUF", got.Model)
	}
	if got.Stream == nil || !*got.Stream {
		t.Fatalf("stream = %#v, want true", got.Stream)
	}
	if got.KeepAlive != "-1" {
		t.Fatalf("keep_alive = %q, want -1", got.KeepAlive)
	}
	if got.NumCtx != 131072 {
		t.Fatalf("num_ctx = %d, want 131072", got.NumCtx)
	}
	if got.NumParallel != 1 {
		t.Fatalf("num_parallel = %d, want 1", got.NumParallel)
	}
	if got.NGPULayers == nil || *got.NGPULayers != 40 {
		t.Fatalf("n_gpu_layers = %#v, want 40", got.NGPULayers)
	}
	if got.CacheTypeK != "q8_0" {
		t.Fatalf("cache_type_k = %q, want q8_0", got.CacheTypeK)
	}
	if got.CacheTypeV != "bf16" {
		t.Fatalf("cache_type_v = %q, want bf16", got.CacheTypeV)
	}
	if got.DType != "q8_0" {
		t.Fatalf("dtype = %q, want q8_0", got.DType)
	}
}
