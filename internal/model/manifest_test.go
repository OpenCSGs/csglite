package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoadManifest(t *testing.T) {
	dir := t.TempDir()

	original := &LocalModel{
		Namespace:    "OpenCSG",
		Name:         "test-model",
		Format:       FormatGGUF,
		Size:         1024 * 1024 * 100,
		Files:        []string{"model.gguf", "config.json"},
		DownloadedAt: time.Now().Truncate(time.Second),
		Description:  "A test model",
		License:      "MIT",
	}

	if err := SaveManifest(dir, original); err != nil {
		t.Fatalf("SaveManifest error: %v", err)
	}

	// Verify file exists
	mpath := ManifestPath(dir, "OpenCSG", "test-model")
	if _, err := os.Stat(mpath); os.IsNotExist(err) {
		t.Fatal("manifest file was not created")
	}

	loaded, err := LoadManifest(dir, "OpenCSG", "test-model")
	if err != nil {
		t.Fatalf("LoadManifest error: %v", err)
	}

	if loaded.Namespace != original.Namespace {
		t.Errorf("Namespace = %q, want %q", loaded.Namespace, original.Namespace)
	}
	if loaded.Name != original.Name {
		t.Errorf("Name = %q, want %q", loaded.Name, original.Name)
	}
	if loaded.Format != original.Format {
		t.Errorf("Format = %q, want %q", loaded.Format, original.Format)
	}
	if loaded.Size != original.Size {
		t.Errorf("Size = %d, want %d", loaded.Size, original.Size)
	}
	if len(loaded.Files) != len(original.Files) {
		t.Errorf("Files len = %d, want %d", len(loaded.Files), len(original.Files))
	}
	if loaded.Description != original.Description {
		t.Errorf("Description = %q, want %q", loaded.Description, original.Description)
	}
	if loaded.License != original.License {
		t.Errorf("License = %q, want %q", loaded.License, original.License)
	}
}

func TestLoadManifest_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadManifest(dir, "nonexistent", "model")
	if err == nil {
		t.Error("expected error for non-existent manifest")
	}
}

func TestLoadManifest_NormalizesFileEntries(t *testing.T) {
	dir := t.TempDir()
	mpath := ManifestPath(dir, "OpenCSG", "normalized")
	if err := os.MkdirAll(filepath.Dir(mpath), 0o755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}

	raw := map[string]any{
		"namespace": "OpenCSG",
		"name":      "normalized",
		"format":    "gguf",
		"size":      42,
		"files":     []string{},
		"file_entries": []map[string]any{
			{"path": "./weights/model.gguf", "size": 42},
			{"path": "weights/model.gguf", "size": 42},
		},
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(mpath, data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	loaded, err := LoadManifest(dir, "OpenCSG", "normalized")
	if err != nil {
		t.Fatalf("LoadManifest error: %v", err)
	}
	if len(loaded.FileEntries) != 1 {
		t.Fatalf("file_entries len = %d, want 1", len(loaded.FileEntries))
	}
	if loaded.FileEntries[0].Path != "weights/model.gguf" {
		t.Fatalf("entry path = %q, want weights/model.gguf", loaded.FileEntries[0].Path)
	}
	if len(loaded.Files) != 1 || loaded.Files[0] != "weights/model.gguf" {
		t.Fatalf("files = %#v, want normalized file path", loaded.Files)
	}
}

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  Format
	}{
		{
			name:  "GGUF files",
			files: []string{"model-q4.gguf", "config.json"},
			want:  FormatGGUF,
		},
		{
			name:  "SafeTensors files",
			files: []string{"model.safetensors", "config.json"},
			want:  FormatSafeTensors,
		},
		{
			name:  "GGUF preferred over SafeTensors",
			files: []string{"model.safetensors", "model.gguf"},
			want:  FormatGGUF,
		},
		{
			name:  "PyTorch bin files",
			files: []string{"pytorch_model.bin", "config.json"},
			want:  FormatPyTorch,
		},
		{
			name:  "unknown format",
			files: []string{"config.json", "tokenizer.json"},
			want:  FormatUnknown,
		},
		{
			name:  "case insensitive",
			files: []string{"Model.GGUF"},
			want:  FormatGGUF,
		},
		{
			name:  "empty",
			files: nil,
			want:  FormatUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectFormat(tt.files)
			if got != tt.want {
				t.Errorf("DetectFormat(%v) = %q, want %q", tt.files, got, tt.want)
			}
		})
	}
}

func TestDetectPipelineTagSentenceTransformersEmbedding(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "modules.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"architectures":["XLMRobertaModel"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := DetectPipelineTag(dir); got != "feature-extraction" {
		t.Fatalf("DetectPipelineTag() = %q, want feature-extraction", got)
	}
}

func TestDetectPipelineTagRegisteredEmbeddingArchitecture(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"architectures":["ModernBertModel"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := DetectPipelineTag(dir); got != "feature-extraction" {
		t.Fatalf("DetectPipelineTag() = %q, want feature-extraction", got)
	}
}

func TestDetectPipelineTagRegisteredVisionArchitecture(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"architectures":["Idefics3ForConditionalGeneration"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := DetectPipelineTag(dir); got != "image-text-to-text" {
		t.Fatalf("DetectPipelineTag() = %q, want image-text-to-text", got)
	}
}

func TestDetectPipelineTagASRSupportedArchitectures(t *testing.T) {
	for _, arch := range []string{
		"Qwen3ASRForConditionalGeneration",
		"GlmAsrForConditionalGeneration",
		"WhisperForConditionalGeneration",
	} {
		t.Run(arch, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"supported_archs":["`+arch+`"]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := DetectPipelineTag(dir); got != "automatic-speech-recognition" {
				t.Fatalf("DetectPipelineTag() = %q, want automatic-speech-recognition", got)
			}
		})
	}
}

func TestDetectPipelineTagASRSupportedArchitectureSubstring(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"supported_archs":["modeling_qwen3_asr.Qwen3ASRForConditionalGeneration"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectPipelineTag(dir); got != "automatic-speech-recognition" {
		t.Fatalf("DetectPipelineTag() = %q, want automatic-speech-recognition", got)
	}
}

func TestDetectPipelineTagASRSupportedModels(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"supported_models":["AIWizards/Fun-ASR-Nano-2512"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectPipelineTag(dir); got != "automatic-speech-recognition" {
		t.Fatalf("DetectPipelineTag() = %q, want automatic-speech-recognition", got)
	}
}

func TestIsASRModelFamily(t *testing.T) {
	for _, name := range []string{
		"iic/SenseVoiceSmall",
		"FunAudioLLM/Fun-ASR-Nano-2512",
		"THUDM/GLM-ASR-Nano-2512",
		"openai/Whisper-large-v3",
		"openai/Whisper-large-v3-turbo",
		"Qwen/Qwen3-ASR-0.6B",
		"Qwen/Qwen3-ASR-1.7B",
		"damo/Paraformer-zh",
		"damo/Paraformer-zh-streaming",
	} {
		t.Run(name, func(t *testing.T) {
			if !IsASRModelFamily(name) {
				t.Fatalf("IsASRModelFamily(%q) = false, want true", name)
			}
		})
	}
}

func TestDetectPipelineTagDiffusersModelIndexDefaultsToTextToImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model_index.json"), []byte(`{"_class_name":"QwenImagePipeline"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"architectures":["Qwen2_5_VLForConditionalGeneration"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := DetectPipelineTag(dir); got != "text-to-image" {
		t.Fatalf("DetectPipelineTag() = %q, want text-to-image", got)
	}
}

func TestDetectPipelineTagDiffusersFamilies(t *testing.T) {
	for _, className := range []string{
		"FluxPipeline",
		"PixArtAlphaPipeline",
		"AuraFlowPipeline",
		"SanaPipeline",
		"StableCascadeCombinedPipeline",
		"ZImagePipeline",
	} {
		t.Run(className, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "model_index.json"), []byte(`{"_class_name":"`+className+`"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := DetectPipelineTag(dir); got != "text-to-image" {
				t.Fatalf("DetectPipelineTag() = %q, want text-to-image", got)
			}
		})
	}
}

func TestDetectPipelineTagStableVideoDiffusionImageToVideo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model_index.json"), []byte(`{"_class_name":"StableVideoDiffusionPipeline"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := DetectPipelineTag(dir); got != "image-to-video" {
		t.Fatalf("DetectPipelineTag() = %q, want image-to-video", got)
	}
}

func TestDetectPipelineTagUnknownDiffusersModelIndexDefaultsToTextToImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model_index.json"), []byte(`{"_class_name":"NewFancyPipeline"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := DetectPipelineTag(dir); got != "text-to-image" {
		t.Fatalf("DetectPipelineTag() = %q, want text-to-image", got)
	}
}

func TestFindModelFile(t *testing.T) {
	dir := t.TempDir()

	// Create a GGUF file
	if err := os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, format, err := FindModelFile(dir)
	if err != nil {
		t.Fatalf("FindModelFile error: %v", err)
	}
	if format != FormatGGUF {
		t.Errorf("format = %q, want %q", format, FormatGGUF)
	}
	if filepath.Base(path) != "model.gguf" {
		t.Errorf("path = %q, want model.gguf", path)
	}
}

func TestFindModelFile_SafeTensors(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("st"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, format, err := FindModelFile(dir)
	if err != nil {
		t.Fatalf("FindModelFile error: %v", err)
	}
	if format != FormatSafeTensors {
		t.Errorf("format = %q, want %q", format, FormatSafeTensors)
	}
	if filepath.Base(path) != "model.safetensors" {
		t.Errorf("path = %q, want model.safetensors", path)
	}
}

func TestFindModelFile_PyTorchBin(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "pytorch_model.bin"), []byte("pt"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, format, err := FindModelFile(dir)
	if err != nil {
		t.Fatalf("FindModelFile error: %v", err)
	}
	if format != FormatPyTorch {
		t.Errorf("format = %q, want %q", format, FormatPyTorch)
	}
	if filepath.Base(path) != "pytorch_model.bin" {
		t.Errorf("path = %q, want pytorch_model.bin", path)
	}
}

func TestFindModelFile_NotFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := FindModelFile(dir)
	if err == nil {
		t.Error("expected error when no model file found")
	}
}

func TestFindModelFile_PicksHighestPrecisionGGUF(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "low-Q4_0.gguf"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "high-Q8_0.gguf"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, format, err := FindModelFile(dir)
	if err != nil {
		t.Fatalf("FindModelFile: %v", err)
	}
	if format != FormatGGUF {
		t.Errorf("format = %q", format)
	}
	if filepath.Base(path) != "high-Q8_0.gguf" {
		t.Errorf("path = %q, want high-Q8_0.gguf", path)
	}
}

func TestFindModelFile_NestedQuantFolders(t *testing.T) {
	dir := t.TempDir()
	q4 := filepath.Join(dir, "Q4_0")
	q8 := filepath.Join(dir, "Q8_0")
	if err := os.MkdirAll(q4, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(q8, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(q4, "model.gguf"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(q8, "model.gguf"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, format, err := FindModelFile(dir)
	if err != nil {
		t.Fatalf("FindModelFile: %v", err)
	}
	if format != FormatGGUF {
		t.Errorf("format = %q", format)
	}
	want := filepath.Join(q8, "model.gguf")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}
