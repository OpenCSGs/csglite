package localinference

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

// FromLocalModel reports whether a downloaded model can run locally via llama.cpp
// or the Diffusers image runtime.
func FromLocalModel(lm *model.LocalModel, modelDir string) api.LocalInferenceSupport {
	if lm == nil {
		return unsupported("")
	}

	manifestPipelineTag := strings.TrimSpace(lm.PipelineTag)
	pipelineTag := manifestPipelineTag
	if modelDir != "" {
		if detected := strings.TrimSpace(model.DetectPipelineTag(modelDir)); detected != "" {
			pipelineTag = detected
		}
	}

	if support := diffusersSupportFromPipelineTag(pipelineTag); support.Supported {
		return support
	}
	if support := asrSupportFromPipelineTag(pipelineTag); support.Supported {
		return support
	}
	modelName := strings.TrimSpace(lm.FullName())
	if support, claimed := ttsRouting(pipelineTag, modelDir, modelName, readArchitectureFromDir(modelDir)); claimed {
		return support
	}
	if support, claimed := ttsRouting(manifestPipelineTag, modelDir, modelName, readArchitectureFromDir(modelDir)); claimed {
		return support
	}
	// The detected tag overrides the manifest tag above, but DetectPipelineTag
	// falls back to "text-generation" whenever it finds nothing more specific.
	// A manifest tag that has no local runtime therefore has to be honoured on
	// its own: a text-to-speech model whose config.json carries a plain causal LM
	// architecture would otherwise reach llamaSupport() and be converted to GGUF.
	if unsupportedPipelineTag(pipelineTag) || unsupportedPipelineTag(manifestPipelineTag) {
		return unsupported("")
	}

	architecture := readArchitectureFromDir(modelDir)
	if model.IsASRArchitecture(architecture) {
		return asrSupport(architecture)
	}
	if model.IsTTSArchitecture(architecture) {
		return ttsMetadataSupport(architecture, modelName)
	}
	return llamaSupport(string(lm.Format), architecture)
}

// FromMarketplace reports whether a marketplace model is likely to support local
// inference after download, based on format tags and hub metadata.
func FromMarketplace(format, architecture, className string) api.LocalInferenceSupport {
	if isUnsupportedDiffusersClass(className) {
		return unsupported(architecture)
	}
	if support := diffusersSupportFromClassName(className); support.Supported {
		return support
	}
	if model.IsASRArchitecture(architecture) {
		return asrSupport(architecture)
	}
	if model.IsTTSArchitecture(architecture) {
		return ttsMetadataSupport(architecture, "")
	}
	return llamaSupport(format, architecture)
}

// FromMarketplaceModel reports likely support for a marketplace model when the
// model id/name or task tag provides stronger routing hints than config metadata.
func FromMarketplaceModel(format, architecture, className, modelName, pipelineTag string) api.LocalInferenceSupport {
	switch normalizePipelineTag(pipelineTag) {
	case "text-to-image", "image-to-image":
		return diffusersSupportForImageTask(architecture, className)
	case "automatic-speech-recognition":
		return asrSupport(architecture)
	case "text-to-speech":
		return ttsMetadataSupport(architecture, modelName)
	case "image-to-video", "text-to-video", "video-text-to-text":
		return unsupported(architecture)
	}

	if model.IsASRModelFamily(modelName) {
		return asrSupport(architecture)
	}
	if model.IsTTSModelFamily(modelName) || model.IsTTSModelName(modelName) {
		return ttsMetadataSupport(architecture, modelName)
	}
	return FromMarketplace(format, architecture, className)
}

func llamaSupport(format, architecture string) api.LocalInferenceSupport {
	normalizedFormat := strings.ToLower(strings.TrimSpace(format))
	normalizedArch := strings.TrimSpace(architecture)

	switch normalizedFormat {
	case string(model.FormatGGUF):
		runtimeArch := llamaRuntimeArchitecture(normalizedArch)
		return api.LocalInferenceSupport{
			Supported:           true,
			Runtime:             "llama",
			Mode:                "direct",
			Architecture:        normalizedArch,
			RuntimeArchitecture: runtimeArch,
		}
	case string(model.FormatSafeTensors), string(model.FormatPyTorch):
		if runtimeArch, ok := convertibleRuntimeArchitecture(normalizedArch); ok {
			return api.LocalInferenceSupport{
				Supported:           true,
				Runtime:             "llama",
				Mode:                "convert",
				Architecture:        normalizedArch,
				RuntimeArchitecture: runtimeArch,
			}
		}
		if model.IsPythonEmbeddingArchitecture(normalizedArch) {
			return api.LocalInferenceSupport{
				Supported:    true,
				Runtime:      "python-embedding",
				Mode:         "embedding",
				Architecture: normalizedArch,
			}
		}
	}

	return unsupported(normalizedArch)
}

func convertibleRuntimeArchitecture(architecture string) (string, bool) {
	architecture = strings.TrimSpace(architecture)
	if architecture == "" {
		return "", false
	}
	if runtimeArch, ok := convert.SupportedHFArchitecture(architecture); ok {
		return runtimeArch, true
	}
	return "", false
}

func llamaRuntimeArchitecture(architecture string) string {
	if runtimeArch, ok := convert.SupportedHFArchitecture(architecture); ok {
		return runtimeArch
	}
	return strings.ToLower(strings.TrimSpace(architecture))
}

func diffusersSupportFromPipelineTag(pipelineTag string) api.LocalInferenceSupport {
	switch normalizePipelineTag(pipelineTag) {
	case "text-to-image", "image-to-image":
		return api.LocalInferenceSupport{
			Supported: true,
			Runtime:   "diffusers",
			Mode:      "image",
		}
	default:
		return api.LocalInferenceSupport{Mode: "none"}
	}
}

func diffusersSupportForImageTask(architecture, className string) api.LocalInferenceSupport {
	classPipelineTag := diffusersPipelineTagFromClassName(className)
	switch classPipelineTag {
	case "text-to-image", "image-to-image":
		return api.LocalInferenceSupport{
			Supported: true,
			Runtime:   "diffusers",
			Mode:      "image",
		}
	case "":
		if strings.TrimSpace(className) == "" {
			return api.LocalInferenceSupport{
				Supported: true,
				Runtime:   "diffusers",
				Mode:      "image",
			}
		}
		return unsupported(architecture)
	default:
		return unsupported(architecture)
	}
}

// ttsSupportFromPipelineTag routes text-to-speech models to the Python runtime.
// They must never reach llama.cpp: the vocoder or codec decoder that turns model
// output into a waveform lives in the model's own inference stack.
// ttsRouting reports how a text-to-speech model should be presented. Supported
// is true only when the runtime has a backend that can actually synthesise it:
// a codec-token model such as Vikhrmodels/Qwen3-0.6B-TTS is a genuine
// text-to-speech model, but it ships no codec decoder and no backend can turn
// its output into a waveform, so advertising support would repeat the original
// complaint in #147 in a new form -- the library says yes and synthesis fails.
//
// Either way the tag stays claimed, which keeps the model off the llama.cpp
// convert path.
func ttsRouting(pipelineTag, modelDir, modelName, architecture string) (api.LocalInferenceSupport, bool) {
	if normalizePipelineTag(pipelineTag) != "text-to-speech" {
		return api.LocalInferenceSupport{Mode: "none"}, false
	}
	if model.TTSBackendFor(modelDir, modelName) == "" {
		return unsupported(architecture), true
	}
	return api.LocalInferenceSupport{
		Supported:    true,
		Runtime:      "python-tts",
		Mode:         "tts",
		Architecture: strings.TrimSpace(architecture),
	}, true
}

// ttsMetadataSupport decides from hub metadata alone, for a model that is not on
// disk yet. It reports unsupported for a text-to-speech shape no backend can
// serve, so the marketplace does not promise something the library will retract
// after the download.
func ttsMetadataSupport(architecture, modelName string) api.LocalInferenceSupport {
	if model.TTSBackendForMetadata(architecture, modelName) == "" {
		return unsupported(architecture)
	}
	return api.LocalInferenceSupport{
		Supported:    true,
		Runtime:      "python-tts",
		Mode:         "tts",
		Architecture: strings.TrimSpace(architecture),
	}
}

func asrSupportFromPipelineTag(pipelineTag string) api.LocalInferenceSupport {
	switch normalizePipelineTag(pipelineTag) {
	case "automatic-speech-recognition":
		return api.LocalInferenceSupport{
			Supported: true,
			Runtime:   "python-asr",
			Mode:      "asr",
		}
	default:
		return api.LocalInferenceSupport{Mode: "none"}
	}
}

func asrSupport(architecture string) api.LocalInferenceSupport {
	return api.LocalInferenceSupport{
		Supported:    true,
		Runtime:      "python-asr",
		Mode:         "asr",
		Architecture: strings.TrimSpace(architecture),
	}
}

func diffusersSupportFromClassName(className string) api.LocalInferenceSupport {
	pipelineTag := diffusersPipelineTagFromClassName(className)
	if pipelineTag == "" {
		return api.LocalInferenceSupport{Mode: "none"}
	}
	return diffusersSupportFromPipelineTag(pipelineTag)
}

func diffusersPipelineTagFromClassName(className string) string {
	className = strings.ToLower(strings.TrimSpace(className))
	if className == "" {
		return ""
	}
	switch {
	case isUnsupportedDiffusersClass(className):
		return "image-to-video"
	case isImageToImageDiffusersClass(className):
		return "image-to-image"
	case isTextToImageDiffusersClass(className):
		return "text-to-image"
	case strings.Contains(className, "pipeline"):
		return "text-to-image"
	default:
		return ""
	}
}

// unsupportedPipelineTag lists pipeline tags that have no local runtime. Without
// an explicit entry a tag falls through to llamaSupport(), which would convert a
// safetensors model to GGUF and serve it as text.
func unsupportedPipelineTag(pipelineTag string) bool {
	switch normalizePipelineTag(pipelineTag) {
	case "image-to-video", "text-to-video", "video-text-to-text":
		return true
	default:
		return false
	}
}

func normalizePipelineTag(pipelineTag string) string {
	return strings.ToLower(strings.TrimSpace(pipelineTag))
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

func readArchitectureFromDir(modelDir string) string {
	if strings.TrimSpace(modelDir) == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(modelDir, "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Architectures []string `json:"architectures"`
	}
	if json.Unmarshal(data, &cfg) != nil || len(cfg.Architectures) == 0 {
		return ""
	}
	return strings.TrimSpace(cfg.Architectures[0])
}

func unsupported(architecture string) api.LocalInferenceSupport {
	return api.LocalInferenceSupport{
		Mode:         "none",
		Architecture: strings.TrimSpace(architecture),
	}
}
