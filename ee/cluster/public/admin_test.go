// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package public

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAdminTokenSessions(t *testing.T) {
	path := AdminTokenPath(t.TempDir())
	auth := NewAdminAuth(path)
	token, err := GenerateAdminToken(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), token) {
		t.Fatal("plaintext credential was persisted", err)
	}
	var cookies []*http.Cookie
	request := func(method, token, origin string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"token": token})
		r := httptest.NewRequest(method, "https://gateway.example/auth/login", strings.NewReader(string(body)))
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		auth.Login(w, r)
		return w
	}
	if got := request("POST", token, "https://attacker.example").Code; got != 403 {
		t.Fatalf("cross-origin login: %d", got)
	}
	if got := request("POST", "wrong", "https://gateway.example").Code; got != 401 {
		t.Fatalf("wrong token: %d", got)
	}
	w := request("POST", token, "https://gateway.example")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies = w.Result().Cookies()
	var csrf string
	for _, c := range cookies {
		if !c.Secure || c.Path != "/" || c.SameSite != http.SameSiteStrictMode {
			t.Fatal("insecure cookie")
		}
		if c.Name == AdminCookie && !c.HttpOnly {
			t.Fatal("session must be HttpOnly")
		}
		if c.Name == CSRFCookie {
			csrf = c.Value
		}
	}
	makeRequest := func(method string) *http.Request {
		r := httptest.NewRequest(method, "https://gateway.example/api/cluster", nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		return r
	}
	if !auth.Authenticate(makeRequest("GET")) {
		t.Fatal("read denied")
	}
	r := makeRequest("PUT")
	if auth.Authenticate(r) {
		t.Fatal("mutation allowed without CSRF")
	}
	r.Header.Set("Origin", "https://gateway.example")
	r.Header.Set(CSRFHeader, csrf)
	if !auth.Authenticate(r) {
		t.Fatal("valid mutation denied")
	}
	r.Header.Set("Origin", "https://attacker.example")
	if auth.Authenticate(r) {
		t.Fatal("cross origin mutation accepted")
	}
	r = makeRequest("GET")
	r.Header.Set("Upgrade", "websocket")
	if auth.Authenticate(r) {
		t.Fatal("cross origin websocket accepted")
	}
	r.Header.Set("Origin", "https://gateway.example")
	if !auth.Authenticate(r) {
		t.Fatal("same origin websocket denied")
	}
	// Expiry is enforced server-side even if a browser retains its cookie.
	auth.mu.Lock()
	for key, session := range auth.sessions {
		session.Expires = time.Now().Add(-time.Second)
		auth.sessions[key] = session
	}
	auth.mu.Unlock()
	if auth.Authenticate(makeRequest("GET")) {
		t.Fatal("expired session accepted")
	}
	w = request("POST", token, "https://gateway.example")
	cookies = w.Result().Cookies()
	_, err = GenerateAdminToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if auth.Authenticate(makeRequest("GET")) {
		t.Fatal("rotation did not invalidate session")
	}
	if got := request("POST", token, "https://gateway.example").Code; got != 401 {
		t.Fatal("old token accepted")
	}
}

func TestAdminLogoutAndRateLimit(t *testing.T) {
	path := AdminTokenPath(t.TempDir())
	token, err := GenerateAdminToken(path)
	if err != nil {
		t.Fatal(err)
	}
	a := NewAdminAuth(path)
	r := httptest.NewRequest("POST", "https://gateway.example/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
	r.Header.Set("Origin", "https://gateway.example")
	w := httptest.NewRecorder()
	a.Login(w, r)
	r = httptest.NewRequest("POST", "https://gateway.example/auth/logout", nil)
	r.Header.Set("Origin", "https://gateway.example")
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
		if c.Name == CSRFCookie {
			r.Header.Set(CSRFHeader, c.Value)
		}
	}
	w = httptest.NewRecorder()
	a.Logout(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	r.Method = "GET"
	if a.Authenticate(r) {
		t.Fatal("logged out session accepted")
	}
	for i := 0; i < 11; i++ {
		r = httptest.NewRequest("POST", "https://gateway.example/auth/login", strings.NewReader(`{"token":"bad"}`))
		r.Header.Set("Origin", "https://gateway.example")
		w = httptest.NewRecorder()
		a.Login(w, r)
		if i == 10 && w.Code != 429 {
			t.Fatal("missing rate limit", w.Code)
		}
	}
}
