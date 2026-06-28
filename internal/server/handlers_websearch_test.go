package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/websearch"
	"github.com/opencsgs/csglite/pkg/api"
)

type captureChatEngine struct {
	messages  []inference.Message
	calls     int
	responses []string
}

func (e *captureChatEngine) Generate(context.Context, string, inference.Options, inference.TokenCallback) (string, error) {
	return "", nil
}

func (e *captureChatEngine) Chat(_ context.Context, messages []inference.Message, _ inference.Options, cb inference.TokenCallback) (string, error) {
	e.messages = append([]inference.Message{}, messages...)
	e.calls++
	response := "answer"
	if len(e.responses) > 0 {
		response = e.responses[0]
		e.responses = e.responses[1:]
	}
	if cb != nil {
		cb(response)
	}
	return response, nil
}

func (e *captureChatEngine) Close() error { return nil }

func (e *captureChatEngine) ModelName() string { return "test/model" }

func webSearchRouteJSON(action, query, reason string, confidence float64) string {
	payload, err := json.Marshal(webSearchRoute{
		Action:     action,
		Query:      query,
		Reason:     reason,
		Confidence: confidence,
	})
	if err != nil {
		panic(err)
	}
	return string(payload)
}

func TestHandleChatWithWebSearchSendsEventsAndInjectsContext(t *testing.T) {
	expectedQuery := fmt.Sprintf("latest news %d", time.Now().Year())
	origSearchWeb := searchWeb
	defer func() { searchWeb = origSearchWeb }()
	searchWeb = func(_ context.Context, cfg websearch.Config, req websearch.SearchRequest) (websearch.SearchResponse, error) {
		if req.Query != expectedQuery {
			t.Fatalf("query = %q, want %q", req.Query, expectedQuery)
		}
		if len(cfg.Providers) != 0 {
			t.Fatalf("providers = %#v, want automatic order", cfg.Providers)
		}
		if !cfg.Quick {
			t.Fatal("Quick = false, want first successful provider for chat latency")
		}
		return websearch.SearchResponse{
			Query:    req.Query,
			Provider: websearch.ProviderBing,
			Results: []websearch.Result{{
				Title:   "Lite Search Result",
				URL:     "https://example.com/result",
				Snippet: "A useful snippet.",
				Engine:  websearch.ProviderBing,
			}},
		}, nil
	}

	s := newTestServer(t)
	s.cfg.WebSearch = config.WebSearchConfig{
		Enabled:        true,
		MaxResults:     3,
		SafeSearch:     1,
		TimeoutSeconds: 5,
	}
	engine := &captureChatEngine{}
	s.engines["test/model"] = &managedEngine{engine: engine, lastUsed: time.Now(), keepAlive: DefaultKeepAlive}

	body := `{"model":"test/model","source":"local","messages":[{"role":"user","content":"latest news"}],"stream":true,"web_search":{"enabled":true}}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-CSGHUB-Stream", "sse")
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	respBody := w.Body.String()
	for _, want := range []string{`"search_route"`, `"action":"search"`, fmt.Sprintf(`"searching":"%s"`, expectedQuery), `"search_results"`, "Lite Search Result", "answer"} {
		if !strings.Contains(respBody, want) {
			t.Fatalf("response body missing %q:\n%s", want, respBody)
		}
	}
	if len(engine.messages) < 2 {
		t.Fatalf("engine messages = %#v, want web context and user message", engine.messages)
	}
	contextText, ok := engine.messages[0].Content.(string)
	if !ok || !strings.Contains(contextText, "Lite Search Result") || !strings.Contains(contextText, "https://example.com/result") {
		t.Fatalf("web search context not injected: %#v", engine.messages)
	}
	if !strings.Contains(contextText, "Current date:") {
		t.Fatalf("current date context not injected: %q", contextText)
	}
	if !strings.Contains(contextText, "readable Markdown") {
		t.Fatalf("answer style context not injected: %q", contextText)
	}
}

func TestHandleChatInjectsCurrentDateWithoutWebSearch(t *testing.T) {
	s := newTestServer(t)
	engine := &captureChatEngine{}
	s.engines["test/model"] = &managedEngine{engine: engine, lastUsed: time.Now(), keepAlive: DefaultKeepAlive}

	body := `{"model":"test/model","source":"local","messages":[{"role":"user","content":"今年是哪一年"}],"stream":false,"web_search":{"enabled":false}}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if len(engine.messages) < 2 {
		t.Fatalf("engine messages = %#v, want current date and user message", engine.messages)
	}
	systemText, ok := engine.messages[0].Content.(string)
	if !ok || !strings.Contains(systemText, "当前日期：") || !strings.Contains(systemText, time.Now().Format("2006")) {
		t.Fatalf("current date system message missing: %#v", engine.messages)
	}
	if !strings.Contains(systemText, "不要暴露内部推理") {
		t.Fatalf("answer style context missing: %q", systemText)
	}
}

func TestHandleChatWithWebSearchAlwaysSearches(t *testing.T) {
	origSearchWeb := searchWeb
	defer func() { searchWeb = origSearchWeb }()
	searchWeb = func(context.Context, websearch.Config, websearch.SearchRequest) (websearch.SearchResponse, error) {
		return websearch.SearchResponse{
			Query:    "解释一下 Go slice",
			Provider: websearch.ProviderBing,
			Results: []websearch.Result{{
				Title:   "Go slices",
				URL:     "https://example.com/go-slices",
				Snippet: "Slice documentation.",
				Engine:  websearch.ProviderBing,
			}},
		}, nil
	}

	s := newTestServer(t)
	s.cfg.WebSearch = config.WebSearchConfig{Enabled: true, MaxResults: 3, SafeSearch: 1, TimeoutSeconds: 5}
	engine := &captureChatEngine{responses: []string{
		webSearchRouteJSON(webSearchActionSearch, "Go slice 解释", "编程问题也可搜索文档", 0.7),
		"answer",
	}}
	s.engines["test/model"] = &managedEngine{engine: engine, lastUsed: time.Now(), keepAlive: DefaultKeepAlive}

	body := `{"model":"test/model","source":"local","messages":[{"role":"user","content":"解释一下 Go slice"}],"stream":true,"web_search":{"enabled":true}}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-CSGHUB-Stream", "sse")
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, `"action":"search"`) || !strings.Contains(respBody, `"search_results"`) {
		t.Fatalf("response missing search events:\n%s", respBody)
	}
}

func TestHandleChatSkipsWebSearchWhenRoutedToSkip(t *testing.T) {
	searchCalled := false
	origSearchWeb := searchWeb
	defer func() { searchWeb = origSearchWeb }()
	searchWeb = func(context.Context, websearch.Config, websearch.SearchRequest) (websearch.SearchResponse, error) {
		searchCalled = true
		return websearch.SearchResponse{}, nil
	}

	s := newTestServer(t)
	s.cfg.WebSearch = config.WebSearchConfig{Enabled: true, MaxResults: 3, SafeSearch: 1, TimeoutSeconds: 5}
	engine := &captureChatEngine{responses: []string{"answer"}}
	s.engines["test/model"] = &managedEngine{engine: engine, lastUsed: time.Now(), keepAlive: DefaultKeepAlive}

	body := `{"model":"test/model","source":"local","messages":[{"role":"user","content":"你好"}],"stream":true,"web_search":{"enabled":true}}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-CSGHUB-Stream", "sse")
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	respBody := w.Body.String()
	if strings.Contains(respBody, `"search_planning"`) {
		t.Fatalf("fast-path skip should not call LLM routing:\n%s", respBody)
	}
	if !strings.Contains(respBody, `"action":"skip"`) || !strings.Contains(respBody, `"search_skipped"`) {
		t.Fatalf("response missing skip events:\n%s", respBody)
	}
	if strings.Contains(respBody, `"searching"`) || strings.Contains(respBody, `"search_results"`) {
		t.Fatalf("response should not search:\n%s", respBody)
	}
	if searchCalled {
		t.Fatal("searchWeb should not be called when route action is skip")
	}
	if engine.calls != 1 {
		t.Fatalf("engine calls = %d, want answer only", engine.calls)
	}
}

func TestHandleChatSearchesBeforeAnswerWhenRoutedToSearch(t *testing.T) {
	origSearchWeb := searchWeb
	defer func() { searchWeb = origSearchWeb }()
	searchWeb = func(context.Context, websearch.Config, websearch.SearchRequest) (websearch.SearchResponse, error) {
		return websearch.SearchResponse{
			Query:    "Qwen 最新版本",
			Provider: websearch.ProviderBing,
			Results: []websearch.Result{{
				Title:   "Qwen release",
				URL:     "https://example.com/qwen",
				Snippet: "Latest Qwen release.",
				Engine:  websearch.ProviderBing,
			}},
		}, nil
	}

	s := newTestServer(t)
	s.cfg.WebSearch = config.WebSearchConfig{Enabled: true, MaxResults: 3, SafeSearch: 1, TimeoutSeconds: 5}
	engine := &captureChatEngine{responses: []string{"answer"}}
	s.engines["test/model"] = &managedEngine{engine: engine, lastUsed: time.Now(), keepAlive: DefaultKeepAlive}

	body := `{"model":"test/model","source":"local","messages":[{"role":"user","content":"查一下 Qwen 最新版本"}],"stream":true,"web_search":{"enabled":true}}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-CSGHUB-Stream", "sse")
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	respBody := w.Body.String()
	if strings.Contains(respBody, `"search_planning"`) {
		t.Fatalf("explicit lookup should use fast-path routing without LLM:\n%s", respBody)
	}
	if !strings.Contains(respBody, `"searching"`) || !strings.Contains(respBody, `"search_results"`) {
		t.Fatalf("response missing search events:\n%s", respBody)
	}
	if engine.calls != 1 {
		t.Fatalf("engine calls = %d, want answer only", engine.calls)
	}
}

func TestEnrichWebSearchQueryAddsYearForRelativeTime(t *testing.T) {
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	if got, want := enrichWebSearchQuery("今年季后赛湖人战绩如何", now), "今年季后赛湖人战绩如何 2026"; got != want {
		t.Fatalf("enrichWebSearchQuery() = %q, want %q", got, want)
	}
	if got := enrichWebSearchQuery("2024 湖人季后赛战绩", now); got != "2024 湖人季后赛战绩" {
		t.Fatalf("explicit year query changed to %q", got)
	}
}

func TestChineseWebSearchContextUsesChineseInstructions(t *testing.T) {
	contextText := webSearchContextMessage("今年季后赛湖人战绩如何", "今年季后赛湖人战绩如何 2026", []api.WebSearchResult{
		{Title: "湖人战绩", URL: "https://example.com", Snippet: "湖人近7场季后赛1胜6负。"},
	})
	if !strings.Contains(contextText, "当前日期：") || !strings.Contains(contextText, "用户问题") || !strings.Contains(contextText, "不要暴露内部推理") {
		t.Fatalf("Chinese context missing localized instructions: %q", contextText)
	}
	if strings.Contains(contextText, "Current date:") || strings.Contains(contextText, "Answer directly") {
		t.Fatalf("Chinese context contains English instructions: %q", contextText)
	}
}

func TestWebSearchContextMergesIntoExistingSystemMessage(t *testing.T) {
	origSearchWeb := searchWeb
	defer func() { searchWeb = origSearchWeb }()
	searchWeb = func(_ context.Context, _ websearch.Config, req websearch.SearchRequest) (websearch.SearchResponse, error) {
		return websearch.SearchResponse{
			Query:    req.Query,
			Provider: websearch.ProviderBing,
			Results: []websearch.Result{{
				Title:   "Current Result",
				URL:     "https://example.com/current",
				Snippet: "Current snippet.",
				Engine:  websearch.ProviderBing,
			}},
		}, nil
	}

	s := newTestServer(t)
	s.cfg.WebSearch = config.WebSearchConfig{Enabled: true, MaxResults: 3, SafeSearch: 1, TimeoutSeconds: 5}
	messages := []inference.Message{
		{Role: "system", Content: "Original system prompt."},
		{Role: "user", Content: "latest model release"},
	}

	routeEngine := &captureChatEngine{responses: []string{
		webSearchRouteJSON(webSearchActionSearch, "latest model release", "needs current facts", 0.9),
	}}
	got, contextText := s.augmentChatMessagesWithWebSearch(context.Background(), api.ChatRequest{
		Messages:  []api.Message{{Role: "user", Content: "latest model release"}},
		WebSearch: &api.WebSearchOptions{Enabled: true},
	}, messages, routeEngine, nil)

	if contextText == "" {
		t.Fatal("contextText is empty")
	}
	if len(got) != len(messages) {
		t.Fatalf("len(messages) = %d, want %d: %#v", len(got), len(messages), got)
	}
	systemText, ok := got[0].Content.(string)
	if !ok {
		t.Fatalf("system content = %#v, want string", got[0].Content)
	}
	if !strings.Contains(systemText, "Original system prompt.") || !strings.Contains(systemText, "Current Result") {
		t.Fatalf("merged system prompt missing content: %q", systemText)
	}
	if got[1].Role != "user" {
		t.Fatalf("got[1].Role = %q, want user", got[1].Role)
	}
}

func TestHandleSettingsUpdateWebSearch(t *testing.T) {
	s := newTestServer(t)
	payload := api.SettingsUpdateRequest{
		WebSearch: &api.WebSearchSettings{
			Enabled:        true,
			MaxResults:     7,
			Language:       "zh-CN",
			Providers:      []string{"sogou", "quark", "bing"},
			SafeSearch:     2,
			TimeoutSeconds: 8,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(data))
	w := httptest.NewRecorder()

	s.handleSettingsUpdate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp api.SettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.WebSearch.Enabled || resp.WebSearch.MaxResults != 7 || strings.Join(resp.WebSearch.Providers, ",") != "sogou,quark,bing" {
		t.Fatalf("web_search response = %#v", resp.WebSearch)
	}
}
