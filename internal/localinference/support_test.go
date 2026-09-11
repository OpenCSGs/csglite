package localinference

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
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
// dropped. It must never take that path, whether or not a backend can
// synthesise it.
func TestTextToSpeechNeverTakesTheLlamaConvertPath(t *testing.T) {
	cases := map[string]api.LocalInferenceSupport{
		// CosyVoice has no backend yet, so it is off the llama path but not
		// advertised as runnable.
		"marketplace pipeline tag": FromMarketplaceModel(
			"safetensors", "Qwen3ForCausalLM", "", "FunAudioLLM/CosyVoice2-0.5B", "text-to-speech"),
		"marketplace model family without a pipeline tag": FromMarketplaceModel(
			"safetensors", "Qwen3ForCausalLM", "", "FunAudioLLM/CosyVoice2-0.5B", ""),
		"codec-token model named in #147": FromMarketplaceModel(
			"safetensors", "Qwen3ForCausalLM", "", "modelscope/Vikhrmodels/Qwen3-0.6B-TTS", ""),
	}
	for name, support := range cases {
		t.Run(name, func(t *testing.T) {
			if support.Runtime == "llama" || support.Mode == "convert" {
				t.Fatalf("support = %#v, want it off the llama convert path", support)
			}
			if support.Supported {
				t.Fatalf("support = %#v, want unsupported: no backend can synthesise it", support)
			}
		})
	}

	t.Run("a backend-backed architecture is advertised", func(t *testing.T) {
		support := FromMarketplace("safetensors", "SpeechT5ForTextToSpeech", "")
		if !support.Supported || support.Runtime != "python-tts" {
			t.Fatalf("support = %#v, want python-tts", support)
		}
	})

	t.Run("a plain text model stays convertible", func(t *testing.T) {
		support := FromMarketplaceModel("safetensors", "Qwen3ForCausalLM", "", "Qwen/Qwen3-0.6B", "")
		if !support.Supported || support.Mode != "convert" {
			t.Fatalf("support = %#v, want llama convert", support)
		}
	})
}

// Reporting support for a text-to-speech model the runtime cannot synthesise
// repeats the complaint in #147 in a new form: the library says yes and
// synthesis fails. Support must track what a backend can actually serve, while
// the model still stays off the llama.cpp convert path either way.
func TestTextToSpeechSupportTracksAvailableBackends(t *testing.T) {
	write := func(t *testing.T, dir string, files map[string]string) {
		t.Helper()
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("codec-token model with no decoder is unsupported", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "Qwen3-0.6B-TTS")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, dir, map[string]string{
			"config.json":       `{"architectures":["Qwen3ForCausalLM"],"model_type":"qwen3","vocab_size":160887}`,
			"added_tokens.json": `{"<|start_of_audio|>":151669,"<|end_of_audio|>":151670}`,
		})
		lm := &model.LocalModel{Namespace: "Vikhrmodels", Name: "Qwen3-0.6B-TTS", Format: model.FormatSafeTensors}
		support := FromLocalModel(lm, dir)
		if support.Supported {
			t.Fatalf("support = %#v, want unsupported: no backend can synthesise it", support)
		}
		// Still must not fall back to the text-generation runtime.
		if support.Runtime == "llama" || support.Mode == "convert" {
			t.Fatalf("support = %#v, want it off the llama convert path", support)
		}
	})

	t.Run("kokoro is supported", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "Kokoro-82M")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// StyleTTS2 config: no architectures field at all.
		write(t, dir, map[string]string{"config.json": `{"istftnet":{},"plbert":{},"dim_in":64}`})
		lm := &model.LocalModel{Namespace: "hexgrad", Name: "Kokoro-82M", Format: model.FormatPyTorch}
		if support := FromLocalModel(lm, dir); !support.Supported || support.Runtime != "python-tts" {
			t.Fatalf("support = %#v, want python-tts", support)
		}
	})

	t.Run("official qwen3-tts is supported", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "Qwen3-TTS-12Hz-0.6B-CustomVoice")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, dir, map[string]string{
			"config.json": `{"architectures":["Qwen3TTSForConditionalGeneration"],"model_type":"qwen3_tts"}`,
		})
		lm := &model.LocalModel{Namespace: "Qwen", Name: "Qwen3-TTS-12Hz-0.6B-CustomVoice", Format: model.FormatSafeTensors}
		if support := FromLocalModel(lm, dir); !support.Supported || support.Runtime != "python-tts" {
			t.Fatalf("support = %#v, want python-tts", support)
		}
	})

	t.Run("transformers-native architecture is supported", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, map[string]string{"config.json": `{"architectures":["VitsModel"],"model_type":"vits"}`})
		lm := &model.LocalModel{Namespace: "facebook", Name: "mms-tts-eng", Format: model.FormatSafeTensors}
		if support := FromLocalModel(lm, dir); !support.Supported || support.Runtime != "python-tts" {
			t.Fatalf("support = %#v, want python-tts", support)
		}
	})
}
