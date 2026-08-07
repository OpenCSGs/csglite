package model

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxModelCardMetadataBytes = 1 << 20

func detectModelCardPipelineTag(modelDir string) string {
	readmePath := findModelCardPath(modelDir)
	if readmePath == "" {
		return ""
	}
	file, err := os.Open(readmePath)
	if err != nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxModelCardMetadataBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxModelCardMetadataBytes {
		return ""
	}
	frontMatter := modelCardFrontMatter(data)
	if len(frontMatter) == 0 {
		return ""
	}
	var metadata map[string]any
	if yaml.Unmarshal(frontMatter, &metadata) != nil {
		return ""
	}
	for _, key := range []string{"pipeline_tag", "pipeline-tag", "task"} {
		if tag := normalizeLocalPipelineTag(stringMetadataValue(metadata[key])); tag != "" {
			return tag
		}
	}
	if pipeline, ok := metadata["pipeline"].(map[string]any); ok {
		if tag := normalizeLocalPipelineTag(stringMetadataValue(pipeline["type"])); tag != "" {
			return tag
		}
	}
	for _, tag := range stringSliceMetadataValue(metadata["tags"]) {
		if normalized := normalizeLocalPipelineTag(tag); normalized != "" {
			return normalized
		}
	}
	return ""
}

func findModelCardPath(modelDir string) string {
	entries, err := os.ReadDir(modelDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(entry.Name(), "README.md") {
			return filepath.Join(modelDir, entry.Name())
		}
	}
	return ""
}

func modelCardFrontMatter(data []byte) []byte {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	lines := bytes.Split(data, []byte("\n"))
	if len(lines) < 3 || strings.TrimSpace(string(bytes.TrimSuffix(lines[0], []byte("\r")))) != "---" {
		return nil
	}
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(string(bytes.TrimSuffix(lines[i], []byte("\r"))))
		if line == "---" || line == "..." {
			return bytes.Join(lines[1:i], []byte("\n"))
		}
	}
	return nil
}

func stringMetadataValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func stringSliceMetadataValue(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if item := stringMetadataValue(value); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func normalizeLocalPipelineTag(value string) string {
	value = strings.NewReplacer("_", "-", " ", "-").Replace(strings.ToLower(strings.TrimSpace(value)))
	switch value {
	case "text-generation", "conversational", "text2text-generation", "fill-mask":
		return "text-generation"
	case "image-text-to-text", "image-to-text", "vision-language", "vision-language-model", "vlm", "vision", "multimodal":
		return "image-text-to-text"
	case "feature-extraction", "sentence-similarity", "text-embedding", "embedding":
		return "feature-extraction"
	case "automatic-speech-recognition", "speech-to-text", "audio-transcription", "asr":
		return "automatic-speech-recognition"
	case "text-to-image", "image-to-image", "text-to-video", "image-to-video", "text-to-speech":
		return value
	default:
		return ""
	}
}
