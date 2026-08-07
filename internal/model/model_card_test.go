package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectModelCardPipelineTag(t *testing.T) {
	tests := []struct {
		name    string
		readme  string
		wantTag string
	}{
		{
			name:    "Hugging Face pipeline tag",
			readme:  "---\npipeline_tag: image-text-to-text\n---\n# Model\n",
			wantTag: "image-text-to-text",
		},
		{
			name:    "pipeline tag alias",
			readme:  "---\npipeline-tag: image_to_text\n---\n",
			wantTag: "image-text-to-text",
		},
		{
			name:    "task alias",
			readme:  "---\ntask: speech-to-text\n---\n",
			wantTag: "automatic-speech-recognition",
		},
		{
			name:    "tags fallback",
			readme:  "---\ntags:\n  - gguf\n  - multimodal\n---\n",
			wantTag: "image-text-to-text",
		},
		{
			name:    "malformed front matter",
			readme:  "---\npipeline_tag: [\n---\n",
			wantTag: "",
		},
		{
			name:    "markdown only",
			readme:  "# Model\npipeline_tag: image-text-to-text\n",
			wantTag: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "readme.MD"), []byte(test.readme), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := detectModelCardPipelineTag(dir); got != test.wantTag {
				t.Fatalf("detectModelCardPipelineTag() = %q, want %q", got, test.wantTag)
			}
		})
	}
}

func TestDetectModelCardPipelineTagRejectsOversizedReadme(t *testing.T) {
	dir := t.TempDir()
	readme := "---\npipeline_tag: image-text-to-text\n---\n" + strings.Repeat("x", maxModelCardMetadataBytes)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectModelCardPipelineTag(dir); got != "" {
		t.Fatalf("detectModelCardPipelineTag() = %q, want empty", got)
	}
}

func TestDetectPipelineTagModelCardPrecedence(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("---\npipeline_tag: feature-extraction\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"architectures":["Qwen3_5ForConditionalGeneration"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectPipelineTag(dir); got != "feature-extraction" {
		t.Fatalf("DetectPipelineTag() = %q, want feature-extraction", got)
	}
}

func TestDetectPipelineTagWeakTextModelCardCanUpgradeToVision(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("---\npipeline_tag: text-generation\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mmproj-model-f16.gguf"), []byte("mmproj"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectPipelineTag(dir); got != "image-text-to-text" {
		t.Fatalf("DetectPipelineTag() = %q, want image-text-to-text", got)
	}
}
