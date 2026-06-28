package server

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
)

type apiKeyContextKey struct{}

func (s *Server) apiAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requiresRemoteAPIAuth(r) || isLoopbackRequest(r) || s.apiKeys == nil {
			next.ServeHTTP(w, r)
			return
		}

		state, err := s.apiKeys.State()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load API key settings")
			return
		}
		if !state.AuthEnabled {
			next.ServeHTTP(w, r)
			return
		}

		apiKey := requestAPIKey(r)
		record, ok, err := s.apiKeys.Validate(apiKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to validate API key")
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "valid API key required")
			return
		}

		ctx := context.WithValue(r.Context(), apiKeyContextKey{}, record)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func authenticatedAPIKey(r *http.Request) (config.APIKeyRecord, bool) {
	record, ok := r.Context().Value(apiKeyContextKey{}).(config.APIKeyRecord)
	return record, ok
}

func requiresRemoteAPIAuth(r *http.Request) bool {
	if r.Method == http.MethodOptions {
		return false
	}
	switch r.URL.Path {
	case "/api/chat", "/api/generate", "/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/messages/count_tokens", "/anthropic/messages", "/anthropic/messages/count_tokens", "/anthropic/v1/messages", "/anthropic/v1/messages/count_tokens":
		return true
	default:
		return false
	}
}

func requestAPIKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("x-api-key")); key != "" {
		return key
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[len("bearer "):])
	}
	return ""
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}
