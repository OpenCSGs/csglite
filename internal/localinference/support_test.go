package localinference

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencsgs/csglite/internal/model"
)

func TestFromMarketplaceGGUF(t *testing.T) {
	support := FromMarketplace("gguf", "Qwen2ForCausalLM", "")
	if !support.Supported || support.Runtime != "llama" || support.Mode != "direct" || support.RuntimeArchitecture != "qwen2" {
		t.Fatalf("support = %#v, want llama direct qwen2", support)
	}
}

func TestFromMarketplaceUnknownSafeTensors(t *testing.T) {
	support := FromMarketplace("safetensors", "UnknownArch", "")
	if support.Supported {
		t.Fatalf("support = %#v, want unsupported", support)
	}
}

func TestFromMarketplaceConvertibleSafeTensors(t *testing.T) {
	support := FromMarketplace("safetensors", "Qwen2ForCausalLM", "")
	if !support.Supported || support.Runtime != "llama" || support.Mode != "convert" || support.RuntimeArchitecture != "qwen2" {
		t.Fatalf("support = %#v, want llama convert qwen2", support)
	}
}

func TestFromMarketplaceConvertibleSafeTensorsUsesLlamaCppRegistry(t *testing.T) {
	cases := map[string]string{
		"ModernBertModel":                  "modernbert",
		"Idefics3ForConditionalGeneration": "mmp",
		"Gemma4ForConditionalGeneration":   "gemma4",
	}
	for arch, wantRuntimeArch := range cases {
		t.Run(arch, func(t *testing.T) {
			support := FromMarketplace("safetensors", arch, "")
			if !support.Supported || support.Runtime != "llama" || support.Mode != "convert" || support.RuntimeArchitecture != wantRuntimeArch {
				t.Fatalf("support = %#v, want llama convert %s", support, wantRuntimeArch)
			}
		})
	}
}

func TestFromMarketplaceDiffusersClassName(t *testing.T) {
	support := FromMarketplace("", "", "StableDiffusionXLPipeline")
	if !support.Supported || support.Runtime != "diffusers" || support.Mode != "image" {
		t.Fatalf("support = %#v, want diffusers image", support)
	}
}

func TestFromMarketplaceDiffusersClassNameFamilies(t *testing.T) {
	for _, className := range []string{
		"FluxPipeline",
		"PixArtSigmaPipeline",
		"AuraFlowPipeline",
		"QwenImagePipeline",
		"SanaPipeline",
		"CogView4Pipeline",
		"ZImagePipeline",
	} {
		t.Run(className, func(t *testing.T) {
			support := FromMarketplace("", "", className)
			if !support.Supported || support.Runtime != "diffusers" || support.Mode != "image" {
				t.Fatalf("support = %#v, want diffusers image", support)
			}
		})
	}
}

func TestFromMarketplaceModelASRFamily(t *testing.T) {
	support := FromMarketplaceModel("pytorch", "", "", "AIWizards/Fun-ASR-Nano-2512", "")
	if !support.Supported || support.Runtime != "python-asr" || support.Mode != "asr" {
		t.Fatalf("support = %#v, want python-asr asr", support)
	}
}

func TestFromMarketplaceModelASRTaskTag(t *testing.T) {
	support := FromMarketplaceModel("", "", "", "AIWizards/unknown", "automatic-speech-recognition")
	if !support.Supported || support.Runtime != "python-asr" || support.Mode != "asr" {
		t.Fatalf("support = %#v, want python-asr asr", support)
	}
}

func TestFromMarketplaceModelImageToVideoTaskUnsupported(t *testing.T) {
	support := FromMarketplaceModel("safetensors", "", "StableVideoDiffusionPipeline", "AIWizards/sv3d-diffusers", "image-to-video")
	if support.Supported || support.Mode != "none" {
		t.Fatalf("support = %#v, want unsupported", support)
	}
}

func TestFromMarketplaceModelTaskGatesConflictingClassName(t *testing.T) {
	support := FromMarketplaceModel("safetensors", "", "StableDiffusionXLPipeline", "AIWizards/sv3d-diffusers", "image-to-video")
	if support.Supported || support.Mode != "none" {
		t.Fatalf("support = %#v, want unsupported", support)
	}
}

func TestFromMarketplaceModelTextToImageRejectsNonImageClassName(t *testing.T) {
	support := FromMarketplaceModel("safetensors", "LlamaForCausalLM", "LlamaForCausalLM", "owner/not-image", "text-to-image")
	if support.Supported || support.Mode != "none" {
		t.Fatalf("support = %#v, want unsupported", support)
	}
}

func TestFromMarketplaceModelTextToImageAllowsMissingClassName(t *testing.T) {
	support := FromMarketplaceModel("safetensors", "", "", "owner/image-model", "text-to-image")
	if !support.Supported || support.Runtime != "diffusers" || support.Mode != "image" {
		t.Fatalf("support = %#v, want diffusers image", support)
	}
}

func TestFromMarketplaceStableVideoDiffusionUnsupported(t *testing.T) {
	support := FromMarketplace("safetensors", "", "StableVideoDiffusionPipeline")
	if support.Supported || support.Mode != "none" {
		t.Fatalf("support = %#v, want unsupported", support)
	}
}

func TestFromLocalModelDiffusers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model_index.json"), []byte(`{"_class_name":"QwenImagePipeline"}`), 0o644); err != nil {
		t.Fatalf("write model_index.json: %v", err)
	}

	support := FromLocalModel(&model.LocalModel{
		Format: model.FormatSafeTensors,
	}, dir)
	if !support.Supported || support.Runtime != "diffusers" || support.Mode != "image" {
		t.Fatalf("support = %#v, want diffusers image", support)
	}
}

func TestFromLocalModelEmbeddingArchitecture(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"architectures":["XLMRobertaModel"]}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	support := FromLocalModel(&model.LocalModel{
		Format: model.FormatSafeTensors,
	}, dir)
	if !support.Supported || support.Runtime != "llama" || support.Mode != "convert" || support.RuntimeArchitecture != "bert" {
		t.Fatalf("support = %#v, want llama convert bert", support)
	}
}

func TestFromLocalModelJinaOmniUsesPythonEmbedding(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"architectures":["JinaEmbeddingsV5OmniModel"]}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	support := FromLocalModel(&model.LocalModel{
		Format:      model.FormatSafeTensors,
		PipelineTag: "feature-extraction",
	}, dir)
	if !support.Supported || support.Runtime != "python-embedding" || support.Mode != "embedding" || support.Architecture != "JinaEmbeddingsV5OmniModel" {
		t.Fatalf("support = %#v, want python-embedding embedding", support)
	}
}

func TestFromLocalModelUnknownEmbeddingDoesNotUsePythonEmbedding(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"architectures":["SomeUnsupportedEmbeddingModel"]}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	support := FromLocalModel(&model.LocalModel{
		Format:      model.FormatSafeTensors,
		PipelineTag: "feature-extraction",
	}, dir)
	if support.Supported || support.Runtime == "python-embedding" {
		t.Fatalf("support = %#v, want unsupported until Python worker has an explicit compatible path", support)
	}
}

func TestDiffusersPipelineTagFromClassName(t *testing.T) {
	if got := diffusersPipelineTagFromClassName("FluxPipeline"); got != "text-to-image" {
		t.Fatalf("tag = %q, want text-to-image", got)
	}
	if got := diffusersPipelineTagFromClassName("QwenImageEditPlusPipeline"); got != "image-to-image" {
		t.Fatalf("tag = %q, want image-to-image", got)
	}
	if got := diffusersPipelineTagFromClassName("StableVideoDiffusionPipeline"); got != "image-to-video" {
		t.Fatalf("tag = %q, want image-to-video", got)
	}
	if got := diffusersPipelineTagFromClassName("LlamaForCausalLM"); got != "" {
		t.Fatalf("tag = %q, want empty", got)
	}
}

// A safetensors text-to-speech model whose language-model half is a supported
// causal LM matches the llama.cpp "convert" path by architecture alone, which
// would turn it into GGUF and serve it as a text model with the vocoder
// dropped. It must route to the Python text-to-speech runtime instead.
func TestTextToSpeechRoutesToPythonRuntimeNotLlama(t *testing.T) {
	t.Run("marketplace pipeline tag", func(t *testing.T) {
		support := FromMarketplaceModel("safetensors", "Qwen3ForCausalLM", "", "FunAudioLLM/CosyVoice2-0.5B", "text-to-speech")
		if !support.Supported || support.Runtime != "python-tts" || support.Mode != "tts" {
			t.Fatalf("support = %#v, want python-tts tts", support)
		}
	})

	t.Run("marketplace model family without a pipeline tag", func(t *testing.T) {
		support := FromMarketplaceModel("safetensors", "Qwen3ForCausalLM", "", "FunAudioLLM/CosyVoice2-0.5B", "")
		if !support.Supported || support.Runtime != "python-tts" || support.Mode != "tts" {
			t.Fatalf("support = %#v, want python-tts tts", support)
		}
	})

	t.Run("marketplace architecture", func(t *testing.T) {
		support := FromMarketplace("safetensors", "SpeechT5ForTextToSpeech", "")
		if !support.Supported || support.Runtime != "python-tts" {
			t.Fatalf("support = %#v, want python-tts", support)
		}
	})

	t.Run("local model pipeline tag", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"architectures":["Qwen3ForCausalLM"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		lm := &model.LocalModel{Format: model.FormatSafeTensors, PipelineTag: "text-to-speech"}
		support := FromLocalModel(lm, dir)
		if !support.Supported || support.Runtime != "python-tts" || support.Mode != "tts" {
			t.Fatalf("support = %#v, want python-tts tts", support)
		}
	})

	t.Run("local model architecture without a pipeline tag", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"architectures":["VitsModel"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		lm := &model.LocalModel{Format: model.FormatSafeTensors}
		support := FromLocalModel(lm, dir)
		if !support.Supported || support.Runtime != "python-tts" {
			t.Fatalf("support = %#v, want python-tts", support)
		}
	})
}

// The model named in issue #147. Its architecture is convertible, so a missed
// detection means the GGUF conversion succeeds and the model is served as text.
func TestCodecTokenTTSModelUsesPythonRuntime(t *testing.T) {
	name := "modelscope/Vikhrmodels/Qwen3-0.6B-TTS"

	support := FromMarketplaceModel("safetensors", "Qwen3ForCausalLM", "", name, "")
	if support.Runtime == "llama" || support.Mode == "convert" {
		t.Fatalf("marketplace support = %#v, want it off the llama convert path", support)
	}
	if !support.Supported || support.Runtime != "python-tts" {
		t.Fatalf("marketplace support = %#v, want python-tts", support)
	}

	dir := t.TempDir()
	for file, body := range map[string]string{
		"config.json":        `{"architectures":["Qwen3ForCausalLM"],"model_type":"qwen3","vocab_size":160887}`,
		"configuration.json": `{"framework":"pytorch","task":"others"}`,
		"added_tokens.json":  `{"<|start_of_audio|>":151669,"<|end_of_audio|>":151670}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lm := &model.LocalModel{Namespace: "Vikhrmodels", Name: "Qwen3-0.6B-TTS", Format: model.FormatSafeTensors}
	if support := FromLocalModel(lm, dir); support.Runtime != "python-tts" || support.Mode != "tts" {
		t.Fatalf("local support = %#v, want python-tts tts", support)
	}

	// Regression guard: a plain Qwen3 text model must stay convertible.
	if support := FromMarketplaceModel("safetensors", "Qwen3ForCausalLM", "", "Qwen/Qwen3-0.6B", ""); !support.Supported || support.Mode != "convert" {
		t.Fatalf("plain text model support = %#v, want llama convert", support)
	}
}
