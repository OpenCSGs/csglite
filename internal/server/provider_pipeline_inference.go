package server

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
)

type thirdPartyProviderModel struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	DisplayName     string                 `json:"display_name"`
	PipelineTag     string                 `json:"pipeline_tag"`
	Task            string                 `json:"task"`
	Type            string                 `json:"type"`
	SubType         string                 `json:"sub_type"`
	Mode            string                 `json:"mode"`
	OwnedBy         string                 `json:"owned_by"`
	Architecture    *providerArchitecture  `json:"architecture"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ModelInfo       map[string]interface{} `json:"model_info"`
	SupportsImageIn bool                   `json:"supports_image_in"`
	SupportsVideoIn bool                   `json:"supports_video_in"`
	Raw             map[string]interface{} `json:"-"`
}

type providerArchitecture struct {
	Modality         string   `json:"modality"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

func (m *thirdPartyProviderModel) UnmarshalJSON(data []byte) error {
	type alias thirdPartyProviderModel
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	var raw map[string]interface{}
	_ = json.Unmarshal(data, &raw)
	*m = thirdPartyProviderModel(a)
	m.Raw = raw
	return nil
}

func inferThirdPartyModelMetadata(provider config.ThirdPartyProvider, item thirdPartyProviderModel) (string, []string, []string) {
	inputs, outputs := inferProviderModalities(item)
	if tag := normalizePipelineTag(item.PipelineTag); tag != "" {
		return tag, inputs, outputs
	}
	if tag := normalizePipelineTag(item.Task); tag != "" {
		return tag, inputs, outputs
	}
	if tag := pipelineTagFromProviderFields(item); tag != "" {
		return tag, inputs, outputs
	}
	if rule, ok := lookupLiteLLMPipelineRule(provider, item); ok {
		inputs, outputs = mergeLiteLLMRuleModalities(inputs, outputs, rule)
		if tag := pipelineTagFromLiteLLMRule(rule); tag != "" {
			inputs, outputs = ensureModalitiesForPipelineTag(inputs, outputs, tag)
			return tag, inputs, outputs
		}
	}
	if tag := pipelineTagFromModelName(item.ID, item.Name, item.DisplayName); tag != "" {
		inputs, outputs = ensureModalitiesForPipelineTag(inputs, outputs, tag)
		return tag, inputs, outputs
	}
	if containsModality(inputs, "video") {
		return "video-text-to-text", inputs, ensureModalities(outputs, "text")
	}
	return "text-generation", inputs, ensureModalities(outputs, "text")
}

func inferProviderModalities(item thirdPartyProviderModel) ([]string, []string) {
	var inputs, outputs []string
	if item.Architecture != nil {
		inputs = appendModalities(inputs, item.Architecture.InputModalities...)
		outputs = appendModalities(outputs, item.Architecture.OutputModalities...)
		archInputs, archOutputs := parseArchitectureModality(item.Architecture.Modality)
		inputs = appendModalities(inputs, archInputs...)
		outputs = appendModalities(outputs, archOutputs...)
	}
	if item.SupportsImageIn || capabilitySupported(item.Capabilities, "image_input") || boolField(item.ModelInfo, "supports_vision") || boolField(item.Raw, "supports_vision") {
		inputs = appendModalities(inputs, "image")
	}
	if item.SupportsVideoIn || boolField(item.ModelInfo, "supports_video_input") || boolField(item.Raw, "supports_video_input") {
		inputs = appendModalities(inputs, "video")
	}
	if boolField(item.ModelInfo, "supports_audio_input") || boolField(item.Raw, "supports_audio_input") {
		inputs = appendModalities(inputs, "audio")
	}
	if boolField(item.ModelInfo, "supports_audio_output") || boolField(item.Raw, "supports_audio_output") {
		outputs = appendModalities(outputs, "audio")
	}
	return inputs, outputs
}

func pipelineTagFromProviderFields(item thirdPartyProviderModel) string {
	subType := strings.TrimSpace(strings.ToLower(item.SubType))
	switch subType {
	case "text-to-image":
		return "text-to-image"
	case "image-to-image":
		return "image-to-image"
	case "text-to-video":
		return "text-to-video"
	case "image-to-video":
		return "image-to-video"
	case "speech-to-text", "audio-transcription", "audio_transcription":
		return "automatic-speech-recognition"
	}
	mode := jsonStringField(item.ModelInfo, "mode")
	if mode == "" {
		mode = item.Mode
	}
	if tag := pipelineTagFromMode(mode); tag != "" {
		return tag
	}
	if item.Architecture != nil {
		if tag := pipelineTagFromModalities(item.Architecture.InputModalities, item.Architecture.OutputModalities); tag != "" {
			return tag
		}
		inputs, outputs := parseArchitectureModality(item.Architecture.Modality)
		if tag := pipelineTagFromModalities(inputs, outputs); tag != "" {
			return tag
		}
	}
	return ""
}

func lookupLiteLLMPipelineRule(provider config.ThirdPartyProvider, item thirdPartyProviderModel) (liteLLMPipelineRule, bool) {
	providerAliases := []string{provider.Provider, provider.Name, provider.ID, providerIDFromBaseURL(provider.BaseURL)}
	modelAliases := []string{item.ID, item.Name, item.DisplayName}
	for _, model := range modelAliases {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if rule, ok := liteLLMPipelineRules[normalizePipelineRuleKey(model)]; ok {
			return rule, true
		}
		if rule, ok := lookupLiteLLMRuleByVersionedModel(model); ok {
			return rule, true
		}
		for _, providerAlias := range providerAliases {
			providerAlias = strings.TrimSpace(providerAlias)
			if providerAlias == "" {
				continue
			}
			key := providerAlias + "/" + model
			if rule, ok := liteLLMPipelineRules[normalizePipelineRuleKey(key)]; ok {
				return rule, true
			}
			if rule, ok := lookupLiteLLMRuleByVersionedModel(key); ok {
				return rule, true
			}
		}
	}
	return liteLLMPipelineRule{}, false
}

func lookupLiteLLMRuleByVersionedModel(model string) (liteLLMPipelineRule, bool) {
	key := normalizePipelineRuleKey(model)
	for known, rule := range liteLLMPipelineRules {
		if len(known) < 8 {
			continue
		}
		if strings.HasPrefix(key, known+"-") || strings.HasPrefix(key, known+"_") {
			return rule, true
		}
	}
	return liteLLMPipelineRule{}, false
}

func mergeLiteLLMRuleModalities(inputs, outputs []string, rule liteLLMPipelineRule) ([]string, []string) {
	if rule.SupportsVision {
		inputs = appendModalities(inputs, "image")
	}
	if rule.SupportsVideoInput {
		inputs = appendModalities(inputs, "video")
	}
	if rule.SupportsAudioInput {
		inputs = appendModalities(inputs, "audio")
	}
	if rule.SupportsAudioOutput {
		outputs = appendModalities(outputs, "audio")
	}
	if rule.Mode == "audio_speech" {
		outputs = appendModalities(outputs, "audio")
	}
	return inputs, outputs
}

func pipelineTagFromLiteLLMRule(rule liteLLMPipelineRule) string {
	if rule.SupportsVideoInput && (rule.Mode == "" || rule.Mode == "chat" || rule.Mode == "completion") {
		return "video-text-to-text"
	}
	return pipelineTagFromMode(rule.Mode)
}

func pipelineTagFromMode(mode string) string {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "chat", "completion":
		return "text-generation"
	case "image_generation":
		return "text-to-image"
	case "audio_speech":
		return "text-to-speech"
	case "audio_transcription":
		return "automatic-speech-recognition"
	default:
		return ""
	}
}

func pipelineTagFromModalities(inputs, outputs []string) string {
	switch {
	case containsModality(outputs, "image"):
		if containsModality(inputs, "image") {
			return "image-to-image"
		}
		return "text-to-image"
	case containsModality(outputs, "video"):
		if containsModality(inputs, "image") {
			return "image-to-video"
		}
		return "text-to-video"
	case containsModality(outputs, "speech"), containsModality(outputs, "audio"):
		return "text-to-speech"
	case containsModality(outputs, "transcription"):
		return "automatic-speech-recognition"
	case containsModality(inputs, "video") && containsModality(outputs, "text"):
		return "video-text-to-text"
	default:
		return ""
	}
}

func pipelineTagFromModelName(values ...string) string {
	value := strings.ToLower(strings.Join(values, " "))
	value = strings.ReplaceAll(value, "_", "-")
	switch {
	case regexp.MustCompile(`\b(tts|text-to-speech|voiceclone|voice-clone|voicedesign|voice-design)\b`).MatchString(value):
		return "text-to-speech"
	case regexp.MustCompile(`\b(asr|whisper|transcribe|transcription|speech-to-text)\b`).MatchString(value):
		return "automatic-speech-recognition"
	case strings.Contains(value, "image-to-image") || strings.Contains(value, "img2img"):
		return "image-to-image"
	case strings.Contains(value, "image-to-video") || strings.Contains(value, "img2video"):
		return "image-to-video"
	case strings.Contains(value, "text-to-video") || strings.Contains(value, "txt2video"):
		return "text-to-video"
	case strings.Contains(value, "text-to-image") || strings.Contains(value, "txt2img") ||
		strings.Contains(value, "stable-diffusion") || strings.Contains(value, "sdxl") ||
		strings.Contains(value, "flux") || strings.Contains(value, "dall-e") ||
		strings.Contains(value, "imagen") || strings.Contains(value, "qwen-image"):
		return "text-to-image"
	default:
		return ""
	}
}

func ensureModalitiesForPipelineTag(inputs, outputs []string, tag string) ([]string, []string) {
	switch tag {
	case "text-to-image":
		return ensureModalities(inputs, "text"), ensureModalities(outputs, "image")
	case "image-to-image":
		return ensureModalities(inputs, "image"), ensureModalities(outputs, "image")
	case "text-to-video":
		return ensureModalities(inputs, "text"), ensureModalities(outputs, "video")
	case "image-to-video":
		return ensureModalities(inputs, "image"), ensureModalities(outputs, "video")
	case "video-text-to-text":
		return ensureModalities(inputs, "video"), ensureModalities(outputs, "text")
	case "text-to-speech":
		return ensureModalities(inputs, "text"), ensureModalities(outputs, "audio")
	case "automatic-speech-recognition":
		return ensureModalities(inputs, "audio"), ensureModalities(outputs, "text")
	default:
		return inputs, outputs
	}
}

func parseArchitectureModality(value string) ([]string, []string) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || !strings.Contains(value, "->") {
		return nil, nil
	}
	parts := strings.SplitN(value, "->", 2)
	return strings.Split(parts[0], "+"), strings.Split(parts[1], "+")
}

func normalizePipelineTag(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "text-generation", "conversational", "text2text-generation", "fill-mask",
		"text-to-image", "image-to-image", "text-to-video", "image-to-video",
		"video-text-to-text", "text-to-speech", "automatic-speech-recognition",
		"image-text-to-text":
		return value
	case "speech-to-text", "audio_transcription", "audio-transcription":
		return "automatic-speech-recognition"
	case "audio_speech", "audio-speech":
		return "text-to-speech"
	default:
		return ""
	}
}

func normalizePipelineRuleKey(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), " ", "")
}

func providerIDFromBaseURL(baseURL string) string {
	host := strings.ToLower(baseURL)
	switch {
	case strings.Contains(host, "openrouter.ai"):
		return "openrouter"
	case strings.Contains(host, "dashscope"):
		return "dashscope"
	case strings.Contains(host, "volces.com"):
		return "volcengine"
	case strings.Contains(host, "siliconflow"):
		return "siliconflow"
	case strings.Contains(host, "deepseek"):
		return "deepseek"
	case strings.Contains(host, "moonshot"):
		return "moonshot"
	case strings.Contains(host, "bigmodel"), strings.Contains(host, "z.ai"):
		return "zai"
	case strings.Contains(host, "openai"):
		return "openai"
	default:
		return ""
	}
}

func appendModalities(out []string, values ...string) []string {
	seen := make(map[string]struct{}, len(out)+len(values))
	for _, item := range out {
		seen[item] = struct{}{}
	}
	for _, value := range values {
		value = normalizeModality(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func ensureModalities(out []string, values ...string) []string {
	return appendModalities(out, values...)
}

func normalizeModality(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "text", "image", "video", "audio", "speech", "transcription", "file":
		return value
	case "images":
		return "image"
	default:
		return ""
	}
}

func containsModality(values []string, target string) bool {
	target = normalizeModality(target)
	for _, value := range values {
		if normalizeModality(value) == target {
			return true
		}
	}
	return false
}

func capabilitySupported(values map[string]interface{}, key string) bool {
	raw, ok := values[key]
	if !ok {
		return false
	}
	if value, ok := raw.(bool); ok {
		return value
	}
	if obj, ok := raw.(map[string]interface{}); ok {
		return boolField(obj, "supported")
	}
	return false
}

func boolField(values map[string]interface{}, key string) bool {
	if values == nil {
		return false
	}
	value, ok := values[key]
	if !ok {
		return false
	}
	if b, ok := value.(bool); ok {
		return b
	}
	return false
}

func jsonStringField(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
