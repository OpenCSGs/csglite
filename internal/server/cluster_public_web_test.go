package server

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	clusterpublic "github.com/opencsgs/csglite/ee/cluster/public"
	"github.com/opencsgs/csglite/internal/config"
)

func TestConsoleNetworkAccessBoundaries(t *testing.T) {
	for _, tc := range []struct {
		peer, host, forwarded string
		tls, plain            bool
		want                  string
	}{
		{"127.0.0.1:1234", "localhost:11435", "", false, true, "local"},
		{"[::1]:1234", "[::1]:11435", "", false, true, "local"},
		{"192.168.1.20:1234", "192.168.1.2:11435", "", false, true, "lan"},
		{"10.1.2.3:1234", "10.1.2.4:8080", "", true, false, "lan"},
		{"[fd00::2]:1234", "[fd00::1]:8080", "", true, false, "lan"},
		{"[::ffff:192.168.1.20]:1234", "192.168.1.2:11435", "", false, true, "lan"},
		{"192.0.2.10:1234", "localhost:11435", "", false, true, ""},
		{"192.0.2.10:1234", "192.168.1.2:11435", "127.0.0.1", false, true, ""},
		{"127.0.0.1:1234", "gateway.example:8080", "", true, false, ""},
		{"127.0.0.1:1234", "localhost:8080", "192.0.2.10", true, false, ""},
		{"127.0.0.1:1234", "localhost:8080", "", false, false, ""},
		{"invalid", "localhost:8080", "", true, true, ""},
	} {
		r := httptest.NewRequest("GET", "http://"+tc.host+"/auth/session", nil)
		r.RemoteAddr = tc.peer
		if tc.tls {
			r.TLS = &tls.ConnectionState{}
		}
		if tc.forwarded != "" {
			r.Header.Set("X-Forwarded-For", tc.forwarded)
		}
		if got := localConsoleAccess(r, tc.plain); got != tc.want {
			t.Errorf("%+v: got %q", tc, got)
		}
	}
}

func TestConsoleGatePreservesInferenceAPIKeyAuthority(t *testing.T) {
	root := t.TempDir()
	s := &Server{apiKeys: config.NewAPIKeyStore(root)}
	_, key, err := s.apiKeys.Create("application")
	if err != nil {
		t.Fatal(err)
	}
	management := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authenticatedAPIKey(r); !ok {
			t.Error("inference usage identity missing")
		}
		if admin, _ := r.Context().Value(publicAdminContextKey{}).(bool); admin {
			t.Error("API key became administrator")
		}
		w.WriteHeader(204)
	})
	h := publicClusterWebHandler(clusterpublic.NewAdminAuth(clusterpublic.AdminTokenPath(root)), management, s.consoleInferenceKeyRequired(management), true)
	for _, path := range []string{"/v1/images/generations", "/v1/audio/speech", "/providers/test/v1/chat/completions", "/anthropic/messages"} {
		for _, authorized := range []bool{false, true} {
			r := httptest.NewRequest("POST", "https://gateway.example"+path, nil)
			if authorized {
				r.Header.Set("Authorization", "Bearer "+key)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 401
			if authorized {
				want = 204
			}
			if w.Code != want {
				t.Errorf("%s authorized=%v: %d", path, authorized, w.Code)
			}
		}
	}
	r := httptest.NewRequest("GET", "https://gateway.example/api/settings", nil)
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("API key gained management access")
	}
}

func TestLocalConsoleSessionAndOrigin(t *testing.T) {
	a := clusterpublic.NewAdminAuth(clusterpublic.AdminTokenPath(t.TempDir()))
	management := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := publicClusterWebHandler(a, management, management, true)
	r := httptest.NewRequest("GET", "http://localhost:11435/auth/session", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var session struct {
		Access   string `json:"access_mode"`
		Role     string `json:"role"`
		Instance string `json:"instance_name"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || session.Access != "local" || session.Role != "administrator" || session.Instance == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("DELETE", "http://localhost:11435/api/cloud/auth/token", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Origin", "https://attacker.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin network access accepted", w.Code)
	}
	r.Header.Set("Origin", "http://localhost:11435")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("local same-origin management rejected", w.Code)
	}
}

func TestPublicWebManagementRequiresIndependentAdminSession(t *testing.T) {
	root := t.TempDir()
	path := clusterpublic.AdminTokenPath(root)
	token, err := clusterpublic.GenerateAdminToken(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{apiKeys: config.NewAPIKeyStore(root)}
	_, key, err := s.apiKeys.Create("inference-only")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.apiKeys.SetAuthEnabled(true)
	if err != nil {
		t.Fatal(err)
	}
	admin := s.apiAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, _ := r.Context().Value(publicAdminContextKey{}).(bool); !ok {
			t.Error("missing trusted context")
		}
		w.WriteHeader(204)
	}))
	h := publicClusterWebHandler(clusterpublic.NewAdminAuth(path), admin, s.publicClusterInferenceRoutes(), false)
	for _, tc := range []struct {
		path, key string
		status    int
	}{
		{"/", "", 303}, {"/login", "", 200}, {"/api/settings", "", 401}, {"/api/cluster/token", key, 401},
		{"/v1/models", key, 200}, {"/v1/models", "", 401}, {"/v1/models", token, 401},
	} {
		r := httptest.NewRequest("GET", "https://gateway.example"+tc.path, nil)
		r.Header.Set("Authorization", "Bearer "+tc.key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%s: %d, want %d", tc.path, w.Code, tc.status)
		}
	}
	r := httptest.NewRequest("POST", "https://gateway.example/auth/login", strings.NewReader(`{"token":"`+key+`"}`))
	r.Header.Set("Origin", "https://gateway.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("inference key allowed admin login")
	}
	r = httptest.NewRequest("POST", "https://gateway.example/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
	r.Header.Set("Origin", "https://gateway.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	for _, method := range []string{"GET", "PUT"} {
		r = httptest.NewRequest(method, "https://gateway.example/api/settings", nil)
		r.RemoteAddr = "192.0.2.1:1234"
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if method == "GET" && w.Code != 204 {
			t.Fatal("authenticated admin denied", w.Code)
		}
		if method == "PUT" && w.Code != 401 {
			t.Fatal("missing CSRF permitted")
		}
		r.Header.Set("Origin", "https://gateway.example")
		for _, c := range cookies {
			if c.Name == clusterpublic.CSRFCookie {
				r.Header.Set(clusterpublic.CSRFHeader, c.Value)
			}
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatal("authenticated management write denied", w.Code)
		}
	}
}
