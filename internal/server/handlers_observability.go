package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/observability"
	routerprofile "github.com/opencsgs/semantic-router"
	"github.com/opencsgs/csglite/pkg/api"
)

func (s *Server) handleObservabilityRequests(w http.ResponseWriter, r *http.Request) {
	filter, err := observabilityRequestFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.observabilityMu.RLock()
	defer s.observabilityMu.RUnlock()
	if s.observability == nil {
		writeError(w, http.StatusServiceUnavailable, "observability store is unavailable")
		return
	}
	page, err := s.observability.ListRequests(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load request history")
		return
	}
	resp := api.ObservabilityRequestListResponse{
		Items:  make([]api.ObservabilityRequest, 0, len(page.Items)),
		Total:  page.Total,
		Limit:  filter.Limit,
		Offset: filter.Offset,
		Summary: api.ObservabilityRequestSummary{
			Requests:       page.Summary.Requests,
			Succeeded:      page.Summary.Succeeded,
			Failed:         page.Summary.Failed,
			TotalTokens:    page.Summary.TotalTokens,
			AverageLatency: page.Summary.AverageLatency,
		},
	}
	for _, record := range page.Items {
		resp.Items = append(resp.Items, observabilityRequestResponse(record))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleObservabilityRequest(w http.ResponseWriter, r *http.Request) {
	s.observabilityMu.RLock()
	defer s.observabilityMu.RUnlock()
	if s.observability == nil {
		writeError(w, http.StatusServiceUnavailable, "observability store is unavailable")
		return
	}
	record, err := s.observability.GetRequest(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "request record not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load request record")
		return
	}
	writeJSON(w, http.StatusOK, observabilityRequestResponse(record))
}

func (s *Server) handleObservabilityTraces(w http.ResponseWriter, r *http.Request) {
	filter, err := observabilityRequestFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.observabilityMu.RLock()
	defer s.observabilityMu.RUnlock()
	if s.observability == nil {
		writeError(w, http.StatusServiceUnavailable, "observability store is unavailable")
		return
	}
	page, err := s.observability.ListTraces(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load traces")
		return
	}
	resp := api.ObservabilityTraceListResponse{
		Items:  make([]api.ObservabilityTrace, 0, len(page.Items)),
		Total:  page.Total,
		Limit:  filter.Limit,
		Offset: filter.Offset,
	}
	for _, trace := range page.Items {
		resp.Items = append(resp.Items, observabilityTraceResponse(trace))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleObservabilityTrace(w http.ResponseWriter, r *http.Request) {
	s.observabilityMu.RLock()
	defer s.observabilityMu.RUnlock()
	if s.observability == nil {
		writeError(w, http.StatusServiceUnavailable, "observability store is unavailable")
		return
	}
	trace, requests, err := s.observability.GetTrace(r.Context(), r.PathValue("traceID"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "trace not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load trace")
		return
	}
	resp := api.ObservabilityTraceDetailResponse{
		Trace:    observabilityTraceResponse(trace),
		Requests: make([]api.ObservabilityRequest, 0, len(requests)),
	}
	for _, record := range requests {
		resp.Requests = append(resp.Requests, observabilityRequestResponse(record))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleObservabilityClear(w http.ResponseWriter, r *http.Request) {
	s.routerStoreMu.RLock()
	if s.routerProfiles != nil {
		if _, err := s.routerProfiles.PurgeTraceDataBefore(r.Context(), time.Now().UTC().Add(time.Millisecond)); err != nil {
			s.routerStoreMu.RUnlock()
			if errors.Is(err, routerprofile.ErrConflict) {
				writeError(w, http.StatusConflict, err.Error())
			} else {
				writeError(w, http.StatusInternalServerError, "failed to clear router trace data")
			}
			return
		}
	}
	s.routerStoreMu.RUnlock()
	s.observabilityMu.RLock()
	defer s.observabilityMu.RUnlock()
	if s.observability == nil {
		writeError(w, http.StatusServiceUnavailable, "observability store is unavailable")
		return
	}
	if err := s.observability.DeleteAll(context.Background()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear observability data")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

func observabilityRequestFilter(r *http.Request) (observability.RequestFilter, error) {
	query := r.URL.Query()
	filter := observability.RequestFilter{
		Status:   strings.TrimSpace(query.Get("status")),
		Model:    strings.TrimSpace(query.Get("model")),
		Source:   strings.TrimSpace(query.Get("source")),
		APIKeyID: strings.TrimSpace(query.Get("api_key_id")),
		Query:    strings.TrimSpace(query.Get("q")),
		Limit:    20,
	}
	var err error
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		filter.Limit, err = strconv.Atoi(value)
		if err != nil || filter.Limit < 1 || filter.Limit > 100 {
			return filter, errors.New("limit must be between 1 and 100")
		}
	}
	if value := strings.TrimSpace(query.Get("offset")); value != "" {
		filter.Offset, err = strconv.Atoi(value)
		if err != nil || filter.Offset < 0 {
			return filter, errors.New("offset must be a non-negative integer")
		}
	}
	if filter.From, err = optionalRFC3339(query.Get("from")); err != nil {
		return filter, errors.New("from must be an RFC3339 timestamp")
	}
	if filter.To, err = optionalRFC3339(query.Get("to")); err != nil {
		return filter, errors.New("to must be an RFC3339 timestamp")
	}
	return filter, nil
}

func optionalRFC3339(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func observabilityRequestResponse(record observability.RequestRecord) api.ObservabilityRequest {
	return api.ObservabilityRequest{
		ID:                         record.ID,
		RequestID:                  record.RequestID,
		TraceID:                    record.TraceID,
		B3TraceID:                  record.B3TraceID,
		ThreadID:                   record.ThreadID,
		StartedAt:                  record.StartedAt,
		CompletedAt:                record.CompletedAt,
		Method:                     record.Method,
		Path:                       record.Path,
		Protocol:                   record.Protocol,
		Status:                     record.Status,
		StatusCode:                 record.StatusCode,
		Stream:                     record.Stream,
		Model:                      record.Model,
		Source:                     record.Source,
		SourceType:                 record.SourceType,
		SourceName:                 record.SourceName,
		APIKeyID:                   record.APIKeyID,
		APIKeyName:                 record.APIKeyName,
		PoolID:                     record.PoolID,
		PoolName:                   record.PoolName,
		PoolModel:                  record.PoolModel,
		ActualMemberID:             record.ActualMemberID,
		MemberModel:                record.MemberModel,
		PoolPolicy:                 record.PoolPolicy,
		RouterProfileID:            record.RouterProfileID,
		RouterProfileVersion:       record.RouterProfileVersion,
		RouterProfileSchemaVersion: record.RouterProfileSchemaVersion,
		RouterAlgorithm:            record.RouterAlgorithm,
		RoutingTextVersion:         record.RoutingTextVersion,
		RouterConfidence:           record.RouterConfidence,
		RouterMargin:               record.RouterMargin,
		RouterSimilarity:           record.RouterSimilarity,
		SemanticRouted:             record.SemanticRouted,
		SemanticCluster:            record.SemanticCluster,
		SemanticClusterID:          record.SemanticClusterID,
		SemanticDistance:           record.SemanticDistance,
		SemanticOOD:                record.SemanticOOD,
		SemanticFallback:           record.SemanticFallback,
		SemanticFallbackReason:     record.SemanticFallbackReason,
		PriceInputPerMillion:       record.PriceInputPerMillion,
		PriceOutputPerMillion:      record.PriceOutputPerMillion,
		EstimatedCost:              record.EstimatedCost,
		CostCurrency:               record.CostCurrency,
		CostKnown:                  record.CostKnown,
		FallbackCount:              record.FallbackCount,
		LimitedCount:               record.LimitedCount,
		InputTokens:                record.InputTokens,
		OutputTokens:               record.OutputTokens,
		TotalTokens:                max(record.InputTokens, record.CacheEligibleTokens) + record.OutputTokens,
		CacheReadInputTokens:       record.CacheReadInputTokens,
		CacheCreationTokens:        record.CacheCreationTokens,
		CacheEligibleTokens:        record.CacheEligibleTokens,
		CacheHitRate:               observabilityCacheHitRate(record),
		DurationMS:                 record.DurationMS,
		FirstTokenLatencyMS:        record.FirstTokenLatencyMS,
		ErrorMessage:               record.ErrorMessage,
		RequestBody:                record.RequestBody,
		ResponseBody:               record.ResponseBody,
		RequestBodyTruncated:       record.RequestBodyTruncated,
		ResponseBodyTruncated:      record.ResponseBodyTruncated,
	}
}

func observabilityCacheHitRate(record observability.RequestRecord) float64 {
	if record.CacheEligibleTokens <= 0 {
		return 0
	}
	return float64(record.CacheReadInputTokens) * 100 / float64(record.CacheEligibleTokens)
}

func observabilityTraceResponse(trace observability.TraceRecord) api.ObservabilityTrace {
	return api.ObservabilityTrace{
		TraceID:      trace.TraceID,
		ThreadID:     trace.ThreadID,
		StartedAt:    trace.StartedAt,
		CompletedAt:  trace.CompletedAt,
		Status:       trace.Status,
		RequestCount: trace.RequestCount,
		Models:       trace.Models,
		TotalTokens:  trace.TotalTokens,
		DurationMS:   trace.DurationMS,
	}
}
