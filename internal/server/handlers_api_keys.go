package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/pkg/api"
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
	options := config.APIUsageListOptions{}
	if since != nil {
		options.Since = since
	}
	state, err := s.apiUsage.List(options)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load API usage")
		return
	}
	resp := api.APIUsageResponse{
		Period:       period,
		TotalSummary: s.apiUsageTotalSummary(r.Context(), state.Events),
		Rows:         make([]api.APIUsageRow, 0, len(state.Records)),
		SourceTotals: make([]api.APIUsageSourceTotal, 0, 4),
	}
	if since != nil {
		resp.From = since
	}
	for _, record := range state.Records {
		resp.Totals.Requests += record.Requests
		resp.Totals.InputTokens += record.InputTokens
		resp.Totals.OutputTokens += record.OutputTokens
		resp.Totals.TotalTokens += record.TotalTokens
		row := s.apiUsageRow(r.Context(), record)
		resp.Rows = append(resp.Rows, row)
		addAPIUsageSourceTotal(&resp.SourceTotals, row)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) apiUsageTotalSummary(ctx context.Context, events []config.APIUsageEventRecord) api.APIUsageTotalSummary {
	summary := api.APIUsageTotalSummary{
		XAxis: make([]string, 0),
		Series: []api.APIUsageSummarySeries{
			{Name: "累计消耗", Type: "line", Data: make([]int64, 0)},
			{Name: "本地模型", Type: "line", Data: make([]int64, 0)},
			{Name: "云端模型", Type: "line", Data: make([]int64, 0)},
		},
	}
	if len(events) == 0 {
		return summary
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
		day := time.Date(event.CreatedAt.UTC().Year(), event.CreatedAt.UTC().Month(), event.CreatedAt.UTC().Day(), 0, 0, 0, 0, time.UTC)
		if firstDay.IsZero() || day.Before(firstDay) {
			firstDay = day
		}
		if lastDay.IsZero() || day.After(lastDay) {
			lastDay = day
		}

		sourceType := strings.TrimSpace(event.SourceType)
		if sourceType == "" {
			_, sourceType, _ = s.resolveAPIUsageSource(ctx, event.Model, event.Source)
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
	if firstDay.IsZero() || lastDay.IsZero() {
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

func apiUsagePeriod(r *http.Request) (string, *time.Time) {
	period := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("period")))
	now := time.Now().UTC()
	switch period {
	case "week":
		since := now.AddDate(0, 0, -7)
		return period, &since
	case "month":
		since := now.AddDate(0, -1, 0)
		return period, &since
	case "year":
		since := now.AddDate(-1, 0, 0)
		return period, &since
	case "all", "":
		return "all", nil
	default:
		return "all", nil
	}
}

func (s *Server) apiUsageRow(ctx context.Context, record config.APIUsageRecord) api.APIUsageRow {
	source := strings.TrimSpace(record.Source)
	sourceType := strings.TrimSpace(record.SourceType)
	sourceName := strings.TrimSpace(record.SourceName)
	if sourceType == "" || source == "" {
		source, sourceType, sourceName = s.resolveAPIUsageSource(ctx, record.Model, source)
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
