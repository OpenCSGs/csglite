package server

import (
	"context"
	"crypto/subtle"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
)

type apiKeyContextKey struct{}

const desktopSessionCookie = "csglite_desktop_session"

func (s *Server) desktopAuthMiddleware(next http.Handler) http.Handler {
	if !s.cfg.DesktopMode {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isDesktopLoopbackHost(r.Host) || !isAllowedDesktopOrigin(r) {
			writeError(w, http.StatusForbidden, "desktop requests must use the loopback origin")
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/" {
			token := strings.TrimSpace(r.URL.Query().Get("desktop_token"))
			if secureTokenEqual(token, s.cfg.DesktopToken) && s.desktopBootstrapped.CompareAndSwap(false, true) {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Referrer-Policy", "no-referrer")
				http.SetCookie(w, &http.Cookie{
					Name:     desktopSessionCookie,
					Value:    s.cfg.DesktopSessionToken,
					Path:     "/",
					HttpOnly: true,
					SameSite: http.SameSiteStrictMode,
				})
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=/"><title>Starting csglite</title></head><body>Starting csglite…</body></html>`)
				return
			}
		}

		if secureTokenEqual(strings.TrimSpace(r.Header.Get("X-CSGLite-Desktop-Token")), s.cfg.DesktopControlToken) {
			next.ServeHTTP(w, r)
			return
		}
		if cookie, err := r.Cookie(desktopSessionCookie); err == nil && secureTokenEqual(cookie.Value, s.cfg.DesktopSessionToken) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "desktop session required")
	})
}

func secureTokenEqual(got, want string) bool {
	if got == "" || want == "" || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func isDesktopLoopbackHost(hostport string) bool {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isAllowedDesktopOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host) && isDesktopLoopbackHost(parsed.Host)
}

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
	path := providerRouteLegacyPath(r.URL.Path)
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && isReadOnlyArtifactPath(path) {
		return true
	}
	switch path {
	case "/api/chat", "/api/generate", "/api/load", "/api/stop", "/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/messages/count_tokens", "/anthropic/messages", "/anthropic/messages/count_tokens", "/anthropic/v1/messages", "/anthropic/v1/messages/count_tokens":
		return true
	default:
		return false
	}
}

func providerRouteLegacyPath(path string) string {
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 3)
	if len(parts) == 3 && parts[0] == "providers" && strings.HasPrefix(parts[2], "v1/") {
		return "/" + parts[2]
	}
	return path
}

func isReadOnlyArtifactPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" {
		return false
	}
	switch parts[1] {
	case "models":
		return (len(parts) == 4 && parts[3] == "manifest") ||
			(len(parts) == 5 && parts[4] == "manifest") ||
			(len(parts) >= 6 && parts[4] == "files")
	case "datasets":
		return (len(parts) == 5 && parts[4] == "manifest") ||
			(len(parts) >= 6 && parts[4] == "files")
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
