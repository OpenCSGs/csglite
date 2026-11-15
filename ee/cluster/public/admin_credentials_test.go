// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package public

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func adminLogin(t *testing.T, auth *AdminAuth, token string, cookies []*http.Cookie) []*http.Cookie {
	t.Helper()
	r := httptest.NewRequest("POST", "https://gateway.example/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
	r.Header.Set("Origin", "https://gateway.example")
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	auth.Login(w, r)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	return w.Result().Cookies()
}

func adminRead(cookies []*http.Cookie) *http.Request {
	r := httptest.NewRequest("GET", "https://gateway.example/api/settings", nil)
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	return r
}

func TestAdminLegacyMigrationPreservesExistingCredentialAndSessions(t *testing.T) {
	path := AdminTokenPath(t.TempDir())
	oldToken := "csgadmin_legacy-fixture"
	expires := time.Now().UTC().Add(24 * time.Hour)
	legacy := map[string]any{"token_hash": tokenHash(oldToken), "created_at": time.Now().UTC().Add(-time.Hour), "expires_at": expires}
	data, _ := json.Marshal(legacy)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	auth := NewAdminAuth(path)
	cookies := adminLogin(t, auth, oldToken, nil)
	metadata, err := ListAdminTokens(path)
	if err != nil || len(metadata) != 1 || !metadata[0].ExpiresAt.Equal(expires) {
		t.Fatal(metadata, err)
	}
	id := metadata[0].ID
	newInfo, newToken, err := CreateAdminToken(path, "second administrator", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.Authenticate(adminRead(cookies)) {
		t.Fatal("migration invalidated existing session")
	}
	adminLogin(t, auth, oldToken, nil)
	adminLogin(t, auth, newToken, nil)
	state, err := loadAdminCredentials(path)
	if err != nil || len(state.Tokens) != 2 || state.Tokens[0].ID != id || !state.Tokens[0].ExpiresAt.Equal(expires) || newInfo.ID == id {
		t.Fatal(state, err)
	}
	persisted, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(persisted), oldToken) || strings.Contains(string(persisted), newToken) {
		t.Fatal("stored plaintext credentials", err)
	}
	var layout map[string]json.RawMessage
	if err := json.Unmarshal(persisted, &layout); err != nil {
		t.Fatal(err)
	}
	if _, ok := layout["token_hash"]; ok {
		t.Fatal("migration retained overlapping writable schemas")
	}
	// A fresh server reads both tokens; existing sessions remain memory-only.
	restarted := NewAdminAuth(path)
	adminLogin(t, restarted, oldToken, nil)
	adminLogin(t, restarted, newToken, nil)
	if restarted.Authenticate(adminRead(cookies)) {
		t.Fatal("sessions unexpectedly persisted across restart")
	}
}

func TestAdminBrowserSwitchDoesNotInvalidateOtherBrowsers(t *testing.T) {
	path := AdminTokenPath(t.TempDir())
	_, first, err := CreateAdminToken(path, "first", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := CreateAdminToken(path, "second", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	auth := NewAdminAuth(path)
	firstBrowser := adminLogin(t, auth, first, nil)
	otherBrowser := adminLogin(t, auth, first, nil)
	switched := adminLogin(t, auth, second, firstBrowser)
	if auth.Authenticate(adminRead(firstBrowser)) {
		t.Fatal("browser kept its replaced session")
	}
	if !auth.Authenticate(adminRead(switched)) || !auth.Authenticate(adminRead(otherBrowser)) {
		t.Fatal("switching tokens invalidated another browser")
	}
	// Logout is session-scoped even when other sessions use the same token.
	secondBrowser := adminLogin(t, auth, second, nil)
	r := adminRead(switched)
	r.Method = "POST"
	r.Header.Set("Origin", "https://gateway.example")
	for _, c := range switched {
		if c.Name == CSRFCookie {
			r.Header.Set(CSRFHeader, c.Value)
		}
	}
	w := httptest.NewRecorder()
	auth.Logout(w, r)
	if w.Code != 200 || auth.Authenticate(adminRead(switched)) || !auth.Authenticate(adminRead(secondBrowser)) || !auth.Authenticate(adminRead(otherBrowser)) {
		t.Fatal("logout was not scoped to its browser", w.Code)
	}
}

func TestAdminExpiryRevocationAndEmptyStoreStayIsolated(t *testing.T) {
	path := AdminTokenPath(t.TempDir())
	first, tokenA, err := CreateAdminToken(path, "expires", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, tokenB, err := CreateAdminToken(path, "stays", 0)
	if err != nil {
		t.Fatal(err)
	}
	auth := NewAdminAuth(path)
	cookiesA := adminLogin(t, auth, tokenA, nil)
	cookiesB := adminLogin(t, auth, tokenB, nil)
	state, err := loadAdminCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Second)
	state.Tokens[0].ExpiresAt = &past
	if err := saveAdminCredentials(path, state); err != nil {
		t.Fatal(err)
	}
	// Login also prunes dead sessions without deleting other active sessions.
	adminLogin(t, auth, tokenB, nil)
	if auth.Authenticate(adminRead(cookiesA)) || !auth.Authenticate(adminRead(cookiesB)) {
		t.Fatal("expiry affected another credential")
	}
	if ok, err := RevokeAdminToken(path, first.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if !auth.Authenticate(adminRead(cookiesB)) {
		t.Fatal("revoking an expired token affected active session")
	}
	if ok, err := RevokeAdminToken(path, second.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if auth.Authenticate(adminRead(cookiesB)) {
		t.Fatal("revoked token retained a session")
	}
	list, err := ListAdminTokens(path)
	if err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	if ok, err := RevokeAdminToken(path, second.ID); err != nil || ok {
		t.Fatal("unknown ID altered store", ok, err)
	}
	_, fresh, err := CreateAdminToken(path, "fresh", 0)
	if err != nil {
		t.Fatal(err)
	}
	adminLogin(t, auth, fresh, nil)
}

func TestAdminMalformedStoreAndInvalidArgumentsNeverOverwriteCredentials(t *testing.T) {
	path := AdminTokenPath(t.TempDir())
	if _, _, err := CreateAdminToken(path, "valid", 0); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		ttl  time.Duration
	}{{"invalid", -time.Second}, {"line\nbreak", 0}, {strings.Repeat("x", 129), 0}} {
		if _, _, err := CreateAdminToken(path, tc.name, tc.ttl); err == nil {
			t.Fatal("invalid argument accepted")
		}
		current, _ := os.ReadFile(path)
		if string(current) != string(original) {
			t.Fatal("invalid input modified credentials")
		}
	}
	for _, bad := range []string{`{`, `null`, `{}`, `{"version":2,"tokens":[]}`, `{"version":1,"tokens":null}`, `{"version":1,"tokens":[{}]}`} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := CreateAdminToken(path, "must-fail", 0); err == nil {
			t.Fatal("malformed credentials overwritten")
		}
		if _, err := ListAdminTokens(path); err == nil {
			t.Fatal("malformed credential file accepted")
		}
		current, _ := os.ReadFile(path)
		if string(current) != bad {
			t.Fatal("malformed credential file was replaced")
		}
	}
}

func TestAdminConcurrentCLIProcessesPreserveEveryToken(t *testing.T) {
	if path := os.Getenv("CSGLITE_TEST_ADMIN_STORE"); path != "" {
		if _, _, err := CreateAdminToken(path, "concurrent", time.Hour); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := AdminTokenPath(t.TempDir())
	commands := make([]*exec.Cmd, 6)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestAdminConcurrentCLIProcessesPreserveEveryToken$")
		commands[i].Env = append(os.Environ(), "CSGLITE_TEST_ADMIN_STORE="+path)
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	list, err := ListAdminTokens(path)
	if err != nil || len(list) != len(commands) {
		t.Fatalf("concurrent creation lost tokens: %d, %v", len(list), err)
	}
}
