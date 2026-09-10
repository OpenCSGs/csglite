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
		Origin:       LocalModelOriginMarketplace,
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
	if loaded.Origin != original.Origin {
		t.Errorf("Origin = %q, want %q", loaded.Origin, original.Origin)
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

func TestPythonEmbeddingArchitectures(t *testing.T) {
	for _, arch := range []string{
		"JinaEmbeddingsV5Model",
		"JinaEmbeddingsV5OmniModel",
		"LlavaEuroBertAudioForEmbedding",
	} {
		t.Run(arch, func(t *testing.T) {
			if !IsPythonEmbeddingArchitecture(arch) {
				t.Fatalf("IsPythonEmbeddingArchitecture(%q) = false, want true", arch)
			}
		})
	}
	if IsPythonEmbeddingArchitecture("BertModel") {
		t.Fatal("BertModel should keep using the llama.cpp embedding path")
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

func TestDetectPipelineTagNewVisionArchitectures(t *testing.T) {
	for _, arch := range []string{
		"Qwen4ExpForConditionalGeneration",
		"Dots3NoteForConditionalGeneration",
	} {
		dir := t.TempDir()
		cfg := []byte(`{"architectures":["` + arch + `"]}`)
		if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := DetectPipelineTag(dir); got != "image-text-to-text" {
			t.Fatalf("DetectPipelineTag(%q) = %q, want image-text-to-text", arch, got)
		}
	}
}

func TestDetectPipelineTagMMProjWithoutConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mmproj-model-f16.gguf"), []byte("mmproj"), 0o644); err != nil {
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

func TestDetectPipelineTagDiffusersImageToImageFamilies(t *testing.T) {
	for _, className := range []string{
		"QwenImageEditPlusPipeline",
		"StableDiffusionXLImg2ImgPipeline",
		"FluxKontextPipeline",
	} {
		t.Run(className, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "model_index.json"), []byte(`{"_class_name":"`+className+`"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := DetectPipelineTag(dir); got != "image-to-image" {
				t.Fatalf("DetectPipelineTag() = %q, want image-to-image", got)
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

// A repo's MTP module is a separate architecture that llama-server cannot load,
// so it must never win over the real main weights even at a higher quant.
func TestFindModelFile_SkipsMTPModule(t *testing.T) {
	dir := t.TempDir()
	mtpDir := filepath.Join(dir, "MTP")
	if err := os.MkdirAll(mtpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mtpDir, "mtp-gemma-4-31B-it-Q8_0.gguf"), []byte("mtp"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "gemma-4-31B-it-qat-Q4_0.gguf")
	if err := os.WriteFile(main, []byte("main"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, format, err := FindModelFile(dir)
	if err != nil {
		t.Fatalf("FindModelFile: %v", err)
	}
	if format != FormatGGUF {
		t.Errorf("format = %q, want %q", format, FormatGGUF)
	}
	if path != main {
		t.Errorf("path = %q, want %q", path, main)
	}
}

func TestFindModelFile_SkipsMetadataDetectedProjector(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "qwen3.5-Q4_0.gguf")
	if err := os.WriteFile(main, []byte("main"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectorDir := filepath.Join(dir, "vision")
	if err := os.MkdirAll(projectorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projector := filepath.Join(projectorDir, "encoder-Q8_0.gguf")
	writeTestGGUF(t, projector, []testGGUFMetadata{
		{key: "general.architecture", valueType: ggufMetadataString, value: "clip"},
		{key: "clip.projector_type", valueType: ggufMetadataString, value: "qwen3vl_merger"},
	})

	path, format, err := FindModelFile(dir)
	if err != nil {
		t.Fatalf("FindModelFile: %v", err)
	}
	if format != FormatGGUF || path != main {
		t.Fatalf("FindModelFile() = %q, %q; want %q, %q", path, format, main, FormatGGUF)
	}
	if got := FindMMProj(dir); got != projector {
		t.Fatalf("FindMMProj() = %q, want %q", got, projector)
	}
	if got := DetectPipelineTag(dir); got != "image-text-to-text" {
		t.Fatalf("DetectPipelineTag() = %q, want image-text-to-text", got)
	}
}

func TestFindMTPFiles(t *testing.T) {
	modelDir := t.TempDir()
	mtpPath := filepath.Join(modelDir, "draft", "mtp-model-q8_0.gguf")
	if err := os.MkdirAll(filepath.Dir(mtpPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mtpPath, []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model-q4_k_m.gguf"), []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := FindMTPFiles(modelDir)
	if err != nil {
		t.Fatalf("FindMTPFiles() error = %v", err)
	}
	if len(got) != 1 || got[0] != mtpPath {
		t.Fatalf("FindMTPFiles() = %v, want [%s]", got, mtpPath)
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

func TestDetectPipelineTagTextToSpeechArchitecture(t *testing.T) {
	for _, arch := range []string{
		"VitsModel",
		"BarkModel",
		"SpeechT5ForTextToSpeech",
		"ParlerTTSForConditionalGeneration",
		"CsmForConditionalGeneration",
	} {
		t.Run(arch, func(t *testing.T) {
			dir := t.TempDir()
			cfg := []byte(`{"architectures":["` + arch + `"]}`)
			if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0o644); err != nil {
				t.Fatal(err)
			}
			if got := DetectPipelineTag(dir); got != "text-to-speech" {
				t.Fatalf("DetectPipelineTag() = %q, want text-to-speech", got)
			}
		})
	}
}

func TestDetectPipelineTagTextToSpeechModelType(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"model_type":"cosyvoice2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectPipelineTag(dir); got != "text-to-speech" {
		t.Fatalf("DetectPipelineTag() = %q, want text-to-speech", got)
	}
}

// A text-to-speech model whose language-model half is a plain causal LM is the
// case that used to be misrouted: the architecture alone matches the llama.cpp
// convert path, so the family name in the model directory has to win.
func TestDetectPipelineTagTextToSpeechFamilyBeatsCausalLMArchitecture(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "CosyVoice2-0.5B")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"architectures":["Qwen3ForCausalLM"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectPipelineTag(dir); got != "text-to-speech" {
		t.Fatalf("DetectPipelineTag() = %q, want text-to-speech", got)
	}
}

func TestDetectPipelineTagModelScopeTextToSpeechTask(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "configuration.json"), []byte(`{"task":"text-to-speech"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectPipelineTag(dir); got != "text-to-speech" {
		t.Fatalf("DetectPipelineTag() = %q, want text-to-speech", got)
	}
}

func TestIsTTSModelFamilyDoesNotClaimASRModels(t *testing.T) {
	for _, name := range []string{
		"SenseVoiceSmall",
		"Whisper-large-v3",
		"Qwen3-ASR-0.6B",
		"Qwen3-4B-Instruct",
	} {
		if IsTTSModelFamily(name) {
			t.Errorf("IsTTSModelFamily(%q) = true, want false", name)
		}
	}
	for _, name := range []string{
		"FunAudioLLM/CosyVoice2-0.5B",
		"SparkAudio/Spark-TTS-0.5B",
		"hexgrad/Kokoro-82M",
		"hexgrad/Kokoro-v1.0",
		"IndexTeam/IndexTTS-1.5",
	} {
		if !IsTTSModelFamily(name) {
			t.Errorf("IsTTSModelFamily(%q) = false, want true", name)
		}
	}
}

// Vikhrmodels/Qwen3-0.6B-TTS is the case that defeats every config-based check:
// architectures ["Qwen3ForCausalLM"], model_type "qwen3", no pipeline tag in the
// model card, and a ModelScope task of "others". Only the tokenizer's matched
// pair of audio span tokens and the "TTS" in its name give it away. Because
// Qwen3ForCausalLM *is* convertible, missing it means the GGUF conversion
// succeeds and the model is served as a text model producing garbage.
func TestDetectPipelineTagCodecTokenTTSModelWithCausalLMConfig(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Qwen3-0.6B")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config.json", `{"architectures":["Qwen3ForCausalLM"],"model_type":"qwen3","vocab_size":160887}`)
	write("configuration.json", `{"framework":"pytorch","task":"others","allow_remote":true}`)
	write("added_tokens.json", `{"<|start_of_audio|>":151669,"<|end_of_audio|>":151670,"<|im_end|>":151645}`)

	// The directory name here carries no "TTS" marker, so the tokenizer is the
	// only remaining signal.
	if !HasAudioOutputTokens(dir) {
		t.Fatal("HasAudioOutputTokens() = false, want true")
	}
	if got := DetectPipelineTag(dir); got != "text-to-speech" {
		t.Fatalf("DetectPipelineTag() = %q, want text-to-speech", got)
	}
}

// A plain text model must keep its text-generation tag: an audio-input model may
// declare a leading audio marker without being a speech generator, so both ends
// of the span are required.
func TestHasAudioOutputTokensRequiresBothSpanEnds(t *testing.T) {
	cases := map[string]struct {
		addedTokens string
		want        bool
	}{
		"both ends":      {`{"<|start_of_audio|>":1,"<|end_of_audio|>":2}`, true},
		"start only":     {`{"<|start_of_audio|>":1}`, false},
		"end only":       {`{"<|end_of_audio|>":2}`, false},
		"speech variant": {`{"<|start_of_speech|>":1,"<|end_of_speech|>":2}`, true},
		"plain text":     {`{"<|im_end|>":151645,"<|endoftext|>":151643}`, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "added_tokens.json"), []byte(tc.addedTokens), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := HasAudioOutputTokens(dir); got != tc.want {
				t.Fatalf("HasAudioOutputTokens() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsTTSModelName(t *testing.T) {
	for _, name := range []string{
		"modelscope/Vikhrmodels/Qwen3-0.6B-TTS",
		"Qwen3-0.6B-TTS",
		"some/tts-model",
		"a/b_tts_v2",
	} {
		if !IsTTSModelName(name) {
			t.Errorf("IsTTSModelName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{
		"",
		"Qwen/Qwen3-0.6B",
		"modelscope/Qwen/Qwen3-ASR-0.6B",
		"iic/SenseVoiceSmall",
		"some/nottsmodel",
	} {
		if IsTTSModelName(name) {
			t.Errorf("IsTTSModelName(%q) = true, want false", name)
		}
	}
}
