package imagegen

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/logutil"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestResolveImageBackend(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("---\nlibrary_name: mlx\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model_index.json"), []byte(`{"_class_name":"QwenImage21Pipeline"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(qwen21RuntimeEnv, "auto")
	got, err := ResolveImageBackend(dir, "mlx-community/Qwen-Image-2.1-MLX-4bit")
	if err != nil {
		t.Fatal(err)
	}
	if got != "mlx-qwen-image-2.1" {
		t.Fatalf("ResolveImageBackend() = %q, want mlx-qwen-image-2.1", got)
	}

	t.Setenv(qwen21RuntimeEnv, "diffusers")
	if _, err := ResolveImageBackend(dir, "mlx-community/Qwen-Image-2.1-MLX-4bit"); err == nil {
		t.Fatal("ResolveImageBackend() with diffusers override succeeded for MLX weights")
	}
}

func TestResolveImageBackendRejectsOtherMLXImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("---\nlibrary_name: mlx\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model_index.json"), []byte(`{"_class_name":"FluxPipeline"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(qwen21RuntimeEnv, "diffusers")
	if _, err := ResolveImageBackend(dir, "mlx-community/FLUX.1-schnell-4bit"); err == nil {
		t.Fatal("ResolveImageBackend() accepted an MLX image checkpoint that is not Qwen-Image-2.1")
	}
}

func TestResolveImageBackendRejectsInvalidOverride(t *testing.T) {
	t.Setenv(qwen21RuntimeEnv, "invalid")
	if _, err := ResolveImageBackend(t.TempDir(), "owner/model"); err == nil {
		t.Fatal("ResolveImageBackend() accepted invalid override")
	}
}

func TestDiffusersGenerateReportsWorkerCrash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	port, err := strconv.Atoi(server.URL[strings.LastIndex(server.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}

	logBuf := logutil.NewTailWriter(64 * 1024)
	_, _ = logBuf.Write([]byte(" 0%|  | 0/40\r 2%|  | 1/40\nlibc++abi: terminating due to uncaught exception of type std::runtime_error: There is no Stream(gpu, 1) in current thread.\n"))
	exitCh := make(chan error, 1)
	exitCh <- errors.New("signal: abort trap")
	close(exitCh)
	engine := &DiffusersEngine{port: port, client: server.Client(), exitCh: exitCh, logBuf: logBuf}

	_, err = engine.Generate(context.Background(), api.OpenAIImagesGenerationRequest{Prompt: "apple"})
	if err == nil {
		t.Fatal("Generate() succeeded against a crashed worker")
	}
	for _, want := range []string{"exited during generation", "signal: abort trap", "There is no Stream(gpu, 1)"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Generate() error = %q, want it to contain %q", err, want)
		}
	}
}

func TestResolveImageBackendRejectsGGUFImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("---\nlibrary_name: gguf\npipeline_tag: text-to-image\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qwen-image-2.1-turbo-Q4_K_M.gguf"), []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveImageBackend(dir, "unsloth/Qwen-Image-2.1-Turbo-GGUF")
	if err == nil || !strings.Contains(err.Error(), "GGUF image model") {
		t.Fatalf("ResolveImageBackend() error = %v, want GGUF image model refusal", err)
	}
}
