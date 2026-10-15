package model

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsDiffusionGGUF(t *testing.T) {
	writeCard := func(t *testing.T, dir, card string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(card), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ggufWithArch := func(t *testing.T, dir, name, arch string) {
		t.Helper()
		writeTestGGUF(t, filepath.Join(dir, name), []testGGUFMetadata{
			{key: "general.architecture", valueType: ggufMetadataString, value: arch},
		})
	}

	t.Run("model card image task", func(t *testing.T) {
		dir := t.TempDir()
		writeCard(t, dir, "---\nlibrary_name: gguf\npipeline_tag: text-to-image\n---\n")
		ggufWithArch(t, dir, "qwen-image-2.1-turbo-Q4_K_M.gguf", "qwen_image")
		if !IsDiffusionGGUF(dir) {
			t.Fatal("IsDiffusionGGUF() = false, want true")
		}
		if got := ImageBackendFor(dir, "unsloth/Qwen-Image-2.1-Turbo-GGUF"); got != ImageBackendGGUFUnsupported {
			t.Fatalf("ImageBackendFor() = %q, want %q", got, ImageBackendGGUFUnsupported)
		}
	})

	t.Run("diffusion architecture without card", func(t *testing.T) {
		dir := t.TempDir()
		ggufWithArch(t, dir, "flux1-dev-Q4_0.gguf", "flux")
		if !IsDiffusionGGUF(dir) {
			t.Fatal("IsDiffusionGGUF() = false, want true")
		}
		// Without the image tag such a file used to be routed to llama-server.
		if got := DetectPipelineTag(dir); got != "text-to-image" {
			t.Fatalf("DetectPipelineTag() = %q, want text-to-image", got)
		}
	})

	t.Run("text model", func(t *testing.T) {
		dir := t.TempDir()
		ggufWithArch(t, dir, "Qwen3-8B-Q4_K_M.gguf", "qwen3")
		if IsDiffusionGGUF(dir) {
			t.Fatal("IsDiffusionGGUF() = true for a text GGUF")
		}
		if got := DetectPipelineTag(dir); got != "text-generation" {
			t.Fatalf("DetectPipelineTag() = %q, want text-generation", got)
		}
	})

	t.Run("diffusers pipeline", func(t *testing.T) {
		dir := t.TempDir()
		writeCard(t, dir, "---\npipeline_tag: text-to-image\n---\n")
		if err := os.WriteFile(filepath.Join(dir, "model_index.json"), []byte(`{"_class_name":"QwenImagePipeline"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		ggufWithArch(t, dir, "extra.gguf", "qwen_image")
		if IsDiffusionGGUF(dir) {
			t.Fatal("IsDiffusionGGUF() = true for a Diffusers pipeline")
		}
	})
}

func TestImageBackendForMFluxBundleTaggedOnly(t *testing.T) {
	// OsaurusAI's mflux bundles declare the library only as a card tag.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("---\npipeline_tag: text-to-image\ntags: [text-to-image, mflux, turbo]\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model_index.json"), []byte(`{"_class_name":"QwenImage21Pipeline"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ImageBackendFor(dir, "OsaurusAI/Qwen-Image-2.1-Turbo-mflux-4bit"); got != ImageBackendMLXQwen21 {
		t.Fatalf("ImageBackendFor() = %q, want %q", got, ImageBackendMLXQwen21)
	}
	if got := ImageBackendForMetadata("OsaurusAI/Qwen-Image-2.1-Turbo-mflux-4bit", "", "", "text-to-image"); got != ImageBackendMLXQwen21 {
		t.Fatalf("ImageBackendForMetadata() = %q, want %q", got, ImageBackendMLXQwen21)
	}
}
