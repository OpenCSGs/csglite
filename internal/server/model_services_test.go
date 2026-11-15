package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestModelServiceSameNamesAndProviderLifecycle(t *testing.T) {
	s := newTestServer(t)
	received := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		received = append(received, body.Model)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer upstream.Close()
	providers := []config.ThirdPartyProvider{{ID: "a", Name: "First", BaseURL: upstream.URL, Enabled: false}, {ID: "b", Name: "Second", BaseURL: upstream.URL, Enabled: true}}
	if err := config.SaveProviders(providers); err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRequest("POST", "/api/provider-pools", strings.NewReader(`{"name":"Shared Qwen","model":"served-qwen","policy":"failover","category":"chat","members":[{"id":"a","source":"provider:a","model":"qwen"},{"id":"b","source":"provider:b","model":"qwen"}]}`))
	w := httptest.NewRecorder()
	s.handleProviderPoolCreate(w, create)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var service api.ProviderPool
	if err := json.Unmarshal(w.Body.Bytes(), &service); err != nil {
		t.Fatal(err)
	}
	if service.SourceStates["a"] != "disabled" || service.SourceStates["b"] != "configured" {
		t.Fatalf("source state: %v", service.SourceStates)
	}
	// A published alias resolves without discovery or a cloud connection. Same
	// upstream names stay distinct, and disabled Provider A is never called.
	eng, err := s.getChatEngine(t.Context(), service.Model, "", 0, 0, -1, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := eng.(inference.ChatCompletionProxier).ChatCompletion(t.Context(), map[string]any{"model": service.Model})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(received) != 1 || received[0] != "qwen" || resp.Header.Get(providerPoolMemberSourceHeader) != "provider:b" {
		t.Fatalf("wrong upstream: %v %v", received, resp.Header)
	}
	// Display-name changes do not change the stable Provider ID binding.
	providers[1].Name = "Renamed"
	providers[1].Enabled = false
	if err := config.SaveProviders(providers); err != nil {
		t.Fatal(err)
	}
	eng, err = s.getChatEngine(t.Context(), service.Model, "", 0, 0, -1, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.(inference.ChatCompletionProxier).ChatCompletion(t.Context(), map[string]any{"model": service.Model})
	if inference.HTTPStatusCode(err) != 503 {
		t.Fatalf("all sources disabled: %v", err)
	}
	// Deleted providers remain visible and removable in an existing service.
	if err := config.SaveProviders(nil); err != nil {
		t.Fatal(err)
	}
	update := httptest.NewRequest("PUT", "/api/provider-pools/"+service.ID, strings.NewReader(`{"enabled":false}`))
	update.SetPathValue("id", service.ID)
	w = httptest.NewRecorder()
	s.handleProviderPoolUpdate(w, update)
	if w.Code != 200 {
		t.Fatalf("orphan binding blocked disabling service: %d %s", w.Code, w.Body.String())
	}
	if len(config.GetProviderPools()[0].Members) != 2 {
		t.Fatal("disable removed source bindings")
	}
}

func TestModelServiceRejectsInvalidRoutingAndDuplicateBindings(t *testing.T) {
	s := newTestServer(t)
	for _, body := range []string{
		`{"name":"x","model":"served","policy":"fixed","members":[{"id":"a","source":"local","model":"m"},{"id":"b","source":"cloud","model":"n"}]}`,
		`{"name":"x","model":"served","members":[{"id":"a","source":"local","model":"m"},{"id":"b","source":"local","model":"m"}]}`,
		`{"name":"x","model":"served","category":"audio","members":[{"id":"a","source":"local","model":"m"}]}`,
	} {
		w := httptest.NewRecorder()
		s.handleProviderPoolCreate(w, httptest.NewRequest("POST", "/api/provider-pools", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("accepted invalid service: %s %d", body, w.Code)
		}
	}
	if len(config.GetProviderPools()) != 0 {
		t.Fatal("invalid service persisted")
	}
}

func TestSameModelIDAcrossProvidersAndPublishedService(t *testing.T) {
	s, key := gatewayFixture(t)
	providers := config.GetProviders()
	disabled := providers[0]
	disabled.ID, disabled.Name, disabled.Enabled = "other", "Other", false
	if err := config.SaveProviders(append(providers, disabled)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"upstream", "other", cloudProviderRouteID} {
		if err := config.ReplaceProviderModelAllowlist(id, []string{"upstream-model"}); err != nil {
			t.Fatal(err)
		}
	}
	payload := `{"name":"Same ID service","model":"upstream-model","policy":"failover","category":"chat","members":[{"id":"disabled","source":"provider:other","model":"upstream-model"},{"id":"active","source":"provider:upstream","model":"upstream-model"}]}`
	w := httptest.NewRecorder()
	s.handleProviderPoolCreate(w, httptest.NewRequest("POST", "/api/provider-pools", strings.NewReader(payload)))
	if w.Code != 201 {
		t.Fatalf("same ID rejected: %d %s", w.Code, w.Body)
	}
	var pool api.ProviderPool
	if err := json.Unmarshal(w.Body.Bytes(), &pool); err != nil {
		t.Fatal(err)
	}
	for name, handler := range map[string]http.Handler{"main": s.routes(), "external": s.externalAPIRoutes(), "public": s.publicClusterInferenceRoutes()} {
		for _, stream := range []bool{false, true} {
			body, _ := json.Marshal(map[string]any{"model": "upstream-model", "stream": stream, "messages": []map[string]string{{"role": "user", "content": "hello"}}})
			result := gatewayRequest(handler, "POST", "/ai-gateway/v1/chat/completions", string(body), key)
			if result.Code != 200 || !strings.Contains(result.Body.String(), "OK") || result.Header().Get(providerPoolMemberSourceHeader) != "provider:upstream" {
				t.Fatalf("%s stream=%v routed to wrong source: %d %s", name, stream, result.Code, result.Body)
			}
		}
	}
	// Updates may retain the shared ID. Embeddings must not recurse into the pool.
	update := httptest.NewRequest("PUT", "/api/provider-pools/"+pool.ID, strings.NewReader(`{"category":"embedding","model":"upstream-model"}`))
	update.SetPathValue("id", pool.ID)
	w = httptest.NewRecorder()
	s.handleProviderPoolUpdate(w, update)
	if w.Code != 200 {
		t.Fatalf("same ID update rejected: %d %s", w.Code, w.Body)
	}
	result := gatewayRequest(s.routes(), "POST", "/ai-gateway/v1/embeddings", `{"model":"upstream-model","input":"hello"}`, key)
	if result.Code != 200 || !strings.Contains(result.Body.String(), "embedding") {
		t.Fatalf("same ID embeddings failed: %d %s", result.Code, result.Body)
	}
	// Service IDs remain unique, including case variants; this cannot silently
	// replace an existing service or merge two independently configured services.
	for _, id := range []string{"upstream-model", "UPSTREAM-MODEL"} {
		duplicate := strings.ReplaceAll(strings.ReplaceAll(payload, "Same ID service", "Another service"), "upstream-model", id)
		w = httptest.NewRecorder()
		s.handleProviderPoolCreate(w, httptest.NewRequest("POST", "/api/provider-pools", strings.NewReader(duplicate)))
		if w.Code != 400 || !strings.Contains(w.Body.String(), "model service ID already exists") {
			t.Fatalf("duplicate public ID accepted: %d %s", w.Code, w.Body)
		}
	}
}

func TestSameModelIDLocalServiceDoesNotRecurse(t *testing.T) {
	s := newTestServer(t)
	storageID := "Qwen/shared-local"
	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{Namespace: "Qwen", Name: "shared-local", Format: model.FormatGGUF, Files: []string{"model.gguf"}, DownloadedAt: time.Unix(100, 0)})
	modelID := s.localInferenceModelID(storageID)
	fake := &fakeChatCompletionEngine{resp: api.OpenAIChatResponse{Choices: []api.OpenAIChoice{{Message: &api.Message{Role: "assistant", Content: "local OK"}}}}}
	s.engines[engineCacheKey(storageID, engineModeChat)] = &managedEngine{engine: fake, numParallel: 2, nGPULayers: -1, lastUsed: time.Now(), keepAlive: -1}
	body, _ := json.Marshal(map[string]any{"name": "Local shared ID", "model": modelID, "policy": "fixed", "category": "chat", "members": []map[string]string{{"id": "local", "source": "local", "model": modelID}}})
	w := httptest.NewRecorder()
	s.handleProviderPoolCreate(w, httptest.NewRequest("POST", "/api/provider-pools", strings.NewReader(string(body))))
	if w.Code != 201 {
		t.Fatalf("local ID rejected: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/ai-gateway/v1/chat/completions", "/providers/local/v1/chat/completions", "/v1/chat/completions"} {
		reqBody, _ := json.Marshal(map[string]any{"model": modelID, "messages": []map[string]string{{"role": "user", "content": "hello"}}})
		request := httptest.NewRequest("POST", path, strings.NewReader(string(reqBody)))
		request.RemoteAddr = "127.0.0.1:1234"
		w = httptest.NewRecorder()
		s.routes().ServeHTTP(w, request)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "local OK") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		if path == "/ai-gateway/v1/chat/completions" && w.Header().Get(providerPoolMemberSourceHeader) != "local" {
			t.Fatal("unified API bypassed service")
		}
		if path != "/ai-gateway/v1/chat/completions" && w.Header().Get(providerPoolMemberSourceHeader) != "" {
			t.Fatal("legacy direct API unexpectedly resolved service")
		}
	}
}
