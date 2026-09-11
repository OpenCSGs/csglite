package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/opencsgs/csglite/internal/ggufmeta"
	"github.com/opencsgs/csglite/internal/ggufpick"
)

// Vision-related HuggingFace architecture suffixes/names.
var visionArchitectures = map[string]bool{
	"AudioFlamingo3ForConditionalGeneration": true,
	"ChameleonForConditionalGeneration":      true,
	"CogVLMForCausalLM":                      true,
	"DeepseekOCRForCausalLM":                 true,
	"Dots3NoteForConditionalGeneration":      true,
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
	"KimiK3ForConditionalGeneration":         true,
	"KimiVLForConditionalGeneration":         true,
	"Lfm2VlForConditionalGeneration":         true,
	"LightOnOCRForConditionalGeneration":     true,
	"LlavaForConditionalGeneration":          true,
	"LlavaNextForConditionalGeneration":      true,
	"MERaLiON2ForConditionalGeneration":      true,
	"MiniCPMV":                               true,
	"MiniCPMV4_6ForConditionalGeneration":    true,
	"Mistral3ForConditionalGeneration":       true,
	"MuseGlimmerForConditionalGeneration":    true,
	"NemotronH_Nano_VL_V2":                   true,
	"PaddleOCRVLForConditionalGeneration":    true,
	"Phi3VForCausalLM":                       true,
	"Phi4ForCausalLMV":                       true,
	"Qwen2VLForConditionalGeneration":        true,
	"Qwen2VLModel":                           true,
	"Qwen2_5OmniModel":                       true,
	"Qwen4ExpForConditionalGeneration":       true,
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

// Text-to-speech models must never reach the llama.cpp convert path: their
// language-model half often looks like a plain causal LM (CosyVoice2 and Orpheus
// carry Qwen/Llama backbones), so converting to GGUF silently drops the vocoder
// and the model emits text instead of audio. Detecting them keeps them off that
// path. See docs/guides/realtime-audio-api.md.
var ttsArchitectures = []string{
	"VitsModel",
	"BarkModel",
	"SpeechT5ForTextToSpeech",
	// The prefix covers FastSpeech2ConformerModel and
	// FastSpeech2ConformerWithHifiGan, and matches the substring the worker
	// dispatches on.
	"FastSpeech2Conformer",
	"ParlerTTSForConditionalGeneration",
	"CsmForConditionalGeneration",
	"DiaForConditionalGeneration",
}

var ttsModelFamilies = []string{
	"CosyVoice",
	"Spark-TTS",
	"IndexTTS",
	"F5-TTS",
	"Kokoro",
	"ChatTTS",
	"OuteTTS",
	"Fish-Speech",
	"GPT-SoVITS",
	"MegaTTS",
	"Orpheus-TTS",
	"XTTS",
	"Zonos",
	"Higgs-Audio",
	"SambertHifigan",
}

// Codec-token text-to-speech models are indistinguishable from plain text
// models by config alone: Vikhrmodels/Qwen3-0.6B-TTS declares
// architectures: ["Qwen3ForCausalLM"] with model_type "qwen3", carries no
// pipeline tag, and its ModelScope task is "others". What gives it away is the
// tokenizer -- it has a matched pair of audio span tokens, because it generates
// audio codec tokens rather than text. A plain text model never has those.
var audioSpanStartTokens = []string{
	"<|start_of_audio|>",
	"<|begin_of_audio|>",
	"<|start_of_speech|>",
	"<|begin_of_speech|>",
}

var audioSpanEndTokens = []string{
	"<|end_of_audio|>",
	"<|end_of_speech|>",
}

// tokenizerFilesWithAddedTokens are small enough to read eagerly. tokenizer.json
// is deliberately excluded: it routinely runs to tens of megabytes.
var tokenizerFilesWithAddedTokens = []string{
	"added_tokens.json",
	"special_tokens_map.json",
	"tokenizer_config.json",
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
	"JinaEmbeddingsV5OmniModel":           true,
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

var pythonEmbeddingArchitectures = map[string]bool{
	// Jina v5 remote-code embedding models expose encode/embed helpers that the
	// Python embedding worker can drive directly. Converter-supported entries
	// still prefer llama.cpp because routing checks converter support first.
	"JinaEmbeddingsV5Model":          true,
	"JinaEmbeddingsV5OmniModel":      true,
	"LlavaEuroBertAudioForEmbedding": true,
}

// IsEmbeddingArchitecture reports whether the HuggingFace architecture is treated
// as a local embedding model by csghub-lite.
func IsEmbeddingArchitecture(architecture string) bool {
	return embeddingArchitectures[strings.TrimSpace(architecture)]
}

// IsPythonEmbeddingArchitecture reports whether the HuggingFace architecture is
// supported by csghub-lite's Python embedding runtime.
func IsPythonEmbeddingArchitecture(architecture string) bool {
	return pythonEmbeddingArchitectures[strings.TrimSpace(architecture)]
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
	normalized := normalizeModelFamilyName(name)
	if normalized == "" {
		return false
	}
	for _, supported := range asrModelFamilies {
		if strings.Contains(normalized, normalizeModelFamilyName(supported)) {
			return true
		}
	}
	return false
}

func normalizeModelFamilyName(value string) string {
	return strings.NewReplacer("-", "", "_", "", " ", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(value)))
}

// IsTTSArchitecture reports whether the HuggingFace architecture is a
// text-to-speech model.
func IsTTSArchitecture(architecture string) bool {
	architecture = strings.TrimSpace(architecture)
	if architecture == "" {
		return false
	}
	for _, supported := range ttsArchitectures {
		if strings.Contains(architecture, supported) {
			return true
		}
	}
	return false
}

// IsTTSModelFamily reports whether a model id/name belongs to a known
// text-to-speech family.
func IsTTSModelFamily(name string) bool {
	normalized := normalizeModelFamilyName(name)
	if normalized == "" {
		return false
	}
	for _, supported := range ttsModelFamilies {
		if strings.Contains(normalized, normalizeModelFamilyName(supported)) {
			return true
		}
	}
	return false
}

// TTS backend names reported by TTSBackendFor. They mirror the engines in
// internal/tts/worker/tts_worker.py; a model that matches none of them is a
// text-to-speech model the runtime cannot synthesise, and saying so is better
// than reporting support and failing at synthesis time.
const (
	TTSBackendQwen3        = "qwen3-tts"
	TTSBackendKokoro       = "kokoro"
	TTSBackendTransformers = "transformers"
	TTSBackendVoxCPM       = "voxcpm"
)

// TTSBackendFor reports which text-to-speech backend can serve the model in
// modelDir, or "" when none can. The order matches load_engine() in
// tts_worker.py, and the two must stay in step: this decides what the library
// advertises, that decides what actually runs.
func TTSBackendFor(modelDir, modelName string) string {
	cfg := readModelConfig(modelDir)
	// VoxCPM writes a singular "architecture" field rather than the usual
	// architectures list, so neither the architecture table nor model_type sees
	// it: VoxCPM2 reports "voxcpm2" and VoxCPM-0.5B reports "voxcpm".
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.Architecture)), "voxcpm") {
		return TTSBackendVoxCPM
	}
	// StyleTTS2 keys stand in for a name match, mirroring _is_kokoro_model.
	if cfg.HasKokoroKeys {
		return TTSBackendKokoro
	}
	name := modelName
	if modelDir != "" {
		name += " " + filepath.Base(filepath.Clean(modelDir))
	}
	return ttsBackendFrom(cfg.ModelType, cfg.Architectures, name)
}

// TTSBackendForMetadata makes the same decision from hub metadata alone, for a
// model that has not been downloaded and so has no config.json to read. It keeps
// what the marketplace advertises in step with what the library reports once the
// model is on disk.
func TTSBackendForMetadata(architecture, modelName string) string {
	architectures := []string{}
	if strings.TrimSpace(architecture) != "" {
		architectures = append(architectures, architecture)
	}
	return ttsBackendFrom("", architectures, modelName)
}

func ttsBackendFrom(modelType string, architectures []string, name string) string {
	if strings.EqualFold(strings.TrimSpace(modelType), "qwen3_tts") {
		return TTSBackendQwen3
	}
	for _, arch := range architectures {
		if strings.Contains(arch, "Qwen3TTS") {
			return TTSBackendQwen3
		}
	}
	if strings.Contains(strings.ToLower(name), "kokoro") {
		return TTSBackendKokoro
	}
	for _, arch := range architectures {
		if IsTTSArchitecture(arch) {
			return TTSBackendTransformers
		}
	}
	return ""
}

type ttsModelConfig struct {
	ModelType     string
	Architecture  string
	Architectures []string
	HasKokoroKeys bool
}

// readModelConfig reads the few config.json fields the backend choice needs.
func readModelConfig(modelDir string) ttsModelConfig {
	var out ttsModelConfig
	modelDir = strings.TrimSpace(modelDir)
	if modelDir == "" {
		return out
	}
	data, err := os.ReadFile(filepath.Join(modelDir, "config.json"))
	if err != nil {
		return out
	}
	var cfg struct {
		ModelType     string          `json:"model_type"`
		Architecture  string          `json:"architecture"`
		Architectures []string        `json:"architectures"`
		ISTFTNet      json.RawMessage `json:"istftnet"`
		PLBert        json.RawMessage `json:"plbert"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return out
	}
	out.ModelType = cfg.ModelType
	out.Architecture = cfg.Architecture
	out.Architectures = cfg.Architectures
	// StyleTTS2 configs, which Kokoro uses, carry these instead of an
	// architectures field.
	out.HasKokoroKeys = len(cfg.ISTFTNet) > 0 || len(cfg.PLBert) > 0
	return out
}

// HasAudioOutputTokens reports whether the tokenizer in modelDir defines a
// matched pair of audio span tokens, which marks a model that generates audio
// codec tokens. Both ends are required: a generator needs to open and close the
// span, whereas an audio-input model may declare only a leading marker.
func HasAudioOutputTokens(modelDir string) bool {
	modelDir = strings.TrimSpace(modelDir)
	if modelDir == "" {
		return false
	}
	var start, end bool
	for _, name := range tokenizerFilesWithAddedTokens {
		data, err := os.ReadFile(filepath.Join(modelDir, name))
		if err != nil {
			continue
		}
		lowered := strings.ToLower(string(data))
		for _, token := range audioSpanStartTokens {
			if strings.Contains(lowered, token) {
				start = true
				break
			}
		}
		for _, token := range audioSpanEndTokens {
			if strings.Contains(lowered, token) {
				end = true
				break
			}
		}
		if start && end {
			return true
		}
	}
	return false
}

var ttsNamePattern = regexp.MustCompile(`(^|[^a-z0-9])tts([^a-z0-9]|$)`)

// IsTTSModelName reports whether a model id or name marks itself as
// text-to-speech with a delimited "tts" token, such as Qwen3-0.6B-TTS. The
// family list cannot cover the long tail of fine-tunes that only say so in
// their name.
func IsTTSModelName(name string) bool {
	return ttsNamePattern.MatchString(strings.ToLower(strings.TrimSpace(name)))
}

// DetectPipelineTag reads config.json in modelDir and returns a local pipeline
// tag for routing. Sentence-transformers repositories are treated as embedding
// models even when the hub metadata was not persisted in older manifests.
func DetectPipelineTag(modelDir string) string {
	modelCardTag := detectModelCardPipelineTag(modelDir)
	if modelCardTag != "" && modelCardTag != "text-generation" {
		return modelCardTag
	}
	if IsASRModelFamily(modelDir) {
		return "automatic-speech-recognition"
	}
	if IsTTSModelFamily(modelDir) || IsTTSModelName(modelDir) || HasAudioOutputTokens(modelDir) {
		return "text-to-speech"
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
		if FindMMProj(modelDir) != "" {
			return "image-text-to-text"
		}
		return "text-generation"
	}
	var cfg struct {
		Architectures   []string `json:"architectures"`
		SupportedArchs  []string `json:"supported_archs"`
		SupportedModels []string `json:"supported_models"`
		ModelType       string   `json:"model_type"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		if FindMMProj(modelDir) != "" {
			return "image-text-to-text"
		}
		return "text-generation"
	}
	if isASRModelType(cfg.ModelType) {
		return "automatic-speech-recognition"
	}
	if isTTSModelType(cfg.ModelType) {
		return "text-to-speech"
	}
	for _, name := range cfg.SupportedModels {
		if IsASRModelFamily(name) {
			return "automatic-speech-recognition"
		}
		if IsTTSModelFamily(name) {
			return "text-to-speech"
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
		if IsTTSArchitecture(arch) {
			return "text-to-speech"
		}
	}
	if FindMMProj(modelDir) != "" {
		return "image-text-to-text"
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
		if IsTTSModelFamily(name) {
			return "text-to-speech"
		}
	}
	for _, arch := range cfg.SupportedArchs {
		if IsASRArchitecture(arch) {
			return "automatic-speech-recognition"
		}
		if IsTTSArchitecture(arch) {
			return "text-to-speech"
		}
	}
	if task == "text-to-speech" ||
		task == "text-to-speech-synthesis" ||
		strings.Contains(task, "speech-synthesis") ||
		strings.Contains(pipelineType, "text-to-speech") {
		return "text-to-speech"
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

func isTTSModelType(modelType string) bool {
	switch strings.ToLower(strings.TrimSpace(modelType)) {
	case "vits", "bark", "speecht5", "fastspeech2_conformer", "parler_tts", "csm", "dia", "cosyvoice", "cosyvoice2":
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
	case isImageToImageDiffusersClass(className):
		return "image-to-image"
	case isTextToImageDiffusersClass(className):
		return "text-to-image"
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

// FindMMProj looks for a multimodal projector GGUF file in the model directory.
func FindMMProj(modelDir string) string {
	paths, err := FindMMProjFiles(modelDir)
	if err != nil || len(paths) == 0 {
		return ""
	}
	return paths[0]
}

// FindMMProjFiles returns multimodal projector GGUF files ordered with
// conventionally named mmproj files first, then metadata-detected projectors.
func FindMMProjFiles(modelDir string) ([]string, error) {
	var named []string
	var candidates []string
	err := filepath.WalkDir(modelDir, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
			return nil
		}
		if ggufpick.IsMMProjGGUF(entry.Name()) {
			named = append(named, current)
			return nil
		}
		candidates = append(candidates, current)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(named)
	if len(named) > 0 {
		return named, nil
	}
	var detected []string
	for _, current := range candidates {
		isProjector, err := ggufmeta.IsVisionProjectorFile(current)
		if err == nil && isProjector {
			detected = append(detected, current)
		}
	}
	sort.Strings(detected)
	return detected, nil
}

// FindMTPFiles returns Multi-Token Prediction companion GGUF files.
func FindMTPFiles(modelDir string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(modelDir, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
			return nil
		}
		relPath, err := filepath.Rel(modelDir, current)
		if err == nil && ggufpick.IsMTPGGUF(relPath) {
			paths = append(paths, current)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
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
	return LoadManifestInDir(ModelDir(baseDir, namespace, name))
}

// LoadManifestInDir reads a manifest from an already resolved model directory.
func LoadManifestInDir(modelDir string) (*LocalModel, error) {
	data, err := os.ReadFile(filepath.Join(modelDir, "manifest.json"))
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
	ggufRel, err := FindWeightGGUFRelPaths(modelDir)
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

// FindWeightGGUFRelPaths returns main-model GGUF paths while excluding
// filename- and metadata-identified companion modules.
func FindWeightGGUFRelPaths(modelDir string) ([]string, error) {
	paths, err := ggufpick.CollectWeightGGUFRelPaths(modelDir)
	if err != nil {
		return nil, err
	}
	return filterMetadataProjectors(modelDir, paths), nil
}

func filterMetadataProjectors(modelDir string, paths []string) []string {
	filtered := make([]string, 0, len(paths))
	for _, relPath := range paths {
		isProjector, err := ggufmeta.IsVisionProjectorFile(filepath.Join(modelDir, relPath))
		if err == nil && isProjector {
			continue
		}
		filtered = append(filtered, relPath)
	}
	return filtered
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
