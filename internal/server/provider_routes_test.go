package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestProviderRouteSourceReservedIDs(t *testing.T) {
	for input, want := range map[string]string{
		"local":  "local",
		"LOCAL":  "local",
		"cloud":  "cloud",
		"csghub": "cloud",
	} {
		got, err := providerRouteSource(input)
		if err != nil {
			t.Fatalf("providerRouteSource(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("providerRouteSource(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEffectiveRequestSourceUsesProviderRoute(t *testing.T) {
	ctx := context.WithValue(context.Background(), providerRouteSourceContextKey{}, "cloud")
	if got, err := effectiveRequestSource(ctx, ""); err != nil || got != "cloud" {
		t.Fatalf("effectiveRequestSource(empty) = %q, %v", got, err)
	}
	if _, err := effectiveRequestSource(ctx, "local"); err == nil {
		t.Fatal("effectiveRequestSource accepted conflicting body source")
	}
}

func TestProviderScopedBaseURL(t *testing.T) {
	got, err := providerScopedBaseURL("http://localhost:11435/", "cloud")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://localhost:11435/providers/csghub" {
		t.Fatalf("providerScopedBaseURL = %q", got)
	}
}

func TestProviderScopedBaseURLUsesUnscopedRouteForPool(t *testing.T) {
	newTestServer(t)
	if err := config.SaveProviderPools([]config.ProviderPool{{
		ID:      "code",
		Name:    "Code",
		Model:   "code-pool",
		Enabled: true,
		Members: []config.ProviderPoolMember{{ID: "cloud", Source: "cloud", Model: "upstream"}},
	}}); err != nil {
		t.Fatal(err)
	}

	got, err := providerScopedBaseURL("http://localhost:11435/", "pool:code")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://localhost:11435" {
		t.Fatalf("providerScopedBaseURL = %q, want unscoped local URL", got)
	}
}

func TestFilterModelsByProviderRoute(t *testing.T) {
	models := []api.ModelInfo{
		{Model: "shared", Source: "local"},
		{Model: "shared", Source: "cloud"},
	}
	got := filterModelsByProviderRoute(models, "cloud")
	if len(got) != 1 || got[0].Source != "cloud" {
		t.Fatalf("filterModelsByProviderRoute = %#v", got)
	}
}

func TestProviderRouteUnknownEndpointReturnsJSON404(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/providers/local/v1/unknown", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
}

func TestProviderRouteLegacyAuthPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/providers/cloud/v1/messages", nil)
	if !requiresRemoteAPIAuth(req) {
		t.Fatal("provider-scoped messages route should require remote API auth")
	}
}

func TestProviderRouteRejectsConflictingRequestSource(t *testing.T) {
	s := newTestServer(t)
	body := []byte(`{"model":"shared","source":"cloud","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/providers/local/v1/messages", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestProviderNamesAreUniqueCaseInsensitively(t *testing.T) {
	providers := []config.ThirdPartyProvider{{ID: "one", Name: "DeepSeek"}}
	if !providerNameExists(providers, " deepseek ", "") {
		t.Fatal("provider name uniqueness should ignore case and surrounding spaces")
	}
	if providerNameExists(providers, "DeepSeek", "one") {
		t.Fatal("provider update should exclude its own stable ID")
	}
}
