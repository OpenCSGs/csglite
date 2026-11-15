package server

import (
	"context"
	"slices"
	"strings"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

func modelServiceCategory(info api.ModelInfo, fallback string) string {
	if info.DecisionOnly {
		return "decision"
	}
	switch info.PipelineTag {
	case "typed-decisions", "decision":
		return "decision"
	case "text-to-image", "image-to-image":
		return "image"
	case "automatic-speech-recognition":
		return "asr"
	case "text-to-speech", "text-to-audio":
		return "tts"
	case "sentence-similarity", "feature-extraction", "text-embedding", "embedding":
		return "embedding"
	}
	switch info.Category {
	case "image", "asr", "tts", "embedding", "decision":
		return info.Category
	}
	if fallback != "" {
		return fallback
	}
	return "chat"
}
func modelServiceProtocols(category string, info api.ModelInfo, source string) []string {
	var paths []string
	switch category {
	case "embedding":
		paths = []string{"embeddings"}
	case "image":
		paths = []string{"images/generations", "images/edits"}
		if info.PipelineTag == "image-to-image" {
			paths = []string{"images/edits"}
		} else if info.PipelineTag == "text-to-image" && !slices.Contains(info.InputModalities, "image") {
			paths = []string{"images/generations"}
		}
	case "asr":
		paths = []string{"audio/transcriptions"}
	case "tts":
		paths = []string{"audio/speech"}
	case "decision":
		paths = []string{"decisions"}
	default:
		paths = []string{"chat/completions", "responses", "messages"}
		if info.TypedDecisions {
			paths = append(paths, "decisions")
		}
	}
	// Existing third-party adapters support text, embeddings and images. Cloud
	// ASR exists, while TTS is currently provided by local or cluster runtimes.
	if strings.HasPrefix(source, "provider:") && (category == "asr" || category == "tts" || category == "decision") {
		return []string{}
	}
	if source == "cloud" && category == "tts" {
		return []string{}
	}
	return paths
}
func (s *Server) poolMemberInfo(member config.ProviderPoolMember) (api.ModelInfo, bool) {
	if member.Source == "local" {
		if lm, err := s.resolveDecisionModel(member.Model); err == nil {
			return s.localModelInfo(lm), true
		}
	}
	if cluster.IsClusterSource(member.Source) && s.cluster != nil {
		pinned := cluster.NodeUUIDFromSource(member.Source)
		if pinned != "" {
			for _, node := range s.cluster.View(context.Background()).Members {
				if node.UUID != pinned || node.Status == nil {
					continue
				}
				if ms, ok := node.Status.Model(member.Model); ok {
					return api.ModelInfo{Category: ms.Category, PipelineTag: ms.PipelineTag, TypedDecisions: ms.TypedDecisions, DecisionOnly: ms.DecisionOnly, HasMMProj: ms.HasMMProj}, true
				}
			}
		} else {
			for _, m := range s.cluster.Models(context.Background()) {
				if m.ID == member.Model {
					return api.ModelInfo{Category: m.Category, PipelineTag: m.PipelineTag, TypedDecisions: m.TypedDecisions, DecisionOnly: m.DecisionOnly, HasMMProj: m.HasMMProj}, true
				}
			}
		}
	}
	if member.Source == "cloud" && s.cloud != nil {
		for _, m := range s.cloud.KnownChatModels() {
			if m.Model == member.Model {
				return m, true
			}
		}
	}
	provider := providerIDFromSource(member.Source)
	if member.Source == "cloud" {
		provider = cloudProviderRouteID
	}
	for _, m := range config.GetProviderModelSelections(provider) {
		if m.Model == member.Model {
			return api.ModelInfo{PipelineTag: m.PipelineTag, InputModalities: m.InputModalities, OutputModalities: m.OutputModalities}, true
		}
	}
	return api.ModelInfo{}, false
}
func (s *Server) fillPoolProtocols(pool config.ProviderPool, out *api.ProviderPool) {
	var common []string
	category := ""
	typed, only, vision := true, true, true
	count := 0
	for i, member := range pool.Members {
		if pool.Policy == config.ProviderPoolPolicyFixed && i > 0 {
			break
		}
		if s.providerPoolSourceState(member) != "configured" {
			continue
		}
		info, _ := s.poolMemberInfo(member)
		kind := modelServiceCategory(info, pool.Category)
		paths := modelServiceProtocols(kind, info, member.Source)
		if count == 0 {
			common = paths
			category = kind
		} else {
			common = slices.DeleteFunc(common, func(p string) bool { return !slices.Contains(paths, p) })
			if category != kind {
				category = "mixed"
			}
		}
		typed = typed && (info.TypedDecisions || kind == "decision")
		only = only && (info.DecisionOnly || kind == "decision")
		vision = vision && (info.PipelineTag == "image-text-to-text" || info.HasMMProj || slices.Contains(info.InputModalities, "image"))
		count++
	}
	if count == 0 {
		common = []string{}
		typed, only, vision = false, false, false
	}
	if common == nil {
		common = []string{}
	}
	if category != "" {
		out.Category = category
	}
	out.InferenceProtocols = common
	out.TypedDecisions = typed
	out.DecisionOnly = only
	out.Vision = vision
}

func poolPipelineTag(pool api.ProviderPool) string {
	switch pool.Category {
	case "embedding":
		return "feature-extraction"
	case "image":
		if slices.Contains(pool.InferenceProtocols, "images/edits") && !slices.Contains(pool.InferenceProtocols, "images/generations") {
			return "image-to-image"
		}
		return "text-to-image"
	case "asr":
		return "automatic-speech-recognition"
	case "tts":
		return "text-to-speech"
	case "decision":
		return "typed-decisions"
	default:
		if pool.Vision {
			return "image-text-to-text"
		}
		return "text-generation"
	}
}
