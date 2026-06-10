package localinference

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencsgs/csghub-lite/internal/model"
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
