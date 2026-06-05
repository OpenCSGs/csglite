package localinference

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csghub-lite/internal/convert"
	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

// FromLocalModel reports whether a downloaded model can run locally via llama.cpp
// or the Diffusers image runtime.
func FromLocalModel(lm *model.LocalModel, modelDir string) api.LocalInferenceSupport {
	if lm == nil {
		return unsupported("")
	}

	pipelineTag := strings.TrimSpace(lm.PipelineTag)
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

	architecture := readArchitectureFromDir(modelDir)
	if model.IsASRArchitecture(architecture) {
		return asrSupport(architecture)
	}
	return llamaSupport(string(lm.Format), architecture)
}

// FromMarketplace reports whether a marketplace model is likely to support local
// inference after download, based on format tags and hub metadata.
func FromMarketplace(format, architecture, className string) api.LocalInferenceSupport {
	if support := diffusersSupportFromClassName(className); support.Supported {
		return support
	}
	if model.IsASRArchitecture(architecture) {
		return asrSupport(architecture)
	}
	return llamaSupport(format, architecture)
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
	if model.IsEmbeddingArchitecture(architecture) || model.IsVisionArchitecture(architecture) {
		return strings.ToLower(architecture), true
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
	switch strings.ToLower(strings.TrimSpace(pipelineTag)) {
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

func asrSupportFromPipelineTag(pipelineTag string) api.LocalInferenceSupport {
	switch strings.ToLower(strings.TrimSpace(pipelineTag)) {
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
	case isTextToImageDiffusersClass(className):
		return "text-to-image"
	case isImageToImageDiffusersClass(className):
		return "image-to-image"
	case strings.Contains(className, "pipeline"):
		return "text-to-image"
	default:
		return ""
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
