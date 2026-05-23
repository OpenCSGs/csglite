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
	if got := diffusersPipelineTagFromClassName("LlamaForCausalLM"); got != "" {
		t.Fatalf("tag = %q, want empty", got)
	}
}
