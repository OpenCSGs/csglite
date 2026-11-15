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
	secondToken, err := GenerateAdminToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.Authenticate(makeRequest("GET")) {
		t.Fatal("adding a token invalidated another token's session")
	}
	if got := request("POST", token, "https://gateway.example").Code; got != 200 {
		t.Fatal("existing token no longer logs in")
	}
	secondLogin := request("POST", secondToken, "https://gateway.example")
	if secondLogin.Code != 200 {
		t.Fatal("second token cannot log in")
	}
	if deleted, err := RevokeAdminToken(path, tokenHash(token)[:16]); err != nil || !deleted {
		t.Fatal(deleted, err)
	}
	if auth.Authenticate(makeRequest("GET")) {
		t.Fatal("revoked token retained session")
	}
	if got := request("POST", token, "https://gateway.example").Code; got != 401 {
		t.Fatal("revoked token still logs in")
	}
	secondRequest := httptest.NewRequest("GET", "https://gateway.example/api/cluster", nil)
	for _, cookie := range secondLogin.Result().Cookies() {
		secondRequest.AddCookie(cookie)
	}
	if !auth.Authenticate(secondRequest) {
		t.Fatal("revoking first token invalidated second session")
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

func TestAdminTokenExpiryBoundsLoginAndSessions(t *testing.T) {
	path := AdminTokenPath(t.TempDir())
	token, err := GenerateAdminTokenWithTTL(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var credentials adminCredentialState
	if err := json.Unmarshal(data, &credentials); err != nil {
		t.Fatal(err)
	}
	credential := &credentials.Tokens[0]
	if credential.ExpiresAt == nil || credential.ExpiresAt.Sub(credential.CreatedAt) != time.Hour {
		t.Fatal("token expiry was not persisted")
	}
	if strings.Contains(string(data), token) {
		t.Fatal("plaintext token persisted")
	}
	auth := NewAdminAuth(path)
	login := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://gateway.example/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
		r.Header.Set("Origin", "https://gateway.example")
		w := httptest.NewRecorder()
		auth.Login(w, r)
		return w
	}
	w := login()
	if w.Code != 200 {
		t.Fatalf("valid expiring token rejected: %d %s", w.Code, w.Body)
	}
	var session struct {
		Expires time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if !session.Expires.Equal(*credential.ExpiresAt) {
		t.Fatal("session outlives token")
	}
	request := httptest.NewRequest("GET", "https://gateway.example/api/cluster", nil)
	for _, cookie := range w.Result().Cookies() {
		if cookie.MaxAge <= 0 || cookie.MaxAge > 3600 || !cookie.Expires.Equal(credential.ExpiresAt.Truncate(time.Second)) {
			t.Fatalf("cookie outlives token: %+v", cookie)
		}
		request.AddCookie(cookie)
	}
	if !auth.Authenticate(request) {
		t.Fatal("valid session rejected")
	}
	// The same hash remains on disk: expiry alone must invalidate an existing
	// session immediately, including after restarting the auth handler.
	past := time.Now().Add(-time.Second)
	credential.ExpiresAt = &past
	data, _ = json.Marshal(credentials)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if auth.Authenticate(request) {
		t.Fatal("expired credential retained an authenticated session")
	}
	if w := login(); w.Code != 401 {
		t.Fatalf("expired token logged in: %d", w.Code)
	}
	auth = NewAdminAuth(path)
	if w := login(); w.Code != 401 {
		t.Fatalf("restart extended token expiry: %d", w.Code)
	}
}

func TestAdminCookieMaxAge(t *testing.T) {
	for _, tt := range []struct {
		name      string
		remaining time.Duration
		want      int
	}{
		{"eight_hours_exact", 8 * time.Hour, 8 * 3600},
		{"eight_hours_minus_nanosecond", 8*time.Hour - time.Nanosecond, 8 * 3600},
		{"one_hour_exact", time.Hour, 3600},
		{"one_second_exact", time.Second, 1},
		{"one_second_plus_nanosecond", time.Second + time.Nanosecond, 2},
		{"subsecond", time.Nanosecond, 1},
		// Zero means a browser-session cookie and negative values delete it.
		// Keep Max-Age positive; authentication enforces the exact deadline.
		{"zero", 0, 1},
		{"expired", -time.Second, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := adminCookieMaxAge(tt.remaining); got != tt.want {
				t.Fatalf("Max-Age for %s = %d, want %d", tt.remaining, got, tt.want)
			}
		})
	}
}

func TestAdminTokenTTLCompatibilityAndInvalidDuration(t *testing.T) {
	path := AdminTokenPath(t.TempDir())
	if _, err := GenerateAdminToken(path); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(original), "expires_at") {
		t.Fatal("default generation unexpectedly changed token lifetime")
	}
	if _, err := GenerateAdminTokenWithTTL(path, -time.Second); err == nil {
		t.Fatal("negative TTL accepted")
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) {
		t.Fatal("invalid TTL revoked the existing token")
	}
	token, err := GenerateAdminTokenWithTTL(path, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	auth := NewAdminAuth(path)
	r := httptest.NewRequest("POST", "https://gateway.example/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
	r.Header.Set("Origin", "https://gateway.example")
	w := httptest.NewRecorder()
	auth.Login(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.MaxAge > 8*3600 {
			t.Fatal("week-long token extended session beyond eight hours")
		}
	}
}
