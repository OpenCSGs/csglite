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

func TestProviderScopedBaseURLSupportsProviderPool(t *testing.T) {
	if err := config.SaveProviderPools([]config.ProviderPool{{
		ID: "pool-one", Name: "Pool One", Model: "public-model", Enabled: true,
		Members: []config.ProviderPoolMember{{ID: "local", Source: "local", Model: "member-model"}},
	}}); err != nil {
		t.Fatal(err)
	}
	got, err := providerScopedBaseURL("http://localhost:11435/", poolSource("pool-one"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://localhost:11435/providers/pool-one" {
		t.Fatalf("providerScopedBaseURL = %q", got)
	}
}

func TestProviderScopedBaseURLRejectsDisabledProviderPool(t *testing.T) {
	if err := config.SaveProviderPools([]config.ProviderPool{{
		ID: "pool-one", Name: "Pool One", Model: "public-model", Enabled: false,
		Members: []config.ProviderPoolMember{{ID: "local", Source: "local", Model: "member-model"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := providerScopedBaseURL("http://localhost:11435/", poolSource("pool-one")); err == nil {
		t.Fatal("providerScopedBaseURL accepted a disabled provider pool")
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

func TestProviderRouteFiltersPoolModels(t *testing.T) {
	s := newTestServer(t)
	if err := config.SaveProviderPools([]config.ProviderPool{{
		ID: "pool-one", Name: "Pool One", Model: "public-model", Enabled: true,
		Members: []config.ProviderPoolMember{{ID: "local", Source: "local", Model: "member-model"}},
	}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/providers/pool-one/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"id":"public-model"`)) {
		t.Fatalf("pool model missing from response: %s", w.Body.String())
	}
}

func TestProviderRouteRejectsDisabledPool(t *testing.T) {
	s := newTestServer(t)
	if err := config.SaveProviderPools([]config.ProviderPool{{
		ID: "pool-one", Name: "Pool One", Model: "public-model", Enabled: false,
		Members: []config.ProviderPoolMember{{ID: "local", Source: "local", Model: "member-model"}},
	}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/providers/pool-one/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestProviderRouteRejectsUnsupportedPoolEndpoint(t *testing.T) {
	s := newTestServer(t)
	if err := config.SaveProviderPools([]config.ProviderPool{{
		ID: "pool-one", Name: "Pool One", Model: "public-model", Enabled: true,
		Members: []config.ProviderPoolMember{{ID: "local", Source: "local", Model: "member-model"}},
	}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/providers/pool-one/v1/images/generations", bytes.NewBufferString(`{"model":"public-model","prompt":"hello"}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body=%s", w.Code, w.Body.String())
	}
}

func TestProviderPoolRouteRequiresPublicModel(t *testing.T) {
	if err := config.SaveProviderPools([]config.ProviderPool{{
		ID: "pool-one", Name: "Pool One", Model: "public-model", Enabled: true,
		Members: []config.ProviderPoolMember{{ID: "local", Source: "local", Model: "member-model"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := providerPoolForRequest("other-model", poolSource("pool-one")); ok {
		t.Fatal("providerPoolForRequest accepted a model other than the pool's public model")
	}
}

func TestProviderPoolRouteResolvesMemberSourceOutsideRouteScope(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("upstream path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	s := newTestServer(t)
	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID: "member-provider", Name: "Member Provider", BaseURL: upstream.URL + "/v1", APIKey: "test-key", Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveProviderPools([]config.ProviderPool{{
		ID: "pool-one", Name: "Pool One", Model: "public-model", Enabled: true,
		Members: []config.ProviderPoolMember{{
			ID: "member", Source: providerSource("member-provider"), Model: "upstream-model",
		}},
	}}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/providers/pool-one/v1/chat/completions", bytes.NewBufferString(`{
		"model":"public-model",
		"messages":[{"role":"user","content":"hello"}],
		"stream":false
	}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !upstreamCalled {
		t.Fatal("provider pool member upstream was not called")
	}
}

func TestProviderRouteLegacyAuthPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/providers/cloud/v1/messages", nil)
	if !requiresRemoteAPIAuth(req) {
		t.Fatal("provider-scoped messages route should require remote API auth")
	}
}

func TestProviderPoolRouteIsObserved(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/providers/pool-one/v1/chat/completions", nil)
	if !isObservedTextGenerationRequest(req) {
		t.Fatal("provider pool chat route should be observed")
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
