package server

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func browserLoginFixture(t *testing.T) (*Server, *atomic.Int32) {
	t.Helper()
	var verifies atomic.Int32
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.Redirect(w, r, "https://iam.example.test/authorize?client_id=example&redirect_uri=https%3A%2F%2Fhub.example.test%2Fcallback&state=casdoor", 302)
		case "/hf/api/whoami-v2":
			verifies.Add(1)
			if r.Header.Get("Authorization") != "Bearer verified-jwt" {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`{"name":"alice"}`))
		case "/api/v1/user/alice/tokens":
			if r.Header.Get("Authorization") != "Bearer verified-jwt" || r.URL.Query().Get("app") != "csghub" {
				t.Error("SSO exchange lost its platform identity")
			}
			_, _ = w.Write([]byte(`{"data":[{"token":"platform-pat","application":"csghub"}]}`))
		case "/api/v1/token/platform-pat":
			_, _ = w.Write([]byte(`{"data":{"user_name":"alice","user_uuid":"alice-id"}}`))
		case "/api/v1/user/alice":
			if r.Header.Get("Authorization") != "Bearer platform-pat" {
				t.Error("saved credential was not verified")
			}
			_, _ = w.Write([]byte(`{"data":{"username":"alice","uuid":"alice-id"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hub.Close)
	s := newTestServer(t)
	s.cfg.ServerURL = hub.URL
	return s, &verifies
}

func beginBrowserLogin(t *testing.T, s *Server, origin string) (*http.Cookie, *url.URL) {
	t.Helper()
	req := httptest.NewRequest("POST", origin+"/api/cloud/auth/login", nil)
	if strings.HasPrefix(origin, "https:") {
		req.TLS = &tls.ConnectionState{}
	}
	w := httptest.NewRecorder()
	s.handleCloudBrowserLogin(w, req)
	if w.Code != 200 {
		t.Fatalf("start = %d: %s", w.Code, w.Body.String())
	}
	var reply struct {
		LoginURL string `json:"login_url"`
	}
	if json.Unmarshal(w.Body.Bytes(), &reply) != nil {
		t.Fatal("invalid start response")
	}
	authorize, err := url.Parse(reply.LoginURL)
	if err != nil {
		t.Fatal(err)
	}
	callback, err := url.Parse(authorize.Query().Get("state"))
	if err != nil {
		t.Fatal(err)
	}
	if callback.Scheme+"://"+callback.Host != origin || callback.Path != browserLoginCallback || len(callback.Query().Get("state")) != 64 {
		t.Fatalf("callback did not return to the initiating instance: %s", callback)
	}
	if authorize.Query().Get("redirect_uri") != "https://hub.example.test/callback" {
		t.Fatal("changed the provider's registered OAuth redirect URI")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].SameSite != http.SameSiteLaxMode || !cookies[0].HttpOnly || cookies[0].Secure != strings.HasPrefix(origin, "https:") {
		t.Fatal("browser nonce cookie policy is incorrect")
	}
	return cookies[0], callback
}

func finishBrowserLogin(s *Server, cookie *http.Cookie, callback *url.URL, jwt string) *httptest.ResponseRecorder {
	target := *callback
	q := target.Query()
	q.Set("jwt_token", jwt)
	target.RawQuery = q.Encode()
	r := httptest.NewRequest("GET", target.String(), nil)
	if target.Scheme == "https" {
		r.TLS = &tls.ConnectionState{}
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	// This return is cross-site and has no console login cookie or API key.
	// Only the nonce-gated callback may pass through the real public gate.
	s.publicClusterWebRoutes(s.storageRoot).ServeHTTP(w, r)
	return w
}

func TestCloudBrowserLoginUsesCurrentInstance(t *testing.T) {
	for _, origin := range []string{"http://localhost:11435", "http://192.168.1.8:11435", "https://gateway.example:8080"} {
		t.Run(origin, func(t *testing.T) {
			s, verifies := browserLoginFixture(t)
			cookie, callback := beginBrowserLogin(t, s, origin)
			w := finishBrowserLogin(s, cookie, callback, "verified-jwt")
			if w.Code != 200 || !strings.Contains(w.Body.String(), "Login complete") || s.cfg.Token != "platform-pat" {
				t.Fatalf("callback failed: status=%d token_saved=%v", w.Code, s.cfg.Token == "platform-pat")
			}
			if strings.Contains(w.Body.String(), "verified-jwt") || strings.Contains(w.Body.String(), "platform-pat") {
				t.Fatal("callback leaked credentials")
			}
			if !strings.Contains(w.Body.String(), "history.replaceState") {
				t.Fatal("callback retained its credentials in browser history")
			}
			if finishBrowserLogin(s, cookie, callback, "verified-jwt").Code != 403 || verifies.Load() != 1 {
				t.Fatal("callback nonce could be replayed")
			}
		})
	}
}

func TestCloudBrowserLoginRejectsUnboundReturns(t *testing.T) {
	for _, failure := range []string{"cookie", "state", "origin", "platform", "expired", "jwt"} {
		t.Run(failure, func(t *testing.T) {
			s, _ := browserLoginFixture(t)
			cookie, callback := beginBrowserLogin(t, s, "https://gateway.example:8080")
			jwt := "verified-jwt"
			switch failure {
			case "cookie":
				cookie = nil
			case "state":
				q := callback.Query()
				q.Set("state", strings.Repeat("a", 64))
				callback.RawQuery = q.Encode()
			case "origin":
				callback.Host = "another.example:8080"
			case "platform":
				s.cfg.ServerURL = "https://another-community.example"
			case "expired":
				s.cloudLoginMu.Lock()
				a := s.cloudLoginAttempts[callback.Query().Get("state")]
				a.expires = a.expires.Add(-20 * time.Minute)
				s.cloudLoginAttempts[callback.Query().Get("state")] = a
				s.cloudLoginMu.Unlock()
			case "jwt":
				jwt = "unverified-jwt"
			}
			w := finishBrowserLogin(s, cookie, callback, jwt)
			if s.cfg.Token != "" || strings.Contains(w.Body.String(), "Login complete") {
				t.Fatal("unbound or unverified callback changed credentials")
			}
		})
	}
}

func TestCloudBrowserLoginDisconnectCancelsPendingReturn(t *testing.T) {
	s, verifies := browserLoginFixture(t)
	cookie, callback := beginBrowserLogin(t, s, "https://gateway.example:8080")
	w := httptest.NewRecorder()
	s.handleCloudAuthTokenDelete(w, httptest.NewRequest("DELETE", "/api/cloud/auth/token", nil))
	if w.Code != 200 {
		t.Fatalf("disconnect: %d", w.Code)
	}
	if finishBrowserLogin(s, cookie, callback, "verified-jwt").Code != 403 || s.cfg.Token != "" || verifies.Load() != 0 {
		t.Fatal("cancelled SSO reconnected after disconnect")
	}
}

func TestCloudBrowserLoginCannotStartWithoutConsoleAccess(t *testing.T) {
	s, verifies := browserLoginFixture(t)
	req := httptest.NewRequest("POST", "https://gateway.example:8080/api/cloud/auth/login", nil)
	req.TLS = &tls.ConnectionState{}
	req.RemoteAddr = "203.0.113.10:4567"
	w := httptest.NewRecorder()
	s.publicClusterWebRoutes(s.storageRoot).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized || verifies.Load() != 0 || len(s.cloudLoginAttempts) != 0 {
		t.Fatalf("unauthenticated visitor started SSO: %d", w.Code)
	}
}
