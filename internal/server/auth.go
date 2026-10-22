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
		if s.apiKeys == nil {
			next.ServeHTTP(w, r)
			return
		}
		secret := isClusterSecretPath(r.URL.Path) && !isLoopbackRequest(r)
		if !secret && (!requiresRemoteAPIAuth(r) || isLoopbackRequest(r)) {
			next.ServeHTTP(w, s.requestWithIdentifiedAPIKey(r))
			return
		}

		state, err := s.apiKeys.State()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load API key settings")
			return
		}
		if !state.AuthEnabled {
			// Reading the join token or the admission code is the whole of the
			// admission check, so it is never answered to the network on trust
			// alone. With authentication off there is no key to present, so
			// the caller is told where it can be read instead.
			if secret {
				writeError(w, http.StatusForbidden, "the cluster join token and admission code are readable on this machine only; enable API key authentication to read them over the network")
				return
			}
			next.ServeHTTP(w, s.requestWithIdentifiedAPIKey(r))
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

// requestWithIdentifiedAPIKey attaches a recognized API key to the request even
// when authentication is not enforced (loopback callers, or auth disabled), so
// usage is attributed to the key that made the call instead of the built-in
// local client. An absent or unknown key leaves the request untouched.
func (s *Server) requestWithIdentifiedAPIKey(r *http.Request) *http.Request {
	if s == nil || s.apiKeys == nil || !identifiesAPIKeyForUsage(r) {
		return r
	}
	if _, ok := authenticatedAPIKey(r); ok {
		return r
	}
	apiKey := requestAPIKey(r)
	if apiKey == "" {
		return r
	}
	record, ok, err := s.apiKeys.Validate(apiKey)
	if err != nil || !ok {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), apiKeyContextKey{}, record))
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
	// Administering the cluster from another machine is an operator action:
	// it admits and removes nodes, moves where work runs, and reads the token
	// that lets a machine join. It was reachable from anywhere on the network
	// with no key at all because only the inference paths were listed here.
	if isClusterManagementPath(path) {
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

// identifiesAPIKeyForUsage reports whether a request targets an inference route
// whose usage is metered per API key. Discovery, streaming session and
// management routes are excluded so that recognizing a key stays off the hot
// path of clients that poll them.
func identifiesAPIKeyForUsage(r *http.Request) bool {
	if r.Method == http.MethodOptions {
		return false
	}
	path := providerRouteLegacyPath(r.URL.Path)
	if path == "/v1/models" || strings.HasSuffix(path, "/realtime") ||
		strings.HasPrefix(path, "/v1/realtime/") || strings.HasSuffix(path, "/count_tokens") {
		return false
	}
	if strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/anthropic/") {
		return true
	}
	switch path {
	case "/api/chat", "/api/generate":
		return true
	default:
		return false
	}
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}
