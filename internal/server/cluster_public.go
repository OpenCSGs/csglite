package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/opencsgs/csglite/ee/cluster"
)

func validatePublicManagementListener(mode, addr string) error {
	if (mode == "gateway" || mode == "worker") && !isDesktopLoopbackHost(addr) {
		return fmt.Errorf("public cluster management must bind loopback; use --listen 127.0.0.1:11435 (the gateway has its own public listener)")
	}
	return nil
}

// The public gateway is a separate, authenticated inference-only surface.
// It never uses the main API's loopback authentication exemption.
func (s *Server) publicClusterInferenceRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		models := []map[string]any{}
		if s.cluster != nil {
			for _, cm := range s.cluster.Models(r.Context()) {
				if len(s.cluster.Explain(r.Context(), cm.ID, 0, 0, "").Order) > 0 {
					models = append(models, map[string]any{"id": cm.ID, "object": "model", "created": int64(0), "owned_by": "cluster"})
				}
			}
		}
		writeJSON(w, 200, map[string]any{"object": "list", "data": models})
	})
	mux.HandleFunc("POST /v1/chat/completions", s.handleOpenAIChatCompletions)
	mux.HandleFunc("POST /v1/embeddings", s.handleOpenAIEmbeddings)
	mux.HandleFunc("POST /v1/responses", s.handleOpenAIResponses)
	mux.HandleFunc("POST /v1/messages", s.handleAnthropicMessages)
	mux.HandleFunc("POST /v1/messages/count_tokens", s.handleAnthropicCountTokens)
	mux.HandleFunc("POST /api/chat", s.handleChat)
	mux.HandleFunc("POST /api/generate", s.handleGenerate)
	// A bounded admission semaphore prevents unbounded queues at the gateway.
	slots := make(chan struct{}, 32)
	business := s.observabilityMiddleware(providerPoolUsageMiddleware(mux))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.apiKeys == nil {
			writeError(w, 401, "valid API key required")
			return
		}
		record, ok, err := s.apiKeys.Validate(requestAPIKey(r))
		if err != nil {
			writeError(w, 500, "failed to validate API key")
			return
		}
		if !ok {
			writeError(w, 401, "valid API key required")
			return
		}
		// Check the whitelist before parsing a body. Management is never mounted.
		_, pattern := mux.Handler(r)
		if pattern == "" || r.URL.RawPath != "" || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "1")
			writeError(w, 429, "public gateway is at capacity")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		ctx = context.WithValue(ctx, apiKeyContextKey{}, record)
		r = r.Clone(ctx)
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
			data, err := io.ReadAll(r.Body)
			if err != nil {
				writeError(w, 413, "request body too large or unreadable")
				return
			}
			var body map[string]json.RawMessage
			if json.Unmarshal(data, &body) != nil || body == nil {
				writeError(w, 400, "a JSON object is required")
				return
			}
			var source string
			if value, exists := body["source"]; exists && json.Unmarshal(value, &source) != nil {
				writeError(w, 400, "invalid model source")
				return
			}
			if source == "" {
				source = cluster.SourceCluster
			}
			if source != "local" && !cluster.IsClusterSource(source) {
				writeError(w, 400, "public gateway accepts local, cluster or node sources")
				return
			}
			if s.cluster != nil && (source == "local" || cluster.NodeUUIDFromSource(source) == s.cluster.Identity().UUID) && !s.cluster.Store().Settings().AcceptWork {
				writeError(w, 403, "gateway local inference is disabled")
				return
			}
			body["source"], _ = json.Marshal(source)
			if r.URL.Path == "/v1/responses" {
				var background bool
				if v, exists := body["background"]; exists && (json.Unmarshal(v, &background) != nil || background) {
					writeError(w, 400, "background responses are not supported")
					return
				}
				if _, exists := body["previous_response_id"]; exists {
					writeError(w, 400, "stored response continuation is not supported")
					return
				}
			}
			data, _ = json.Marshal(body)
			r.Body = io.NopCloser(bytes.NewReader(data))
			r.ContentLength = int64(len(data))

		}
		business.ServeHTTP(w, r)
	})
	return correlationMiddleware(handler)
}
