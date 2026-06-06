package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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

var asrArchitectures = []string{
	"GlmAsrForConditionalGeneration",
	"Qwen3ASRForConditionalGeneration",
	"WhisperForConditionalGeneration",
	"Wav2Vec2ForCTC",
	"HubertForCTC",
	"SEWForCTC",
	"SEWDForCTC",
	"Data2VecAudioForCTC",
	"UniSpeechForCTC",
	"UniSpeechSatForCTC",
	"WavLMForCTC",
	"Speech2TextForConditionalGeneration",
	"SpeechEncoderDecoderModel",
}

var asrModelFamilies = []string{
	"SenseVoiceSmall",
	"Fun-ASR-Nano-2512",
	"GLM-ASR-Nano-2512",
	"Whisper-large-v3",
	"Whisper-large-v3-turbo",
	"Qwen3-ASR-0.6B",
	"Qwen3-ASR-1.7B",
	"Paraformer-zh",
	"Paraformer-zh-streaming",
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

// IsASRArchitecture reports whether the HuggingFace architecture is an
// automatic speech recognition model.
func IsASRArchitecture(architecture string) bool {
	architecture = strings.TrimSpace(architecture)
	if architecture == "" {
		return false
	}
	for _, supported := range asrArchitectures {
		if strings.Contains(architecture, supported) {
			return true
		}
	}
	return false
}

// IsASRModelFamily reports whether a model id/name belongs to a known ASR
// family served by the Python ASR runtime.
func IsASRModelFamily(name string) bool {
	normalized := normalizeASRModelFamily(name)
	if normalized == "" {
		return false
	}
	for _, supported := range asrModelFamilies {
		if strings.Contains(normalized, normalizeASRModelFamily(supported)) {
			return true
		}
	}
	return false
}

func normalizeASRModelFamily(value string) string {
	return strings.NewReplacer("-", "", "_", "", " ", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(value)))
}

// DetectPipelineTag reads config.json in modelDir and returns a local pipeline
// tag for routing. Sentence-transformers repositories are treated as embedding
// models even when the hub metadata was not persisted in older manifests.
func DetectPipelineTag(modelDir string) string {
	if IsASRModelFamily(modelDir) {
		return "automatic-speech-recognition"
	}
	if tag := detectDiffusersPipelineTag(modelDir); tag != "" {
		return tag
	}
	if _, err := os.Stat(filepath.Join(modelDir, "modules.json")); err == nil {
		return "feature-extraction"
	}
	if tag := detectModelScopePipelineTag(modelDir); tag != "" {
		return tag
	}
	data, err := os.ReadFile(filepath.Join(modelDir, "config.json"))
	if err != nil {
		return "text-generation"
	}
	var cfg struct {
		Architectures   []string `json:"architectures"`
		SupportedArchs  []string `json:"supported_archs"`
		SupportedModels []string `json:"supported_models"`
		ModelType       string   `json:"model_type"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return "text-generation"
	}
	if isASRModelType(cfg.ModelType) {
		return "automatic-speech-recognition"
	}
	for _, name := range cfg.SupportedModels {
		if IsASRModelFamily(name) {
			return "automatic-speech-recognition"
		}
	}
	for _, arch := range append(cfg.Architectures, cfg.SupportedArchs...) {
		if visionArchitectures[arch] {
			return "image-text-to-text"
		}
		if embeddingArchitectures[arch] {
			return "feature-extraction"
		}
		if IsASRArchitecture(arch) {
			return "automatic-speech-recognition"
		}
	}
	return "text-generation"
}

func detectModelScopePipelineTag(modelDir string) string {
	data, err := os.ReadFile(filepath.Join(modelDir, "configuration.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Task            string   `json:"task"`
		SupportedArchs  []string `json:"supported_archs"`
		SupportedModels []string `json:"supported_models"`
		Model           struct {
			Type string `json:"type"`
		} `json:"model"`
		Pipeline struct {
			Type string `json:"type"`
		} `json:"pipeline"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	task := strings.ToLower(strings.TrimSpace(cfg.Task))
	modelType := strings.ToLower(strings.TrimSpace(cfg.Model.Type))
	pipelineType := strings.ToLower(strings.TrimSpace(cfg.Pipeline.Type))
	for _, name := range cfg.SupportedModels {
		if IsASRModelFamily(name) {
			return "automatic-speech-recognition"
		}
	}
	for _, arch := range cfg.SupportedArchs {
		if IsASRArchitecture(arch) {
			return "automatic-speech-recognition"
		}
	}
	if task == "automatic-speech-recognition" ||
		task == "auto-speech-recognition" ||
		strings.Contains(task, "speech-recognition") ||
		modelType == "funasr" ||
		strings.Contains(pipelineType, "funasr") {
		return "automatic-speech-recognition"
	}
	return ""
}

func isASRModelType(modelType string) bool {
	switch strings.ToLower(strings.TrimSpace(modelType)) {
	case "glm_asr", "glm-asr", "qwen3_asr", "qwen3-asr", "whisper", "wav2vec2", "hubert", "sew", "sew-d", "data2vec-audio", "unispeech", "unispeech-sat", "wavlm", "speech_to_text":
		return true
	default:
		return false
	}
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
	case isUnsupportedDiffusersClass(className):
		return "image-to-video"
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

func isUnsupportedDiffusersClass(className string) bool {
	return strings.Contains(className, "videodiffusion") ||
		strings.Contains(className, "texttovideo") ||
		strings.Contains(className, "imagetovideo") ||
		strings.Contains(className, "image2video") ||
		strings.Contains(className, "video2video") ||
		strings.Contains(className, "videopipeline")
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
		if strings.HasSuffix(lower, ".bin") || strings.HasSuffix(lower, ".pt") || strings.HasSuffix(lower, ".pth") {
			return FormatPyTorch
		}
	}
	return FormatUnknown
}

// FindModelFile returns the primary model file (GGUF or SafeTensors).
func FindModelFile(modelDir string) (string, Format, error) {
	// Prefer GGUF weight files (skip multimodal projector); recurse into subdirs; pick highest precision.
	ggufRel, err := ggufpick.CollectWeightGGUFRelPaths(modelDir)
	if err != nil {
		return "", FormatUnknown, err
	}
	if len(ggufRel) > 0 {
		best := ggufpick.BestWeightGGUFRelPath(ggufRel)
		return filepath.Join(modelDir, best), FormatGGUF, nil
	}
	// Then HuggingFace weights; recurse because uploaded folder selections often
	// preserve a top-level repository directory or nested checkpoint directory.
	if path, ok := findWeightFileBySuffix(modelDir, ".safetensors"); ok {
		return path, FormatSafeTensors, nil
	}
	if path, ok := findWeightFileBySuffix(modelDir, ".bin"); ok {
		return path, FormatPyTorch, nil
	}
	if path, ok := findWeightFileBySuffix(modelDir, ".pt"); ok {
		return path, FormatPyTorch, nil
	}
	if path, ok := findWeightFileBySuffix(modelDir, ".pth"); ok {
		return path, FormatPyTorch, nil
	}
	return "", FormatUnknown, os.ErrNotExist
}

func findWeightFileBySuffix(modelDir, suffix string) (string, bool) {
	var matches []string
	_ = filepath.WalkDir(modelDir, func(current string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), suffix) {
			matches = append(matches, current)
		}
		return nil
	})
	if len(matches) == 0 {
		return "", false
	}
	sort.Strings(matches)
	for _, candidate := range matches {
		if strings.EqualFold(filepath.Base(candidate), "model"+suffix) || strings.EqualFold(filepath.Base(candidate), "pytorch_model"+suffix) {
			return candidate, true
		}
	}
	return matches[0], true
}
