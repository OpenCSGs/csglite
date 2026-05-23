package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csghub-lite/internal/ggufpick"
)

// Vision-related HuggingFace architecture suffixes/names.
var visionArchitectures = map[string]bool{
	"AudioFlamingo3ForConditionalGeneration": true,
	"ChameleonForConditionalGeneration":      true,
	"CogVLMForCausalLM":                      true,
	"DeepseekOCRForCausalLM":                 true,
	"DotsOCRForCausalLM":                     true,
	"Gemma3ForConditionalGeneration":         true,
	"Gemma3nForConditionalGeneration":        true,
	"Glm4vForConditionalGeneration":          true,
	"Glm4vMoeForConditionalGeneration":       true,
	"GlmOcrForConditionalGeneration":         true,
	"HunYuanVLForConditionalGeneration":      true,
	"Idefics3ForConditionalGeneration":       true,
	"InternVLChatModel":                      true,
	"JanusForConditionalGeneration":          true,
	"KimiK25ForConditionalGeneration":        true,
	"KimiVLForConditionalGeneration":         true,
	"Lfm2VlForConditionalGeneration":         true,
	"LightOnOCRForConditionalGeneration":     true,
	"LlavaForConditionalGeneration":          true,
	"LlavaNextForConditionalGeneration":      true,
	"MERaLiON2ForConditionalGeneration":      true,
	"MiniCPMV":                               true,
	"MiniCPMV4_6ForConditionalGeneration":    true,
	"Mistral3ForConditionalGeneration":       true,
	"NemotronH_Nano_VL_V2":                   true,
	"PaddleOCRVLForConditionalGeneration":    true,
	"Phi3VForCausalLM":                       true,
	"Phi4ForCausalLMV":                       true,
	"Qwen2VLForConditionalGeneration":        true,
	"Qwen2VLModel":                           true,
	"Qwen2_5OmniModel":                       true,
	"Qwen2_5_VLForConditionalGeneration":     true,
	"Qwen3OmniMoeForConditionalGeneration":   true,
	"Qwen3VLForConditionalGeneration":        true,
	"Qwen3VLMoeForConditionalGeneration":     true,
	"Qwen3_5ForConditionalGeneration":        true,
	"Qwen3_5MoeForConditionalGeneration":     true,
	"Sarashina2VisionForCausalLM":            true,
	"SmolVLMForConditionalGeneration":        true,
	"StepVLForConditionalGeneration":         true,
	"VoxtralForConditionalGeneration":        true,
	"YoutuVLForConditionalGeneration":        true,
}

var embeddingArchitectures = map[string]bool{
	"BertForMaskedLM":                     true,
	"BertForSequenceClassification":       true,
	"BertModel":                           true,
	"CamembertModel":                      true,
	"DistilBertForMaskedLM":               true,
	"DistilBertForSequenceClassification": true,
	"DistilBertModel":                     true,
	"EuroBertModel":                       true,
	"JinaBertForMaskedLM":                 true,
	"JinaBertModel":                       true,
	"JinaEmbeddingsV5Model":               true,
	"ModernBertForMaskedLM":               true,
	"ModernBertForSequenceClassification": true,
	"ModernBertModel":                     true,
	"NeoBERT":                             true,
	"NeoBERTForSequenceClassification":    true,
	"NeoBERTLMHead":                       true,
	"NomicBertModel":                      true,
	"RobertaForSequenceClassification":    true,
	"RobertaModel":                        true,
	"T5EncoderModel":                      true,
	"UMT5Model":                           true,
	"XLMRobertaForSequenceClassification": true,
	"XLMRobertaModel":                     true,
}

// IsEmbeddingArchitecture reports whether the HuggingFace architecture is treated
// as a local embedding model by csghub-lite.
func IsEmbeddingArchitecture(architecture string) bool {
	return embeddingArchitectures[strings.TrimSpace(architecture)]
}

// IsVisionArchitecture reports whether the HuggingFace architecture is a supported
// multimodal vision-language model.
func IsVisionArchitecture(architecture string) bool {
	return visionArchitectures[strings.TrimSpace(architecture)]
}

// DetectPipelineTag reads config.json in modelDir and returns a local pipeline
// tag for routing. Sentence-transformers repositories are treated as embedding
// models even when the hub metadata was not persisted in older manifests.
func DetectPipelineTag(modelDir string) string {
	if tag := detectDiffusersPipelineTag(modelDir); tag != "" {
		return tag
	}
	if _, err := os.Stat(filepath.Join(modelDir, "modules.json")); err == nil {
		return "feature-extraction"
	}
	data, err := os.ReadFile(filepath.Join(modelDir, "config.json"))
	if err != nil {
		return "text-generation"
	}
	var cfg struct {
		Architectures []string `json:"architectures"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return "text-generation"
	}
	for _, arch := range cfg.Architectures {
		if visionArchitectures[arch] {
			return "image-text-to-text"
		}
		if embeddingArchitectures[arch] {
			return "feature-extraction"
		}
	}
	return "text-generation"
}

func detectDiffusersPipelineTag(modelDir string) string {
	data, err := os.ReadFile(filepath.Join(modelDir, "model_index.json"))
	if err != nil {
		return ""
	}
	var idx struct {
		ClassName string `json:"_class_name"`
	}
	if json.Unmarshal(data, &idx) != nil {
		return ""
	}
	className := strings.ToLower(strings.TrimSpace(idx.ClassName))
	switch {
	case isTextToImageDiffusersClass(className):
		return "text-to-image"
	case isImageToImageDiffusersClass(className):
		return "image-to-image"
	default:
		// A Diffusers model_index.json is a stronger signal than the legacy
		// text-model config checks below. Prefer trying the image runtime so
		// newly supported text-to-image pipelines do not fall back to llama.
		return "text-to-image"
	}
}

func isTextToImageDiffusersClass(className string) bool {
	return strings.Contains(className, "texttoimage") ||
		strings.Contains(className, "pipelinefortext2image") ||
		strings.Contains(className, "qwenimage") ||
		strings.Contains(className, "flux") ||
		strings.Contains(className, "stablediffusion") ||
		strings.Contains(className, "stablecascade") ||
		strings.Contains(className, "wuerstchen") ||
		strings.Contains(className, "kandinsky") ||
		strings.Contains(className, "pixart") ||
		strings.Contains(className, "auraflow") ||
		strings.Contains(className, "sana") ||
		strings.Contains(className, "lumina") ||
		strings.Contains(className, "kolors") ||
		strings.Contains(className, "cogview") ||
		strings.Contains(className, "hunyuan") ||
		strings.Contains(className, "dit") ||
		strings.Contains(className, "glmpipeline") ||
		strings.Contains(className, "glmimage") ||
		strings.Contains(className, "zimage") ||
		strings.Contains(className, "ovisimage") ||
		strings.Contains(className, "prxpipeline") ||
		strings.Contains(className, "latentconsistency") ||
		strings.Contains(className, "deepfloyd")
}

func isImageToImageDiffusersClass(className string) bool {
	return strings.Contains(className, "image2image") ||
		strings.Contains(className, "img2img") ||
		strings.Contains(className, "inpaint") ||
		strings.Contains(className, "depth2img") ||
		strings.Contains(className, "kontext") ||
		strings.Contains(className, "edit")
}

// FindMMProj looks for a multimodal projector GGUF file (mmproj) in the model directory.
func FindMMProj(modelDir string) string {
	entries, err := os.ReadDir(modelDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		lower := strings.ToLower(e.Name())
		if strings.Contains(lower, "mmproj") && strings.HasSuffix(lower, ".gguf") {
			return filepath.Join(modelDir, e.Name())
		}
	}
	return ""
}

// SaveManifest writes a model manifest to disk.
func SaveManifest(baseDir string, m *LocalModel) error {
	normalizeLocalModel(m)
	mpath := ManifestPath(baseDir, m.Namespace, m.Name)
	if err := os.MkdirAll(filepath.Dir(mpath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(mpath, data, 0o644)
}

// LoadManifest reads a model manifest from disk.
func LoadManifest(baseDir, namespace, name string) (*LocalModel, error) {
	mpath := ManifestPath(baseDir, namespace, name)
	data, err := os.ReadFile(mpath)
	if err != nil {
		return nil, err
	}
	var m LocalModel
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	normalizeLocalModel(&m)
	return &m, nil
}

// DetectFormat guesses the model format from the file names.
func DetectFormat(files []string) Format {
	for _, f := range files {
		lower := strings.ToLower(f)
		if strings.HasSuffix(lower, ".gguf") {
			return FormatGGUF
		}
	}
	for _, f := range files {
		lower := strings.ToLower(f)
		if strings.HasSuffix(lower, ".safetensors") {
			return FormatSafeTensors
		}
	}
	for _, f := range files {
		lower := strings.ToLower(f)
		if strings.HasSuffix(lower, ".bin") {
			return FormatPyTorch
		}
	}
	return FormatUnknown
}

// FindModelFile returns the primary model file (GGUF or SafeTensors).
func FindModelFile(modelDir string) (string, Format, error) {
	entries, err := os.ReadDir(modelDir)
	if err != nil {
		return "", FormatUnknown, err
	}

	// Prefer GGUF weight files (skip multimodal projector); recurse into subdirs; pick highest precision.
	ggufRel, err := ggufpick.CollectWeightGGUFRelPaths(modelDir)
	if err != nil {
		return "", FormatUnknown, err
	}
	if len(ggufRel) > 0 {
		best := ggufpick.BestWeightGGUFRelPath(ggufRel)
		return filepath.Join(modelDir, best), FormatGGUF, nil
	}
	// Then HuggingFace weights that the bundled llama.cpp converter can handle.
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".safetensors") {
			return filepath.Join(modelDir, e.Name()), FormatSafeTensors, nil
		}
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".bin") {
			return filepath.Join(modelDir, e.Name()), FormatPyTorch, nil
		}
	}
	return "", FormatUnknown, os.ErrNotExist
}
