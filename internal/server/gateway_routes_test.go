package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/observability"
)

func gatewayFixture(t *testing.T) (*Server, string) {
	t.Helper()
	s := newTestServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if body["model"] != "upstream-model" {
			t.Errorf("gateway sent wrong upstream model: %v", body["model"])
		}
		if stream, _ := body["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/embeddings") {
			_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":2,"total_tokens":2}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	t.Cleanup(upstream.Close)
	if err := config.SaveProviders([]config.ThirdPartyProvider{{ID: "upstream", Name: "Upstream", BaseURL: upstream.URL, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if err := config.ReplaceProviderModelAllowlist("upstream", []string{"raw-only", "published"}); err != nil {
		t.Fatal(err)
	}
	member := []config.ProviderPoolMember{{ID: "member", Source: "provider:upstream", Model: "upstream-model"}}
	if err := config.SaveProviderPools([]config.ProviderPool{
		{ID: "service", Name: "Published", Model: "published", Enabled: true, Policy: "fixed", Category: "chat", Members: member},
		{ID: "embed", Name: "Embedding", Model: "vector", Enabled: true, Policy: "fixed", Category: "embedding", Members: member},
		{ID: "disabled", Name: "Disabled", Model: "disabled", Enabled: false, Members: member},
		{ID: "missing", Name: "Missing", Model: "missing", Enabled: true, Members: []config.ProviderPoolMember{{ID: "gone", Source: "provider:gone", Model: "gone"}}},
	}); err != nil {
		t.Fatal(err)
	}
	_, key, err := s.apiKeys.Create("gateway-client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.apiKeys.SetAuthEnabled(true); err != nil {
		t.Fatal(err)
	}
	return s, key
}

func gatewayRequest(handler http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.10:1234"
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestGatewayListsOnlyPublishedServicesAndProtectsEveryProtocol(t *testing.T) {
	s, key := gatewayFixture(t)
	for name, h := range map[string]http.Handler{"main": s.routes(), "external": s.externalAPIRoutes(), "public": s.publicClusterInferenceRoutes()} {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{"models", "chat/completions", "embeddings", "responses", "messages", "messages/count_tokens", "decisions", "images/generations", "images/edits", "audio/transcriptions", "audio/speech"} {
				method := "POST"
				if path == "models" {
					method = "GET"
				}
				w := gatewayRequest(h, method, "/ai-gateway/v1/"+path, `{"model":"published"}`, "")
				if w.Code != 401 {
					t.Fatalf("unprotected %s: %d %s", path, w.Code, w.Body.String())
				}
			}
			w := gatewayRequest(h, "GET", "/ai-gateway/v1/models", "", key)
			if w.Code != 200 {
				t.Fatalf("model discovery: %d %s", w.Code, w.Body.String())
			}
			var list struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			if len(list.Data) != 2 || list.Data[0].ID != "published" || list.Data[1].ID != "vector" {
				t.Fatalf("unpublished models leaked: %s", w.Body.String())
			}
			for _, body := range []string{`{"model":"raw-only"}`, `{"model":"disabled"}`, `{"model":"published","source":"provider:upstream"}`} {
				w = gatewayRequest(h, "POST", "/ai-gateway/v1/chat/completions", body, key)
				if w.Code != 400 && w.Code != 404 {
					t.Fatalf("source escape: %d %s", w.Code, w.Body.String())
				}
			}
			w = gatewayRequest(h, "POST", "/ai-gateway/v1/embeddings", `{"model":"published","input":"hello"}`, key)
			if w.Code != 400 {
				t.Fatalf("mismatched capability: %d", w.Code)
			}
			w = gatewayRequest(h, "POST", "/ai-gateway/v1/embeddings", `{"model":"vector","input":"hello"}`, key)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "embedding") {
				t.Fatalf("embedding failed: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestGatewayStreamingAndUsageResolveServiceDespiteDirectNameCollision(t *testing.T) {
	s, key := gatewayFixture(t)
	for _, stream := range []bool{false, true} {
		body, _ := json.Marshal(map[string]any{"model": "published", "stream": stream, "messages": []map[string]string{{"role": "user", "content": "hello"}}})
		w := gatewayRequest(s.routes(), "POST", "/ai-gateway/v1/chat/completions", string(body), key)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "OK") {
			t.Fatalf("service routing failed: %d %s", w.Code, w.Body.String())
		}
		if stream && !strings.Contains(w.Body.String(), "[DONE]") {
			t.Fatal("stream completion marker missing")
		}
	}
	s.observabilityMu.RLock()
	page, err := s.observability.ListRequests(t.Context(), observability.RequestFilter{})
	s.observabilityMu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) < 2 || page.Items[0].PoolID != "service" || page.Items[0].APIKeyName != "gateway-client" {
		t.Fatalf("gateway usage not attributed: %#v", page.Items)
	}
	// Legacy and explicit-source route contracts remain mounted.
	for _, path := range []string{"/providers/upstream/v1/chat/completions", "/ai-gateway/providers/upstream/v1/chat/completions"} {
		w := gatewayRequest(s.routes(), "POST", path, `{"model":"upstream-model","messages":[{"role":"user","content":"hello"}]}`, key)
		if w.Code != 200 {
			t.Fatalf("direct route %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestGatewayPublicResponsesKeepStorageRestrictions(t *testing.T) {
	s, key := gatewayFixture(t)
	for _, body := range []string{`{"model":"published","input":"x","background":true}`, `{"model":"published","input":"x","previous_response_id":"private"}`} {
		w := gatewayRequest(s.publicClusterInferenceRoutes(), "POST", "/ai-gateway/v1/responses", body, key)
		if w.Code != 400 {
			t.Fatalf("public response storage allowed: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestGatewayRouteFamilyIsDocumented(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "openapi", "local-api.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	for path, methods := range map[string][]string{
		"/ai-gateway/v1/models": {"get"}, "/ai-gateway/v1/chat/completions": {"post"}, "/ai-gateway/v1/embeddings": {"post"},
		"/ai-gateway/v1/responses": {"get", "post"}, "/ai-gateway/v1/messages": {"post"}, "/ai-gateway/v1/messages/count_tokens": {"post"},
		"/ai-gateway/v1/decisions": {"post"}, "/ai-gateway/v1/images/generations": {"post"}, "/ai-gateway/v1/images/edits": {"post"},
		"/ai-gateway/v1/audio/transcriptions": {"post"}, "/ai-gateway/v1/audio/speech": {"post"},
		"/ai-gateway/realtime/calls": {"post"}, "/ai-gateway/realtime/calls/{call_id}": {"delete"},
		"/ai-gateway/providers/{providerID}/v1/audio/speech": {"post"},
	} {
		for _, method := range methods {
			if _, ok := spec.Paths[path][method]; !ok {
				t.Errorf("missing gateway operation: %s %s", method, path)
			}
		}
	}
	for path, operations := range spec.Paths {
		if !strings.HasPrefix(path, "/providers/") {
			continue
		}
		for method := range operations {
			if _, ok := spec.Paths[gatewayPrefix+path][method]; !ok {
				t.Errorf("missing explicit gateway operation: %s %s", method, path)
			}
		}
	}
}
