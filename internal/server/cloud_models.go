package server

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/opencsgs/csghub-lite/pkg/api"
)

const forcedCloudModelRefreshInterval = 30 * time.Second

func requestWantsModelRefresh(r *http.Request) bool {
	value := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("refresh")))
	switch value {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *Server) listAvailableModelsWithRefresh(ctx context.Context, refreshCloud bool) ([]api.ModelInfo, error) {
	localModels, err := s.listLocalModelInfos()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(localModels)+8)
	out := make([]api.ModelInfo, 0, len(localModels)+8)
	for _, item := range localModels {
		modelID := strings.TrimSpace(item.Model)
		if modelID == "" {
			continue
		}
		if _, ok := seen[modelID]; ok {
			continue
		}
		seen[modelID] = struct{}{}
		out = append(out, item)
	}

	cloudModels, err := s.listCloudModels(ctx, refreshCloud)
	if err == nil {
		for _, item := range cloudModels {
			modelID := strings.TrimSpace(item.Model)
			if modelID == "" {
				continue
			}
			if _, ok := seen[modelID]; ok {
				continue
			}
			seen[modelID] = struct{}{}
			out = append(out, item)
		}
	}

	for _, item := range s.listThirdPartyProviderModels(ctx) {
		modelID := strings.TrimSpace(item.Model)
		source := strings.TrimSpace(item.Source)
		if modelID == "" || source == "" {
			continue
		}
		key := source + ":" + modelID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}

	sortModelsByPriority(out)

	return out, nil
}

// Public cloud model metadata can be listed without an access token.
// Authentication is enforced later when a cloud inference engine is created.
func (s *Server) listCloudModels(ctx context.Context, refresh bool) ([]api.ModelInfo, error) {
	if s == nil || s.cloud == nil {
		return nil, nil
	}
	if refresh {
		return s.refreshCloudChatModels(ctx)
	}
	return s.cloud.ListChatModels(ctx)
}

func (s *Server) refreshCloudChatModels(ctx context.Context) ([]api.ModelInfo, error) {
	if s == nil || s.cloud == nil {
		return nil, nil
	}

	for {
		s.cloudRefreshMu.Lock()
		if wait := s.cloudRefreshWait; wait != nil {
			s.cloudRefreshMu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		if !s.cloudRefreshAt.IsZero() && time.Since(s.cloudRefreshAt) < forcedCloudModelRefreshInterval {
			s.cloudRefreshMu.Unlock()
			return s.cloud.ListChatModels(ctx)
		}

		wait := make(chan struct{})
		s.cloudRefreshAt = time.Now()
		s.cloudRefreshWait = wait
		s.cloudRefreshMu.Unlock()

		models, err := s.cloud.RefreshChatModels(ctx)

		s.cloudRefreshMu.Lock()
		if s.cloudRefreshWait == wait {
			s.cloudRefreshWait = nil
			close(wait)
		}
		s.cloudRefreshMu.Unlock()

		return models, err
	}
}

func sortModelsByPriority(models []api.ModelInfo) {
	sort.SliceStable(models, func(i, j int) bool {
		iIsLocal := isLocalModelInfo(models[i])
		jIsLocal := isLocalModelInfo(models[j])
		if iIsLocal != jIsLocal {
			return iIsLocal
		}

		iType := strings.TrimSpace(strings.ToLower(models[i].LLMType))
		jType := strings.TrimSpace(strings.ToLower(models[j].LLMType))
		iOwner := strings.TrimSpace(strings.ToLower(models[i].OwnedBy))
		jOwner := strings.TrimSpace(strings.ToLower(models[j].OwnedBy))

		iIsExternal := iType == "external_llm"
		jIsExternal := jType == "external_llm"
		if iIsExternal != jIsExternal {
			return iIsExternal
		}

		iIsOpenCSG := iOwner == "opencsg"
		jIsOpenCSG := jOwner == "opencsg"
		if iIsOpenCSG != jIsOpenCSG {
			return iIsOpenCSG
		}

		return models[i].Model < models[j].Model
	})
}

func isLocalModelInfo(model api.ModelInfo) bool {
	source := strings.TrimSpace(strings.ToLower(model.Source))
	format := strings.TrimSpace(strings.ToLower(model.Format))
	return source == "local" || format == "gguf" || format == "safetensors"
}
