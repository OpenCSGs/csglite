package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func TestRemoteAPIAuthDefaultDisabled(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	req.RemoteAddr = "192.168.1.20:5555"
	w := httptest.NewRecorder()

	s.routes().ServeHTTP(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s, want request to pass auth when disabled by default", w.Code, w.Body.String())
	}
}

func TestRemoteAPIAuthOnlyProtectsInferenceRoutesWhenEnabled(t *testing.T) {
	s := newTestServer(t)
	if _, err := s.apiKeys.SetAuthEnabled(true); err != nil {
		t.Fatalf("enable auth: %v", err)
	}

	settingsReq := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	settingsReq.RemoteAddr = "192.168.1.20:5555"
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, settingsReq)
	if w.Code != http.StatusOK {
		t.Fatalf("settings status without key = %d body=%s, want 200", w.Code, w.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	req.RemoteAddr = "192.168.1.20:5555"
	w = httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("remote inference status without key = %d body=%s, want 401", w.Code, w.Body.String())
	}

	localReq := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	localReq.RemoteAddr = "127.0.0.1:5555"
	w = httptest.NewRecorder()
	s.routes().ServeHTTP(w, localReq)
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("local inference status without key = %d body=%s, want loopback bypass", w.Code, w.Body.String())
	}
}

func TestRemoteAPIAuthProtectsAnthropicAlias(t *testing.T) {
	s := newTestServer(t)
	if _, err := s.apiKeys.SetAuthEnabled(true); err != nil {
		t.Fatalf("enable auth: %v", err)
	}

	for _, path := range []string{"/anthropic/messages", "/anthropic/v1/messages"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.RemoteAddr = "192.168.1.20:5555"
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s status without key = %d body=%s, want 401", path, w.Code, w.Body.String())
		}
	}
}

func TestRemoteAPIAuthAcceptsBearerAndXAPIKey(t *testing.T) {
	s := newTestServer(t)
	_, plain, err := s.apiKeys.Create("client")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if _, err := s.apiKeys.SetAuthEnabled(true); err != nil {
		t.Fatalf("enable auth: %v", err)
	}

	for name, header := range map[string]func(*http.Request){
		"bearer":    func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+plain) },
		"x-api-key": func(req *http.Request) { req.Header.Set("x-api-key", plain) },
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			req.RemoteAddr = "192.168.1.20:5555"
			header(req)
			w := httptest.NewRecorder()
			s.routes().ServeHTTP(w, req)
			if w.Code == http.StatusUnauthorized {
				t.Fatalf("status = %d body=%s, want API key to pass auth", w.Code, w.Body.String())
			}
		})
	}
}

func TestAPIKeyCreateDoesNotExposeHash(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/api-keys", strings.NewReader(`{"name":"client"}`))
	w := httptest.NewRecorder()

	s.handleAPIKeyCreate(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s, want 201", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "hash") {
		t.Fatalf("create response exposed key hash: %s", w.Body.String())
	}
	var resp api.APIKeyCreateResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.APIKey == "" || resp.Key.ID == "" || resp.Key.Prefix == "" {
		t.Fatalf("incomplete create response: %#v", resp)
	}
}

func TestAPIUsageAggregatesByKeyAndModel(t *testing.T) {
	s := newTestServer(t)
	record, _, err := s.apiKeys.Create("client")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	req = req.WithContext(context.WithValue(req.Context(), apiKeyContextKey{}, record))

	s.recordAPIUsage(req, "test/model", "local", 3, 5)
	s.recordAPIUsage(req, "test/model", "local", 2, 7)

	w := httptest.NewRecorder()
	s.handleAPIUsage(w, httptest.NewRequest(http.MethodGet, "/api/api-usage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var resp api.APIUsageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	if resp.Totals.Requests != 2 || resp.Totals.InputTokens != 5 || resp.Totals.OutputTokens != 12 || resp.Totals.TotalTokens != 17 {
		t.Fatalf("unexpected totals: %#v", resp.Totals)
	}
	if len(resp.TotalSummary.XAxis) != 1 || len(resp.TotalSummary.Series) != 3 {
		t.Fatalf("unexpected total summary shape: %#v", resp.TotalSummary)
	}
	if resp.TotalSummary.Series[0].Data[0] != 17 || resp.TotalSummary.Series[1].Data[0] != 17 || resp.TotalSummary.Series[2].Data[0] != 0 {
		t.Fatalf("unexpected total summary values: %#v", resp.TotalSummary)
	}
	if len(resp.Rows) != 1 || resp.Rows[0].APIKeyName != "client" || resp.Rows[0].Model != "test/model" {
		t.Fatalf("unexpected rows: %#v", resp.Rows)
	}
	if resp.Rows[0].SourceType != "local" {
		t.Fatalf("source_type = %q, want local", resp.Rows[0].SourceType)
	}
	if len(resp.SourceTotals) != 1 || resp.SourceTotals[0].SourceType != "local" || resp.SourceTotals[0].TotalTokens != 17 {
		t.Fatalf("unexpected source totals: %#v", resp.SourceTotals)
	}
}

func TestAPIUsageRecordsBuiltinClientWithoutAPIKey(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)

	s.recordAPIUsage(req, "provider-model", "provider:test", 4, 6)

	w := httptest.NewRecorder()
	s.handleAPIUsage(w, httptest.NewRequest(http.MethodGet, "/api/api-usage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var resp api.APIUsageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	if len(resp.Rows) != 1 {
		t.Fatalf("rows = %d, want 1: %#v", len(resp.Rows), resp.Rows)
	}
	if resp.Rows[0].APIKeyID != apiUsageBuiltinKeyID || resp.Rows[0].APIKeyName != apiUsageBuiltinKeyName {
		t.Fatalf("unexpected builtin client row: %#v", resp.Rows[0])
	}
	if resp.Rows[0].SourceType != "provider" || resp.Totals.TotalTokens != 10 {
		t.Fatalf("unexpected usage row: %#v totals=%#v", resp.Rows[0], resp.Totals)
	}
}

func TestAPIUsageSourceTotalsKeepThirdPartyProvidersSeparate(t *testing.T) {
	s := newTestServer(t)
	record, _, err := s.apiKeys.Create("client")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	for _, event := range []config.APIUsageEvent{
		{
			APIKeyID:     record.ID,
			APIKeyName:   record.Name,
			Model:        "model-a",
			Source:       "provider:a",
			SourceType:   "provider",
			SourceName:   "Provider A",
			InputTokens:  1,
			OutputTokens: 2,
		},
		{
			APIKeyID:     record.ID,
			APIKeyName:   record.Name,
			Model:        "model-b",
			Source:       "provider:b",
			SourceType:   "provider",
			SourceName:   "Provider B",
			InputTokens:  3,
			OutputTokens: 4,
		},
	} {
		if err := s.apiUsage.Add(event); err != nil {
			t.Fatalf("add usage: %v", err)
		}
	}

	w := httptest.NewRecorder()
	s.handleAPIUsage(w, httptest.NewRequest(http.MethodGet, "/api/api-usage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var resp api.APIUsageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	if len(resp.SourceTotals) != 2 {
		t.Fatalf("source totals = %#v, want two provider entries", resp.SourceTotals)
	}
	names := map[string]int64{}
	for _, total := range resp.SourceTotals {
		names[total.SourceName] = total.TotalTokens
	}
	if names["Provider A"] != 3 || names["Provider B"] != 7 {
		t.Fatalf("unexpected provider totals: %#v", resp.SourceTotals)
	}
}

func TestAPIUsageFiltersByPeriod(t *testing.T) {
	s := newTestServer(t)
	record, _, err := s.apiKeys.Create("client")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	now := time.Now().UTC()
	if err := s.apiUsage.Add(config.APIUsageEvent{
		APIKeyID:     record.ID,
		APIKeyName:   record.Name,
		Model:        "old/model",
		Source:       "cloud",
		SourceType:   "cloud",
		SourceName:   "OpenCSG",
		InputTokens:  100,
		OutputTokens: 50,
		CreatedAt:    now.AddDate(0, 0, -10),
	}); err != nil {
		t.Fatalf("add old usage: %v", err)
	}
	if err := s.apiUsage.Add(config.APIUsageEvent{
		APIKeyID:     record.ID,
		APIKeyName:   record.Name,
		Model:        "new/model",
		Source:       "provider:test",
		SourceType:   "provider",
		SourceName:   "Test Provider",
		InputTokens:  3,
		OutputTokens: 4,
		CreatedAt:    now,
	}); err != nil {
		t.Fatalf("add new usage: %v", err)
	}

	w := httptest.NewRecorder()
	s.handleAPIUsage(w, httptest.NewRequest(http.MethodGet, "/api/api-usage?period=week", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", w.Code, w.Body.String())
	}
	var resp api.APIUsageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	if resp.Period != "week" || resp.From == nil {
		t.Fatalf("period = %q from=%v, want week with from", resp.Period, resp.From)
	}
	if resp.Totals.TotalTokens != 7 || len(resp.Rows) != 1 || resp.Rows[0].Model != "new/model" {
		t.Fatalf("unexpected weekly usage: %#v", resp)
	}
	if len(resp.TotalSummary.XAxis) != 1 || resp.TotalSummary.Series[0].Data[0] != 7 || resp.TotalSummary.Series[2].Data[0] != 7 {
		t.Fatalf("unexpected weekly total summary: %#v", resp.TotalSummary)
	}
	if len(resp.SourceTotals) != 1 || resp.SourceTotals[0].SourceType != "provider" {
		t.Fatalf("unexpected weekly source totals: %#v", resp.SourceTotals)
	}
}
