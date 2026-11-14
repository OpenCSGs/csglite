package server

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"

	clusterpublic "github.com/opencsgs/csglite/ee/cluster/public"
)

type publicAdminContextKey struct{}

// The public listener exposes management only to an authenticated browser.
// API keys keep their existing inference-only authority.
func (s *Server) publicClusterWebRoutes(storageRoot string) http.Handler {
	return s.consoleWebRoutes(storageRoot, false)
}

func (s *Server) consoleWebRoutes(storageRoot string, allowPlainLocal bool) http.Handler {
	auth := clusterpublic.NewAdminAuth(clusterpublic.AdminTokenPath(storageRoot))
	management := s.routes()
	inference := s.publicClusterInferenceRoutes()
	if allowPlainLocal {
		// The ordinary server keeps its full inference API (including provider,
		// image and audio routes). Only the dedicated cluster listener narrows it.
		inference = s.consoleInferenceKeyRequired(management)
	}
	return publicClusterWebHandler(auth, management, inference, allowPlainLocal)
}

func (s *Server) consoleInferenceKeyRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), apiKeyContextKey{}, record)))
	})
}

// Trust the direct socket peer, never a client-supplied forwarding header.
// Requiring a local destination also prevents a public reverse proxy from
// inheriting the exemption merely because it connects over loopback.
func localConsoleAccess(r *http.Request, allowPlainLocal bool) string {
	if !allowPlainLocal && r.TLS == nil {
		return ""
	}
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Real-IP", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		if r.Header.Get(header) != "" {
			return ""
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	peer = peer.Unmap()
	if !peer.IsLoopback() && !peer.IsPrivate() && !peer.IsLinkLocalUnicast() {
		return ""
	}
	destination := r.Host
	if h, _, err := net.SplitHostPort(destination); err == nil {
		destination = h
	}
	destination = strings.Trim(destination, "[]")
	addr, err := netip.ParseAddr(destination)
	hostname, _ := os.Hostname()
	localHost := strings.EqualFold(destination, "localhost") || (hostname != "" && strings.EqualFold(destination, hostname))
	if err == nil {
		addr = addr.Unmap()
		localHost = addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast()
	}
	if !localHost {
		return ""
	}
	if peer.IsLoopback() {
		return "local"
	}
	return "lan"
}

func consoleInstanceName() string {
	name, _ := os.Hostname()
	if name == "" {
		return "CSGLite"
	}
	return name
}

func publicClusterWebHandler(auth *clusterpublic.AdminAuth, management, inference http.Handler, allowPlainLocal bool) http.Handler {
	static := staticHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		access := localConsoleAccess(r, allowPlainLocal)
		// Network access still rejects cross-origin browser requests, including
		// mutations and WebSocket upgrades, without demanding a login cookie.
		if access != "" && r.Header.Get("Origin") != "" {
			u, err := url.Parse(r.Header.Get("Origin"))
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.EqualFold(u.Host, r.Host) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				writeError(w, 403, "same origin required")
				return
			}
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /auth/login":
			auth.Login(w, r)
			return
		case "POST /auth/logout":
			auth.Logout(w, r)
			return
		case "GET /auth/session":
			if access != "" {
				writeJSON(w, 200, map[string]any{"authenticated": true, "access_mode": access, "role": "administrator", "instance_name": consoleInstanceName()})
				return
			}
			expires, ok := auth.SessionExpiry(r)
			if !ok {
				writeError(w, 401, "login required")
				return
			}
			writeJSON(w, 200, map[string]any{"authenticated": true, "access_mode": "token", "role": "administrator", "instance_name": consoleInstanceName(), "expires_at": expires})
			return
		case "GET /login", "GET /login.html":
			if access != "" {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			copy := r.Clone(r.Context())
			copy.URL.Path = "/login.html"
			static.ServeHTTP(w, copy)
			return
		}
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && (strings.HasPrefix(r.URL.Path, "/assets/") || r.URL.Path == "/favicon.svg") {
			static.ServeHTTP(w, r)
			return
		}
		if access != "" || auth.Authenticate(r) {
			management.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), publicAdminContextKey{}, true)))
			return
		}
		inferencePath := providerRouteLegacyPath(r.URL.Path)
		if strings.HasPrefix(inferencePath, "/v1/") || strings.HasPrefix(inferencePath, "/anthropic/") || inferencePath == "/api/chat" || inferencePath == "/api/generate" {
			inference.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/auth/") || r.Method != http.MethodGet {
			writeError(w, 401, "public management login required")
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}
