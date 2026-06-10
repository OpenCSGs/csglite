package server

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

const (
	defaultLocalModelSearchLimit = 20
	maxLocalModelSearchLimit     = 100
)

// GET /api/models/search -- search local downloaded models
func (s *Server) handleLocalModelSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	formatFilter := strings.TrimSpace(r.URL.Query().Get("format"))
	pipelineTagFilter := strings.TrimSpace(r.URL.Query().Get("pipeline_tag"))

	limit, err := parseLocalModelSearchLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	offset, err := parseLocalModelSearchOffset(r.URL.Query().Get("offset"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	models, err := s.listLocalModelInfos()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	filtered := make([]api.ModelInfo, 0, len(models))
	for _, item := range models {
		if !matchesLocalModelSearch(item, query, formatFilter, pipelineTagFilter) {
			continue
		}
		filtered = append(filtered, item)
	}

	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}

	writeJSON(w, http.StatusOK, api.LocalModelSearchResponse{
		Query:       query,
		Format:      formatFilter,
		PipelineTag: pipelineTagFilter,
		Limit:       limit,
		Offset:      offset,
		Total:       total,
		HasMore:     end < total,
		Models:      filtered[offset:end],
	})
}

func (s *Server) listLocalModelInfos() ([]api.ModelInfo, error) {
	localModels, err := s.manager.List()
	if err != nil {
		return nil, err
	}

	sort.SliceStable(localModels, func(i, j int) bool {
		left := strings.TrimSpace(localModels[i].FullName())
		right := strings.TrimSpace(localModels[j].FullName())
		if localModels[i].DownloadedAt.Equal(localModels[j].DownloadedAt) {
			return left < right
		}
		return localModels[i].DownloadedAt.After(localModels[j].DownloadedAt)
	})

	infos := make([]api.ModelInfo, 0, len(localModels))
	for _, item := range localModels {
		if item == nil {
			continue
		}
		infos = append(infos, s.localModelInfo(item))
	}
	return infos, nil
}

func (s *Server) localModelInfo(item *model.LocalModel) api.ModelInfo {
	storageID := strings.TrimSpace(item.FullName())
	inferenceID := model.InferenceModelID(item)
	pipelineTag := s.resolvedLocalPipelineTag(storageID, strings.TrimSpace(item.PipelineTag))
	hasMMProj := false
	var contextWindow int64

	if dir, err := s.manager.ModelPath(storageID); err == nil {
		hasMMProj = model.FindMMProj(dir) != ""
		contextWindow = s.localModelContextWindow(storageID, dir)
	}
	if pipelineTag == "" {
		pipelineTag = "text-generation"
	}

	return api.ModelInfo{
		Name:          inferenceID,
		Model:         inferenceID,
		Size:          item.Size,
		Format:        string(item.Format),
		ModifiedAt:    item.DownloadedAt,
		Label:         inferenceID,
		DisplayName:   inferenceID,
		Source:        "local",
		Provider:      "local",
		Category:      categoryForPipelineTag(pipelineTag),
		PipelineTag:   pipelineTag,
		HasMMProj:     hasMMProj,
		ContextWindow: contextWindow,
		Description:   strings.TrimSpace(item.Description),
		License:       strings.TrimSpace(item.License),
	}
}

func (s *Server) modelUsesEmbeddingEngine(modelID string) bool {
	lm, err := s.manager.ResolveLocalModel(modelID)
	if err != nil || lm == nil {
		return false
	}
	pipelineTag := s.resolvedLocalPipelineTag(lm.FullName(), strings.TrimSpace(lm.PipelineTag))
	return isEmbeddingPipelineTag(pipelineTag)
}

func (s *Server) modelUsesImageGenerationEngine(modelID string) bool {
	lm, err := s.manager.ResolveLocalModel(modelID)
	if err != nil || lm == nil {
		return false
	}
	pipelineTag := s.resolvedLocalPipelineTag(lm.FullName(), strings.TrimSpace(lm.PipelineTag))
	return isImageGenerationPipelineTag(pipelineTag)
}

func (s *Server) modelUsesASREngine(modelID string) bool {
	lm, err := s.manager.ResolveLocalModel(modelID)
	if err != nil || lm == nil {
		return false
	}
	pipelineTag := s.resolvedLocalPipelineTag(lm.FullName(), strings.TrimSpace(lm.PipelineTag))
	return isASRPipelineTag(pipelineTag)
}

func (s *Server) resolvedLocalPipelineTag(modelID, manifestPipelineTag string) string {
	detected := ""
	if dir, err := s.manager.ModelPath(modelID); err == nil {
		detected = strings.TrimSpace(model.DetectPipelineTag(dir))
	}
	if isImageGenerationPipelineTag(detected) || isEmbeddingPipelineTag(detected) || isASRPipelineTag(detected) {
		return detected
	}
	if manifestPipelineTag != "" {
		return manifestPipelineTag
	}
	return detected
}

func isEmbeddingPipelineTag(pipelineTag string) bool {
	switch strings.ToLower(strings.TrimSpace(pipelineTag)) {
	case "feature-extraction", "sentence-similarity", "text-embedding", "embedding":
		return true
	default:
		return false
	}
}

func isImageGenerationPipelineTag(pipelineTag string) bool {
	switch strings.ToLower(strings.TrimSpace(pipelineTag)) {
	case "text-to-image", "image-to-image":
		return true
	default:
		return false
	}
}

func isASRPipelineTag(pipelineTag string) bool {
	switch strings.ToLower(strings.TrimSpace(pipelineTag)) {
	case "automatic-speech-recognition":
		return true
	default:
		return false
	}
}

func (s *Server) localModelContextWindow(modelID, modelDir string) int64 {
	s.mu.RLock()
	if me, ok := s.engines[modelID]; ok && me.numCtx > 0 {
		numCtx := me.numCtx
		s.mu.RUnlock()
		return int64(numCtx)
	}
	s.mu.RUnlock()
	return int64(inference.ResolveNumCtx(modelDir, 0))
}

func matchesLocalModelSearch(item api.ModelInfo, query, formatFilter, pipelineTagFilter string) bool {
	if formatFilter != "" && !strings.EqualFold(strings.TrimSpace(item.Format), strings.TrimSpace(formatFilter)) {
		return false
	}
	if pipelineTagFilter != "" && !strings.EqualFold(strings.TrimSpace(item.PipelineTag), strings.TrimSpace(pipelineTagFilter)) {
		return false
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return true
	}

	query = strings.ToLower(query)
	fields := []string{
		item.Name,
		item.Model,
		item.DisplayName,
		item.Description,
		item.License,
		item.Format,
		item.PipelineTag,
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func parseLocalModelSearchLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultLocalModelSearchLimit, nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid limit %q", raw)
	}
	if value <= 0 {
		return 0, fmt.Errorf("limit must be greater than 0")
	}
	if value > maxLocalModelSearchLimit {
		return maxLocalModelSearchLimit, nil
	}
	return value, nil
}

func parseLocalModelSearchOffset(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid offset %q", raw)
	}
	if value < 0 {
		return 0, fmt.Errorf("offset must be greater than or equal to 0")
	}
	return value, nil
}
