package server

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	clusterpublic "github.com/opencsgs/csglite/ee/cluster/public"
	"github.com/opencsgs/csglite/internal/config"
)

func TestConsoleAccessPreservesInferenceAuthorizationAndIdentity(t *testing.T) {
	root := t.TempDir()
	s := &Server{apiKeys: config.NewAPIKeyStore(root)}
	key, plain, err := s.apiKeys.Create("application")
	if err != nil {
		t.Fatal(err)
	}
	var identified string
	downstream := s.apiAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if record, ok := authenticatedAPIKey(r); ok {
			identified = record.ID
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	h := publicClusterWebHandler(clusterpublic.NewAdminAuth(clusterpublic.AdminTokenPath(root)), downstream, s.consoleInferenceKeyRequired(downstream), true, false)
	for _, enabled := range []bool{false, true} {
		if _, err := s.apiKeys.SetAuthEnabled(enabled); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name, host, peer, origin, secret, path string
		}{
			{"loopback", "localhost:11435", "127.0.0.1:1234", "", "", "/v1/chat/completions"},
			{"loopback key", "localhost:11435", "127.0.0.1:1234", "", plain, "/v1/chat/completions"},
			{"LAN no key", "192.168.1.2:11435", "192.168.1.20:1234", "", "", "/v1/chat/completions"},
			{"LAN invalid key", "192.168.1.2:11435", "192.168.1.20:1234", "", "invalid", "/api/chat"},
			{"LAN key", "192.168.1.2:11435", "192.168.1.20:1234", "", plain, "/providers/test/v1/chat/completions"},
			{"LAN console", "192.168.1.2:11435", "192.168.1.20:1234", "http://192.168.1.2:11435", "", "/api/chat"},
			{"LAN console key", "192.168.1.2:11435", "192.168.1.20:1234", "http://192.168.1.2:11435", plain, "/api/chat"},
			{"LAN management", "192.168.1.2:11435", "192.168.1.20:1234", "", "", "/api/settings"},
			{"cross origin", "192.168.1.2:11435", "192.168.1.20:1234", "https://other.example", plain, "/api/chat"},
		} {
			t.Run(tc.name+map[bool]string{true: " auth enabled", false: " auth disabled"}[enabled], func(t *testing.T) {
				identified = ""
				r := httptest.NewRequest("POST", "http://"+tc.host+tc.path, nil)
				r.RemoteAddr = tc.peer
				r.Header.Set("Origin", tc.origin)
				if tc.secret != "" {
					r.Header.Set("Authorization", "Bearer "+tc.secret)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				want := http.StatusNoContent
				if enabled && !isLoopbackRequest(r) && tc.origin == "" && tc.secret != plain && tc.path != "/api/settings" {
					want = http.StatusUnauthorized
				}
				if tc.name == "cross origin" {
					want = http.StatusForbidden
				}
				if w.Code != want {
					t.Fatalf("got %d, want %d", w.Code, want)
				}
				if want == http.StatusNoContent && tc.secret == plain && identified != key.ID {
					t.Fatalf("API key identity lost: %q", identified)
				}
			})
		}
	}
}

func TestPublicListenerRequiresInferenceKeyEvenFromLAN(t *testing.T) {
	root := t.TempDir()
	s := &Server{apiKeys: config.NewAPIKeyStore(root)}
	management := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("native caller gained console authority") })
	h := publicClusterWebHandler(clusterpublic.NewAdminAuth(clusterpublic.AdminTokenPath(root)), management, s.publicClusterInferenceRoutes(), false, false)
	r := httptest.NewRequest("GET", "https://192.168.1.2/v1/models", nil)
	r.TLS = &tls.ConnectionState{}
	r.RemoteAddr = "192.168.1.20:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", w.Code)
	}
}

func TestDesktopConsoleSessionUsesAutomaticDesktopAuthentication(t *testing.T) {
	s := newTestServer(t)
	t.Setenv(config.EnvConsoleAuthMode, config.ConsoleAuthRequired)
	s.cfg.DesktopMode = true
	s.cfg.DesktopSessionToken = "desktop-session"
	for _, authorized := range []bool{false, true} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:43123/auth/session", nil)
		if authorized {
			r.AddCookie(&http.Cookie{Name: desktopSessionCookie, Value: "desktop-session"})
		}
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		want := http.StatusUnauthorized
		if authorized {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Fatalf("got %d, want %d", w.Code, want)
		}
	}
	for _, path := range []string{"/login", "/login.html"} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:43123"+path, nil)
		r.AddCookie(&http.Cookie{Name: desktopSessionCookie, Value: "desktop-session"})
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
			t.Fatalf("desktop exposed public login at %s: %d", path, w.Code)
		}
	}
}

func TestRequiredConsoleLoginProtectsLocalAndLANManagement(t *testing.T) {
	for _, plain := range []bool{true, false} {
		for _, host := range []string{"localhost", "192.168.1.2"} {
			t.Run(host+map[bool]string{true: " ordinary", false: " public"}[plain], func(t *testing.T) {
				root := t.TempDir()
				token, err := clusterpublic.GenerateAdminToken(clusterpublic.AdminTokenPath(root))
				if err != nil {
					t.Fatal(err)
				}
				s := &Server{apiKeys: config.NewAPIKeyStore(root)}
				_, key, err := s.apiKeys.Create("inference")
				if err != nil {
					t.Fatal(err)
				}
				management := s.apiAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
				h := publicClusterWebHandler(clusterpublic.NewAdminAuth(clusterpublic.AdminTokenPath(root)), management, s.consoleInferenceKeyRequired(management), plain, true)
				request := func(method, path, body string) *http.Request {
					r := httptest.NewRequest(method, "https://"+host+path, strings.NewReader(body))
					r.RemoteAddr = "192.168.1.20:1234"
					if host == "localhost" {
						r.RemoteAddr = "127.0.0.1:1234"
					}
					return r
				}
				for _, tc := range []struct {
					method, path string
					status       int
				}{
					{"GET", "/", 303}, {"GET", "/login", 200}, {"GET", "/auth/session", 401},
					{"GET", "/api/settings", 401}, {"PUT", "/api/settings", 401}, {"GET", "/api/cluster/token", 401},
				} {
					r := request(tc.method, tc.path, "")
					r.Header.Set("Authorization", "Bearer "+key)
					r.Header.Set("Origin", "https://"+host)
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					if w.Code != tc.status {
						t.Fatalf("%s %s = %d want %d", tc.method, tc.path, w.Code, tc.status)
					}
				}
				// Management policy does not change native inference authorization.
				for _, enabled := range []bool{true, false} {
					if _, err := s.apiKeys.SetAuthEnabled(enabled); err != nil {
						t.Fatal(err)
					}
					for _, secret := range []string{"", key} {
						r := request("POST", "/v1/chat/completions", "")
						r.Header.Set("Authorization", "Bearer "+secret)
						w := httptest.NewRecorder()
						h.ServeHTTP(w, r)
						want := 204
						if secret == "" && (!plain || (enabled && host != "localhost")) {
							want = 401
						}
						if w.Code != want {
							t.Fatalf("inference enabled=%v key=%v: %d want %d", enabled, secret != "", w.Code, want)
						}
					}
				}
				r := request("POST", "/auth/login", `{"token":"`+token+`"}`)
				r.Header.Set("Origin", "https://"+host)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != 200 {
					t.Fatal("login failed", w.Code, w.Body.String())
				}
				cookies := w.Result().Cookies()
				for _, csrf := range []bool{false, true} {
					r = request("PUT", "/api/settings", "")
					r.Header.Set("Origin", "https://"+host)
					for _, c := range cookies {
						r.AddCookie(c)
						if csrf && c.Name == clusterpublic.CSRFCookie {
							r.Header.Set(clusterpublic.CSRFHeader, c.Value)
						}
					}
					w = httptest.NewRecorder()
					h.ServeHTTP(w, r)
					want := 401
					if csrf {
						want = 204
					}
					if w.Code != want {
						t.Fatalf("session write csrf=%v: %d want %d", csrf, w.Code, want)
					}
				}
			})
		}
	}
}

func TestConsoleRoutesApplyConfiguredPolicy(t *testing.T) {
	s := newTestServer(t)
	for _, tc := range []struct {
		configured, env string
		want            int
	}{
		{"", "", 200}, {"required", "", 401}, {"required", "auto", 200}, {"auto", "required", 401}, {"", "invalid", 401},
	} {
		t.Setenv(config.EnvConsoleAuthMode, tc.env)
		s.cfg.Console.AuthMode = tc.configured
		for _, plain := range []bool{true, false} {
			h := s.consoleWebRoutes(t.TempDir(), plain)
			r := httptest.NewRequest("GET", "https://localhost/auth/session", nil)
			r.RemoteAddr = "127.0.0.1:1234"
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("%+v plain=%v got=%d", tc, plain, w.Code)
			}
		}
	}
	t.Setenv(config.EnvConsoleAuthMode, "invalid")
	if err := s.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "console auth mode") {
		t.Fatalf("invalid policy startup: %v", err)
	}
}
