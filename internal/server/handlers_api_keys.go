package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

func (s *Server) handleAPIKeysList(w http.ResponseWriter, r *http.Request) {
	if s.apiKeys == nil {
		writeError(w, http.StatusInternalServerError, "API key store is unavailable")
		return
	}
	state, err := s.apiKeys.State()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load API keys")
		return
	}
	writeJSON(w, http.StatusOK, apiKeysResponse(state))
}

func (s *Server) handleAPIKeysSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	if s.apiKeys == nil {
		writeError(w, http.StatusInternalServerError, "API key store is unavailable")
		return
	}
	var req api.APIKeySettingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	state, err := s.apiKeys.SetAuthEnabled(req.AuthEnabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save API key settings")
		return
	}
	writeJSON(w, http.StatusOK, apiKeysResponse(state))
}

func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	if s.apiKeys == nil {
		writeError(w, http.StatusInternalServerError, "API key store is unavailable")
		return
	}
	var req api.APIKeyCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	record, plain, err := s.apiKeys.Create(strings.TrimSpace(req.Name))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create API key")
		return
	}
	writeJSON(w, http.StatusCreated, api.APIKeyCreateResponse{
		Key:    apiKeyInfo(record),
		APIKey: plain,
	})
}

func (s *Server) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	if s.apiKeys == nil {
		writeError(w, http.StatusInternalServerError, "API key store is unavailable")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "API key id is required")
		return
	}
	deleted, err := s.apiKeys.Delete(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete API key")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "API key not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleAPIUsage(w http.ResponseWriter, r *http.Request) {
	if s.apiUsage == nil {
		writeError(w, http.StatusInternalServerError, "API usage store is unavailable")
		return
	}
	period, since := apiUsagePeriod(r)
	options := config.APIUsageListOptions{
		Provider: strings.TrimSpace(r.URL.Query().Get("provider")),
	}
	if since != nil {
		options.Since = since
	}
	state, err := s.apiUsage.List(options)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load API usage")
		return
	}
	historyState, err := s.apiUsage.List(config.APIUsageListOptions{Provider: options.Provider})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load API usage")
		return
	}
	resp := api.APIUsageResponse{
		Period:       period,
		TotalHistory: apiUsageTotalTokens(historyState.Records),
		Rows:         make([]api.APIUsageRow, 0, len(state.Records)),
		SourceTotals: make([]api.APIUsageSourceTotal, 0, 4),
	}
	sourceHints := apiUsageSourceHints(historyState.Events, historyState.Records)
	resp.TotalSummary = s.apiUsageTotalSummary(r.Context(), state.Events, since, sourceHints)
	if since != nil {
		resp.From = since
	}
	for _, record := range state.Records {
		row := s.apiUsageRow(r.Context(), record, sourceHints)
		resp.Totals.Requests += record.Requests
		resp.Totals.InputTokens += record.InputTokens
		resp.Totals.OutputTokens += record.OutputTokens
		resp.Totals.TotalTokens += record.TotalTokens
		if row.SourceType == apiUsageSourceLocal {
			resp.Totals.LocalTokens += record.TotalTokens
		} else {
			resp.Totals.CloudTokens += record.TotalTokens
		}
		resp.Rows = append(resp.Rows, row)
		addAPIUsageSourceTotal(&resp.SourceTotals, row)
	}
	writeJSON(w, http.StatusOK, resp)
}

func apiUsageTotalTokens(records []config.APIUsageRecord) int64 {
	var total int64
	for _, record := range records {
		total += record.TotalTokens
	}
	return total
}

type apiUsageSourceHint struct {
	source     string
	sourceType string
	sourceName string
}

func apiUsageSourceHints(events []config.APIUsageEventRecord, records []config.APIUsageRecord) map[string]apiUsageSourceHint {
	hints := map[string]apiUsageSourceHint{}
	ambiguous := map[string]struct{}{}
	add := func(modelID, source, sourceType, sourceName string) {
		modelID = strings.TrimSpace(modelID)
		source = strings.TrimSpace(source)
		sourceType = strings.TrimSpace(sourceType)
		sourceName = strings.TrimSpace(sourceName)
		if modelID == "" || source == "" || sourceType == "" {
			return
		}
		if _, ok := ambiguous[modelID]; ok {
			return
		}
		if current, ok := hints[modelID]; ok && (current.source != source || current.sourceType != sourceType) {
			delete(hints, modelID)
			ambiguous[modelID] = struct{}{}
			return
		}
		hints[modelID] = apiUsageSourceHint{
			source:     source,
			sourceType: sourceType,
			sourceName: sourceName,
		}
	}
	for _, event := range events {
		add(event.Model, event.Source, event.SourceType, event.SourceName)
	}
	for _, record := range records {
		add(record.Model, record.Source, record.SourceType, record.SourceName)
	}
	return hints
}

func (s *Server) apiUsageTotalSummary(ctx context.Context, events []config.APIUsageEventRecord, since *time.Time, sourceHints map[string]apiUsageSourceHint) api.APIUsageTotalSummary {
	summary := api.APIUsageTotalSummary{
		XAxis: make([]string, 0),
		Series: []api.APIUsageSummarySeries{
			{Name: "累计消耗", Type: "line", Data: make([]int64, 0)},
			{Name: "本地模型", Type: "line", Data: make([]int64, 0)},
			{Name: "云端模型", Type: "line", Data: make([]int64, 0)},
		},
	}

	daily := map[string]struct {
		local int64
		cloud int64
	}{}
	var firstDay, lastDay time.Time
	for _, event := range events {
		if event.CreatedAt.IsZero() {
			continue
		}
		day := apiUsageDay(event.CreatedAt)
		if firstDay.IsZero() || day.Before(firstDay) {
			firstDay = day
		}
		if lastDay.IsZero() || day.After(lastDay) {
			lastDay = day
		}

		sourceType := strings.TrimSpace(event.SourceType)
		if sourceType == "" {
			if hint, ok := sourceHints[strings.TrimSpace(event.Model)]; ok {
				sourceType = hint.sourceType
			} else {
				_, sourceType, _ = s.resolveAPIUsageSource(ctx, event.Model, event.Source)
			}
		}
		key := day.Format("2006-01-02")
		totals := daily[key]
		if sourceType == apiUsageSourceLocal {
			totals.local += event.TotalTokens
		} else {
			totals.cloud += event.TotalTokens
		}
		daily[key] = totals
	}

	if since != nil {
		firstDay = apiUsageDay(*since)
		lastDay = apiUsageDay(time.Now().UTC())
	}
	if firstDay.IsZero() || lastDay.IsZero() || lastDay.Before(firstDay) {
		return summary
	}

	var cumulativeLocal, cumulativeCloud int64
	for day := firstDay; !day.After(lastDay); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		totals := daily[key]
		cumulativeLocal += totals.local
		cumulativeCloud += totals.cloud
		summary.XAxis = append(summary.XAxis, key)
		summary.Series[0].Data = append(summary.Series[0].Data, cumulativeLocal+cumulativeCloud)
		summary.Series[1].Data = append(summary.Series[1].Data, cumulativeLocal)
		summary.Series[2].Data = append(summary.Series[2].Data, cumulativeCloud)
	}
	return summary
}

func apiUsageDay(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func apiUsagePeriod(r *http.Request) (string, *time.Time) {
	period := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("period")))
	now := time.Now().UTC()
	switch period {
	case "week":
		since := now.AddDate(0, 0, -6)
		return period, &since
	case "month":
		since := now.AddDate(0, 0, -29)
		return period, &since
	case "year":
		since := now.AddDate(0, 0, -364)
		return period, &since
	case "":
		since := now.AddDate(0, 0, -6)
		return "week", &since
	default:
		since := now.AddDate(0, 0, -6)
		return "week", &since
	}
}

func (s *Server) apiUsageRow(ctx context.Context, record config.APIUsageRecord, sourceHints map[string]apiUsageSourceHint) api.APIUsageRow {
	source := strings.TrimSpace(record.Source)
	sourceType := strings.TrimSpace(record.SourceType)
	sourceName := strings.TrimSpace(record.SourceName)
	if sourceType == "" || source == "" {
		if hint, ok := sourceHints[strings.TrimSpace(record.Model)]; ok {
			source = hint.source
			sourceType = hint.sourceType
			sourceName = hint.sourceName
		} else {
			source, sourceType, sourceName = s.resolveAPIUsageSource(ctx, record.Model, source)
		}
	}
	return api.APIUsageRow{
		APIKeyID:     record.APIKeyID,
		APIKeyName:   record.APIKeyName,
		Model:        record.Model,
		Source:       source,
		SourceType:   sourceType,
		SourceName:   sourceName,
		Requests:     record.Requests,
		InputTokens:  record.InputTokens,
		OutputTokens: record.OutputTokens,
		TotalTokens:  record.TotalTokens,
		LastUsedAt:   record.LastUsedAt,
	}
}

func addAPIUsageSourceTotal(totals *[]api.APIUsageSourceTotal, row api.APIUsageRow) {
	sourceType := strings.TrimSpace(row.SourceType)
	if sourceType == "" {
		sourceType = apiUsageSourceUnknown
	}
	source := sourceType
	if row.Source != "" {
		source = row.Source
	}
	for i := range *totals {
		if (*totals)[i].SourceType == sourceType && (*totals)[i].Source == source {
			(*totals)[i].Requests += row.Requests
			(*totals)[i].InputTokens += row.InputTokens
			(*totals)[i].OutputTokens += row.OutputTokens
			(*totals)[i].TotalTokens += row.TotalTokens
			return
		}
	}
	sourceName := row.SourceName
	*totals = append(*totals, api.APIUsageSourceTotal{
		Source:       source,
		SourceType:   sourceType,
		SourceName:   sourceName,
		Requests:     row.Requests,
		InputTokens:  row.InputTokens,
		OutputTokens: row.OutputTokens,
		TotalTokens:  row.TotalTokens,
	})
}

func apiKeysResponse(state config.APIKeyState) api.APIKeysResponse {
	resp := api.APIKeysResponse{
		AuthEnabled: state.AuthEnabled,
		Keys:        make([]api.APIKeyInfo, 0, len(state.Keys)),
	}
	for _, record := range state.Keys {
		resp.Keys = append(resp.Keys, apiKeyInfo(record))
	}
	return resp
}

func apiKeyInfo(record config.APIKeyRecord) api.APIKeyInfo {
	var lastUsedAt *time.Time
	if !record.LastUsedAt.IsZero() {
		lastUsedAt = &record.LastUsedAt
	}
	return api.APIKeyInfo{
		ID:         record.ID,
		Name:       record.Name,
		Prefix:     record.Prefix,
		CreatedAt:  record.CreatedAt,
		LastUsedAt: lastUsedAt,
	}
}
