package imagegen

import (
	"os"
	"path/filepath"
	"testing"
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
