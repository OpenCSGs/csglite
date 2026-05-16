package server

import (
	"strings"

	"github.com/opencsgs/csghub-lite/pkg/api"
)

var supportedPipelineTagGroups = []api.PipelineTagGroup{
	{
		Category: "language_model",
		Label:    "语言模型",
		Tags:     []string{"text-generation", "conversational", "text2text-generation", "fill-mask"},
	},
	{
		Category: "image_generation",
		Label:    "图像生成",
		Tags:     []string{"text-to-image", "image-to-image"},
	},
	{
		Category: "video_generation",
		Label:    "视频生成",
		Tags:     []string{"text-to-video", "image-to-video", "video-text-to-text"},
	},
	{
		Category: "text_to_speech",
		Label:    "语音合成",
		Tags:     []string{"text-to-speech"},
	},
	{
		Category: "speech_recognition",
		Label:    "语音识别",
		Tags:     []string{"automatic-speech-recognition"},
	},
}

func pipelineTagsForCategory(category string) (map[string]struct{}, bool) {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil, true
	}
	for _, group := range supportedPipelineTagGroups {
		if group.Category != category {
			continue
		}
		tags := make(map[string]struct{}, len(group.Tags))
		for _, tag := range group.Tags {
			tags[tag] = struct{}{}
		}
		return tags, true
	}
	return nil, false
}

func filterModelsByPipelineCategory(models []api.ModelInfo, category string) ([]api.ModelInfo, bool) {
	tags, ok := pipelineTagsForCategory(category)
	if !ok {
		return nil, false
	}
	if len(tags) == 0 {
		return models, true
	}

	out := make([]api.ModelInfo, 0, len(models))
	for _, model := range models {
		if _, ok := tags[strings.TrimSpace(model.PipelineTag)]; ok {
			out = append(out, model)
		}
	}
	return out, true
}
