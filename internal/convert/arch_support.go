package convert

import "strings"

// IsSupportedHFArchitecture reports whether csghub-lite can convert the HuggingFace
// architecture to GGUF via the bundled llama.cpp converter.
func IsSupportedHFArchitecture(hfArch string) bool {
	_, ok := SupportedHFArchitecture(hfArch)
	return ok
}

// SupportedHFArchitecture maps a HuggingFace architecture name to the llama.cpp
// GGUF runtime architecture it converts into.
func SupportedHFArchitecture(hfArch string) (string, bool) {
	arch := strings.TrimSpace(hfArch)
	if arch == "" {
		return "", false
	}
	if ggufArch, ok := archMapping[arch]; ok {
		return ggufArch, true
	}
	ggufArch, ok := pythonConverterArchitectureMapping[arch]
	return ggufArch, ok
}

// pythonConverterArchitectureMapping mirrors @ModelBase.register entries from
// the bundled llama.cpp convert_hf_to_gguf.py that are not all represented in
// archMapping. Values are llama.cpp GGUF architecture names. Keep this list in
// sync when updating the bundled converter.
var pythonConverterArchitectureMapping = map[string]string{
	// Llama/Mistral aliases and VLM variants.
	"LlamaModel":                       "llama",
	"VLlama3ForCausalLM":               "llama",
	"IQuestCoderForCausalLM":           "llama",
	"Idefics3ForConditionalGeneration": "mmp",
	"SmolVLMForConditionalGeneration":  "mmp",

	// Qwen multimodal, audio, ASR, OCR, and model aliases.
	"QwenForCausalLM":                      "qwen2",
	"Qwen2VLModel":                         "qwen2vl",
	"Qwen2_5OmniModel":                     "qwen2vl",
	"Qwen3Model":                           "qwen3",
	"Qwen3OmniMoeForConditionalGeneration": "qwen3vlmoe",
	"Qwen3ASRForConditionalGeneration":     "qwen3vl",
	"Qwen2VLForConditionalGeneration":      "qwen2vl",
	"Qwen2_5_VLForConditionalGeneration":   "qwen2vl",

	// Vision-language / OCR architectures from the bundled converter.
	"InternVisionModel":                   "mmp",
	"NemotronH_Nano_VL_V2":                "mmp",
	"RADIOModel":                          "mmp",
	"PaddleOCRVisionModel":                "mmp",
	"DeepseekOCRForCausalLM":              "deepseek2",
	"StepVLForConditionalGeneration":      "qwen3",
	"MiniCPMV4_6ForConditionalGeneration": "qwen35",
	"HunYuanVLForConditionalGeneration":   "hunyuan-vl",
	"Lfm2VlForConditionalGeneration":      "mmp",
	"Lfm2AudioForConditionalGeneration":   "mmp",
	"LightOnOCRForConditionalGeneration":  "mmp",
	"KimiVLForConditionalGeneration":      "deepseek2",
	"KimiK25ForConditionalGeneration":     "deepseek2",
	"JanusForConditionalGeneration":       "llama",
	"YoutuVLForConditionalGeneration":     "deepseek2",
	"Sarashina2VisionForCausalLM":         "llama",
	"DotsOCRForCausalLM":                  "qwen2",
	"YoutuForCausalLM":                    "deepseek2",

	// Audio / speech architectures.
	"Qwen2AudioForConditionalGeneration":     "qwen2",
	"VoxtralForConditionalGeneration":        "mistral3",
	"AudioFlamingo3ForConditionalGeneration": "qwen2",
	"UltravoxModel":                          "llama",
	"GlmasrModel":                            "mmp",
	"MERaLiON2ForConditionalGeneration":      "mmp",
	"GraniteSpeechForConditionalGeneration":  "mmp",

	// Embedding / encoder architectures supported by llama.cpp conversion.
	"BertModel":                           "bert",
	"BertForMaskedLM":                     "bert",
	"BertForSequenceClassification":       "bert",
	"CamembertModel":                      "bert",
	"DistilBertModel":                     "bert",
	"DistilBertForMaskedLM":               "bert",
	"DistilBertForSequenceClassification": "bert",
	"RobertaModel":                        "bert",
	"RobertaForSequenceClassification":    "bert",
	"NomicBertModel":                      "nomic-bert",
	"NeoBERT":                             "neo-bert",
	"NeoBERTLMHead":                       "neo-bert",
	"NeoBERTForSequenceClassification":    "neo-bert",
	"EuroBertModel":                       "eurobert",
	"JinaEmbeddingsV5Model":               "eurobert",
	"XLMRobertaModel":                     "bert",
	"XLMRobertaForSequenceClassification": "bert",
	"JinaBertModel":                       "jina-bert-v2",
	"JinaBertForMaskedLM":                 "jina-bert-v2",
	"ModernBertModel":                     "modernbert",
	"ModernBertForMaskedLM":               "modernbert",
	"ModernBertForSequenceClassification": "modernbert",

	// Recently added or Python-converter-only model classes.
	"SmolLM3ForCausalLM":                       "smollm3",
	"RuGPT3XLForCausalLM":                      "gpt2",
	"Gemma3TextModel":                          "gemma-embedding",
	"Gemma4ForConditionalGeneration":           "gemma4",
	"MiMoV2ForCausalLM":                        "mimo2",
	"MiMoV2FlashForCausalLM":                   "mimo2",
	"SarvamMoEForCausalLM":                     "bailingmoe2",
	"modeling_sarvam_moe.SarvamMoEForCausalLM": "bailingmoe2",
	"GraniteMoeSharedForCausalLM":              "granitemoe",
	"GraniteMoeHybridForCausalLM":              "granitehybrid",
	"Glm4vForConditionalGeneration":            "glm4",
	"Glm4vMoeForConditionalGeneration":         "glm4moe",
	"GlmOcrForConditionalGeneration":           "glm4",
	"ChameleonForConditionalGeneration":        "chameleon",
	"ChameleonForCausalLM":                     "chameleon",
	"Glm4MoeLiteForCausalLM":                   "deepseek2",
}
