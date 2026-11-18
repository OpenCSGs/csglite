package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/inference"
)

func newCloudAuthAPIServer(t *testing.T, token, username string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/token/" + token:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"msg": "OK",
				"data": map[string]any{
					"token":       token,
					"token_name":  "token-name",
					"application": "git",
					"user_name":   username,
					"user_uuid":   "user-1",
				},
			})
		case "/api/v1/user/" + username:
			if got := r.Header.Get("Authorization"); got != "Bearer "+token {
				t.Fatalf("Authorization = %q, want %q", got, "Bearer "+token)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"msg": "OK",
				"data": map[string]any{
					"username": username,
					"nickname": "Alice",
					"email":    "alice@example.com",
					"avatar":   "https://example.com/alice.png",
					"uuid":     "user-1",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestHandleCloudAuthStatus(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/cloud/auth", nil)
	w := httptest.NewRecorder()
	s.handleCloudAuthStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp cloudAuthStatus
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.AuthMode != "token" {
		t.Fatalf("AuthMode = %q, want token", resp.AuthMode)
	}
	if resp.LoginURL != cloud.DefaultLoginURL {
		t.Fatalf("LoginURL = %q, want %q", resp.LoginURL, cloud.DefaultLoginURL)
	}
	if resp.HasToken {
		t.Fatalf("HasToken = true, want false")
	}
	if resp.Authenticated {
		t.Fatalf("Authenticated = true, want false")
	}
	if resp.User != nil {
		t.Fatalf("User = %#v, want nil", resp.User)
	}
}

func TestCloudAuthStatusKeepsAPIKeyUsableWhenJWTExpires(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "expired", http.StatusUnauthorized)
	}))
	defer authServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = authServer.URL
	s.cfg.Token = "expired-token"
	s.cfg.OpenCSGAPIKey = "gateway-key"

	status := s.cloudAuthStatus(t.Context())
	if status.Authenticated {
		t.Fatal("expired JWT should not be reported as authenticated")
	}
	if !status.HasToken || !status.HasAPIKey || status.APIKeySource != "manual" {
		t.Fatalf("status = %+v, want expired token plus usable manual API Key", status)
	}
}

func TestCloudAPIKeyUsesManualKeyWithoutValidatingExpiredJWT(t *testing.T) {
	authRequests := 0
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		authRequests++
		http.Error(w, "expired", http.StatusUnauthorized)
	}))
	defer authServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = authServer.URL
	s.cfg.Token = "expired-token"
	s.cfg.OpenCSGAPIKey = "gateway-key"

	apiKey, err := s.cloudAPIKey(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if apiKey != "gateway-key" {
		t.Fatalf("API Key = %q, want gateway-key", apiKey)
	}
	if authRequests != 0 {
		t.Fatalf("JWT validation requests = %d, want 0 when a manual API Key is configured", authRequests)
	}
}

// An instance used only through the API never polls the status endpoint, so a
// rejection here has to mark the token unusable; otherwise it could never adopt
// a login another process saved.
func TestCloudAPIKeyMarksRejectedTokenForAdoption(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.Token = "expired-token"

	if _, err := s.cloudAPIKey(t.Context()); err == nil {
		t.Fatal("rejected token should not produce an API key")
	}
	if !s.cloudTokenInvalid {
		t.Fatal("rejected token should be marked invalid")
	}

	writeDiskCloudCredentialsWithRevision(t, "fresh-token", api.URL, s.cfg.AuthRevision+1)
	s.adoptExternalCloudCredentials()
	if s.cfg.Token != "fresh-token" {
		t.Fatalf("token = %q, want the adopted fresh-token", s.cfg.Token)
	}
}

func TestHandleCloudAuthStatusWithUser(t *testing.T) {
	api := newCloudAuthAPIServer(t, "test-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.Token = "test-token"

	req := httptest.NewRequest(http.MethodGet, "/api/cloud/auth", nil)
	w := httptest.NewRecorder()
	s.handleCloudAuthStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp cloudAuthStatus
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.HasToken {
		t.Fatalf("HasToken = false, want true")
	}
	if !resp.Authenticated {
		t.Fatalf("Authenticated = false, want true")
	}
	if resp.User == nil {
		t.Fatal("User = nil, want non-nil")
	}
	if resp.User.Username != "alice" {
		t.Fatalf("Username = %q, want %q", resp.User.Username, "alice")
	}
	if resp.User.Email != "alice@example.com" {
		t.Fatalf("Email = %q, want %q", resp.User.Email, "alice@example.com")
	}
}

// writeDiskCloudToken simulates another csglite process saving a login to the
// shared config.json. newTestServer points HOME at a temporary directory.
func writeDiskCloudToken(t *testing.T, token string) {
	t.Helper()
	writeDiskCloudCredentialsWithRevision(t, token, "", 0)
}

func writeDiskCloudCredentials(t *testing.T, token, serverURL string) {
	t.Helper()
	writeDiskCloudCredentialsWithRevision(t, token, serverURL, 0)
}

func writeDiskCloudCredentialsWithRevision(t *testing.T, token, serverURL string, revision uint64) {
	t.Helper()
	writeDiskCloudCredentialsWithKey(t, token, serverURL, "", revision)
}

func writeDiskCloudCredentialsWithKey(t *testing.T, token, serverURL, apiKey string, revision uint64) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	appHome := filepath.Join(home, config.AppDir)
	if err := os.MkdirAll(appHome, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"token":           token,
		"server_url":      serverURL,
		"opencsg_api_key": apiKey,
		"auth_revision":   revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appHome, config.ConfigFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The fixed SSO callback port belongs to one instance, so a second instance
// receives no redirect; it has to adopt the token the first one persisted.
func TestCloudAuthStatusAdoptsTokenSavedByAnotherProcess(t *testing.T) {
	api := newCloudAuthAPIServer(t, "adopted-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	writeDiskCloudCredentialsWithRevision(t, "adopted-token", api.URL, 1)

	status := s.cloudAuthStatus(t.Context())
	if s.cfg.Token != "adopted-token" {
		t.Fatalf("in-memory token = %q, want adopted-token", s.cfg.Token)
	}
	if !status.HasToken || !status.Authenticated {
		t.Fatalf("status = %+v, want an authenticated adopted login", status)
	}
	if status.User == nil || status.User.Username != "alice" {
		t.Fatalf("User = %+v, want alice", status.User)
	}
}

// A token minted for another community must not be adopted: validating it here
// would send that community's credential to a different host.
func TestCloudAuthStatusDoesNotAdoptTokenFromAnotherCommunity(t *testing.T) {
	api := newCloudAuthAPIServer(t, "other-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	writeDiskCloudCredentialsWithRevision(t, "other-token", "https://other.example.com", 1)
	status := s.cloudAuthStatus(t.Context())
	if s.cfg.Token != "" {
		t.Fatalf("in-memory token = %q, want no adoption from another community", s.cfg.Token)
	}
	if status.HasToken {
		t.Fatalf("status = %+v, want no token reported", status)
	}
}

// The login generation moves the gateway key too: a newer login saved by
// another process clears the manual key it no longer belongs to, exactly as a
// login in this process does.
func TestCloudAuthStatusAdoptionTakesDiskAPIKeyState(t *testing.T) {
	api := newCloudAuthAPIServer(t, "adopted-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.OpenCSGAPIKey = "manual-key"
	writeDiskCloudCredentialsWithRevision(t, "adopted-token", api.URL, 1)

	status := s.cloudAuthStatus(t.Context())
	if s.cfg.OpenCSGAPIKey != "" {
		t.Fatalf("manual API key = %q, want it cleared by the adopted login", s.cfg.OpenCSGAPIKey)
	}
	if !status.HasToken {
		t.Fatalf("status = %+v, want the adopted token", status)
	}
}

// A key saved by the instance that logged in is part of the adopted session.
func TestCloudAuthStatusAdoptionAppliesDiskAPIKey(t *testing.T) {
	api := newCloudAuthAPIServer(t, "adopted-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.OpenCSGAPIKey = "manual-key"
	writeDiskCloudCredentialsWithKey(t, "adopted-token", api.URL, "disk-key", 1)

	status := s.cloudAuthStatus(t.Context())
	if s.cfg.OpenCSGAPIKey != "disk-key" {
		t.Fatalf("manual API key = %q, want the adopted disk-key", s.cfg.OpenCSGAPIKey)
	}
	if !status.HasAPIKey || status.APIKeySource != "manual" {
		t.Fatalf("status = %+v, want the adopted manual key", status)
	}
}

// A newer generation wins even over a token that still works: the user may
// have switched accounts in the instance that owns the callback port.
func TestCloudAuthStatusFollowsNewerLoginOverWorkingToken(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/token/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"msg": "OK",
			"data": map[string]any{
				"token":     strings.TrimPrefix(r.URL.Path, "/api/v1/token/"),
				"user_name": "alice",
				"user_uuid": "user-1",
			},
		})
	}))
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.Token = "current-token"
	s.cfg.AuthRevision = 4
	writeDiskCloudCredentialsWithRevision(t, "new-token", api.URL, 5)

	status := s.cloudAuthStatus(t.Context())
	if s.cfg.Token != "new-token" {
		t.Fatalf("in-memory token = %q, want the newer login", s.cfg.Token)
	}
	if s.cfg.AuthRevision != 5 {
		t.Fatalf("auth revision = %d, want 5", s.cfg.AuthRevision)
	}
	if !status.Authenticated {
		t.Fatalf("status = %+v, want the newer login authenticated", status)
	}
}

// A newer generation with no token is a logout, and it wins over a token this
// instance still holds; otherwise signing out in one window leaves the other
// window logged in.
func TestCloudAuthStatusFollowsNewerLogout(t *testing.T) {
	api := newCloudAuthAPIServer(t, "current-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.Token = "current-token"
	s.cfg.AuthRevision = 4
	writeDiskCloudCredentialsWithRevision(t, "", api.URL, 5)

	status := s.cloudAuthStatus(t.Context())
	if s.cfg.Token != "" {
		t.Fatalf("in-memory token = %q, want the adopted logout", s.cfg.Token)
	}
	if status.HasToken || status.Authenticated {
		t.Fatalf("status = %+v, want a signed-out instance", status)
	}
}

// A session that still works must not be replaced by an older token a stale
// instance wrote back to the shared config.
func TestCloudAuthStatusKeepsWorkingTokenOverStaleDiskToken(t *testing.T) {
	api := newCloudAuthAPIServer(t, "current-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.Token = "current-token"
	writeDiskCloudToken(t, "stale-token")

	status := s.cloudAuthStatus(t.Context())
	if s.cfg.Token != "current-token" {
		t.Fatalf("in-memory token = %q, want the working current-token", s.cfg.Token)
	}
	if !status.Authenticated {
		t.Fatalf("status = %+v, want the working session kept", status)
	}
}

// An expired token counts as no usable login: the user re-logs in through the
// instance that owns the callback port and this one adopts the fresh token.
func TestCloudAuthStatusAdoptsTokenAfterRejectedToken(t *testing.T) {
	// The real token endpoint answers 404 for an unknown or expired token.
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer rejecting.Close()
	fresh := newCloudAuthAPIServer(t, "fresh-token", "alice")
	defer fresh.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = rejecting.URL
	s.cfg.Token = "expired-token"
	if status := s.cloudAuthStatus(t.Context()); status.Authenticated {
		t.Fatal("expired token should not be reported as authenticated")
	}

	// A server error is not an authentication answer: it must not clear the
	// rejected state and let a stale disk token look adoptable later.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusInternalServerError)
	}))
	defer failing.Close()
	s.cfg.ServerURL = failing.URL
	if status := s.cloudAuthStatus(t.Context()); status.Authenticated {
		t.Fatal("server error should not be reported as authenticated")
	}

	s.cfg.ServerURL = fresh.URL
	writeDiskCloudCredentialsWithRevision(t, "fresh-token", fresh.URL, 1)

	status := s.cloudAuthStatus(t.Context())
	if s.cfg.Token != "fresh-token" {
		t.Fatalf("in-memory token = %q, want fresh-token", s.cfg.Token)
	}
	if !status.Authenticated || status.User == nil {
		t.Fatalf("status = %+v, want the fresh login adopted", status)
	}
}

// A config from before server_url was tracked carries no community, so an
// instance pointing at a custom one must not adopt its token. The default
// community case is covered by TestCredentialsMatchCommunity.
func TestCloudAuthStatusDoesNotAdoptLegacyConfigForAnotherCommunity(t *testing.T) {
	api := newCloudAuthAPIServer(t, "legacy-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	writeDiskCloudToken(t, "legacy-token")

	status := s.cloudAuthStatus(t.Context())
	if s.cfg.Token != "" {
		t.Fatalf("in-memory token = %q, want no adoption for a custom community", s.cfg.Token)
	}
	if status.HasToken {
		t.Fatalf("status = %+v, want no token reported", status)
	}
}

// Adoption must not write config.json back: that file belongs to the instance
// that received the callback, and rewriting it would clobber its settings.
func TestCloudAuthStatusAdoptionLeavesConfigFileUntouched(t *testing.T) {
	api := newCloudAuthAPIServer(t, "adopted-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	writeDiskCloudCredentialsWithRevision(t, "adopted-token", api.URL, 1)

	cfgPath, err := config.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	if status := s.cloudAuthStatus(t.Context()); !status.Authenticated {
		t.Fatalf("status = %+v, want the adopted login", status)
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("config.json changed during adoption:\nbefore: %s\nafter:  %s", before, after)
	}
}

// A token the platform already rejected must not be adopted again when it is
// still the one on disk; otherwise every poll would retry the same dead login.
func TestCloudAuthStatusDoesNotReadoptRejectedDiskToken(t *testing.T) {
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "expired", http.StatusUnauthorized)
	}))
	defer rejecting.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = rejecting.URL
	s.cfg.Token = "expired-token"
	writeDiskCloudCredentials(t, "expired-token", rejecting.URL)

	if status := s.cloudAuthStatus(t.Context()); status.Authenticated {
		t.Fatal("expired token should not be reported as authenticated")
	}
	revision := s.cloudLoginRevision

	if status := s.cloudAuthStatus(t.Context()); status.Authenticated {
		t.Fatal("expired token should still not be reported as authenticated")
	}
	if s.cloudLoginRevision != revision {
		t.Fatal("rejected disk token was adopted again")
	}
	if s.cfg.Token != "expired-token" {
		t.Fatalf("in-memory token = %q, want the rejected token kept", s.cfg.Token)
	}
}

// A config written before auth_revision existed cannot say whether a cleared
// token means a logout or a partially written file, so a generation-0 clear
// must not clear the login this instance still holds. A newer generation does
// clear it; TestCloudAuthStatusFollowsNewerLogout covers that.
func TestCloudAuthStatusKeepsMemoryTokenWhenDiskTokenIsCleared(t *testing.T) {
	api := newCloudAuthAPIServer(t, "current-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.Token = "expired-token"
	s.cloudTokenInvalid = true
	writeDiskCloudCredentials(t, "", api.URL)

	status := s.cloudAuthStatus(t.Context())
	if s.cfg.Token != "expired-token" {
		t.Fatalf("in-memory token = %q, want it kept", s.cfg.Token)
	}
	if !status.HasToken {
		t.Fatalf("status = %+v, want the memory token still reported", status)
	}
}

// A login that completes here while a snapshot of the disk is being read must
// win over that older snapshot: the local login bumped the generation past it.
func TestAdoptExternalCredentialsKeepsLoginCompletedWhileReading(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	api := newCloudAuthAPIServer(t, "fresh-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	writeDiskCloudCredentialsWithRevision(t, "stale-token", api.URL, 5)

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.adoptExternalCloudCredentialsFrom(func() (config.CloudCredentials, error) {
			close(started)
			<-release
			// Taken before the local login below, so its generation is older.
			return config.CloudCredentials{Token: "stale-token", ServerURL: api.URL, AuthRevision: 5}, nil
		})
	}()

	<-started
	if err := s.saveCloudAccessToken("fresh-token"); err != nil {
		close(release)
		<-done
		t.Fatalf("saving a local login: %v", err)
	}
	if s.cfg.AuthRevision != 6 {
		close(release)
		<-done
		t.Fatalf("auth revision = %d, want 6", s.cfg.AuthRevision)
	}
	close(release)
	<-done

	if s.cfg.Token != "fresh-token" {
		t.Fatalf("in-memory token = %q, want the local login kept", s.cfg.Token)
	}
}

// A headless instance never polls the status endpoint, so the background loop
// has to adopt a login saved by another process.
func TestAdoptExternalCredentialsLoop(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	api := newCloudAuthAPIServer(t, "adopted-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL

	// adoptExternalCredentialsInterval is a package variable only so this test
	// can shrink it. Package tests do not run in parallel, and cancel() at
	// function exit stops the loop before the cleanup restores the variable. A
	// concurrent Save-vs-adoption race test cannot be added until cfg.Token has
	// a locked accessor: -race would flag the existing unlocked reads.
	previous := adoptExternalCredentialsInterval
	adoptExternalCredentialsInterval = 5 * time.Millisecond
	t.Cleanup(func() { adoptExternalCredentialsInterval = previous })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go s.adoptExternalCredentialsLoop(ctx)

	writeDiskCloudCredentialsWithRevision(t, "adopted-token", api.URL, 1)

	token := func() string {
		s.cloudLoginMu.Lock()
		defer s.cloudLoginMu.Unlock()
		return s.cfg.Token
	}
	deadline := time.Now().Add(5 * time.Second)
	for token() != "adopted-token" {
		if time.Now().After(deadline) {
			t.Fatalf("token = %q, want the login adopted by the loop", token())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Adoption cancels browser logins this instance has pending: the user already
// completed a login through the instance that owns the callback port.
func TestCloudAuthStatusAdoptionCancelsPendingBrowserLogin(t *testing.T) {
	api := newCloudAuthAPIServer(t, "adopted-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	writeDiskCloudCredentialsWithRevision(t, "adopted-token", api.URL, 1)

	s.cloudLoginMu.Lock()
	s.cloudLoginAttempts = map[string]browserLoginAttempt{
		"pending": {platform: api.URL, origin: "http://localhost:11435", expires: time.Now().Add(time.Minute)},
	}
	revision := s.cloudLoginRevision
	s.cloudLoginMu.Unlock()

	if status := s.cloudAuthStatus(t.Context()); !status.Authenticated {
		t.Fatalf("status = %+v, want the adopted login", status)
	}

	s.cloudLoginMu.Lock()
	defer s.cloudLoginMu.Unlock()
	if s.cloudLoginRevision != revision+1 {
		t.Fatalf("login revision = %d, want %d", s.cloudLoginRevision, revision+1)
	}
	if len(s.cloudLoginAttempts) != 0 {
		t.Fatalf("pending browser logins = %d, want them cancelled", len(s.cloudLoginAttempts))
	}
}

// Another instance still holding the old token can write the whole config back
// after a logout. The login generation it writes is not newer, so the logout
// must survive the next poll.
func TestCloudAuthStatusDoesNotAdoptTokenRewrittenAfterLogout(t *testing.T) {
	api := newCloudAuthAPIServer(t, "token-t", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.Token = "token-t"

	w := httptest.NewRecorder()
	s.handleCloudAuthTokenDelete(w, httptest.NewRequest(http.MethodDelete, "/api/cloud/auth/token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d", w.Code, http.StatusOK)
	}
	if s.cfg.Token != "" {
		t.Fatalf("token after logout = %q, want empty", s.cfg.Token)
	}
	revision := s.cfg.AuthRevision

	// A stale process writes its whole in-memory config back; Save merges the
	// generation, so the old token returns with the generation already applied.
	writeDiskCloudCredentialsWithRevision(t, "token-t", api.URL, revision)

	status := s.cloudAuthStatus(t.Context())
	if s.cfg.Token != "" {
		t.Fatalf("in-memory token = %q, want the logout kept", s.cfg.Token)
	}
	if status.Authenticated {
		t.Fatalf("status = %+v, want the logout kept", status)
	}
}

// A repeated logout with no token must not forget the token the first one
// cleared, or a build without auth_revision could write it back.
func TestCloudAuthTokenDeleteKeepsClearedTokenOnRepeat(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	s := newTestServer(t)
	s.cfg.Token = "token-t"

	w := httptest.NewRecorder()
	s.handleCloudAuthTokenDelete(w, httptest.NewRequest(http.MethodDelete, "/api/cloud/auth/token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("first logout status = %d, want %d", w.Code, http.StatusOK)
	}
	if s.cloudClearedToken != "token-t" {
		t.Fatalf("cleared token = %q, want token-t", s.cloudClearedToken)
	}

	w = httptest.NewRecorder()
	s.handleCloudAuthTokenDelete(w, httptest.NewRequest(http.MethodDelete, "/api/cloud/auth/token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("second logout status = %d, want %d", w.Code, http.StatusOK)
	}
	if s.cloudClearedToken != "token-t" {
		t.Fatalf("cleared token after a repeated logout = %q, want token-t kept", s.cloudClearedToken)
	}
}

// A key-only change does not say anything about the token, so it must not
// clear the rejected-token state.
func TestAdoptExternalCredentialsKeyOnlyKeepsRejectedState(t *testing.T) {
	s := newTestServer(t)
	s.cfg.Token = "token-t"
	s.cfg.OpenCSGAPIKey = "old-key"
	s.cfg.AuthRevision = 4
	s.cloudTokenInvalid = true
	writeDiskCloudCredentialsWithKey(t, "token-t", s.cfg.ServerURL, "new-key", 5)

	s.adoptExternalCloudCredentials()
	if s.cfg.OpenCSGAPIKey != "new-key" {
		t.Fatalf("manual API key = %q, want the adopted new-key", s.cfg.OpenCSGAPIKey)
	}
	if !s.cloudTokenInvalid {
		t.Fatal("key-only adoption cleared the rejected-token state")
	}
}

// A re-login in the other instance may reuse the same token value; only the
// newer generation distinguishes it from the stale write above.
func TestCloudAuthStatusAdoptsReloginWithSameToken(t *testing.T) {
	api := newCloudAuthAPIServer(t, "token-t", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.Token = "token-t"

	w := httptest.NewRecorder()
	s.handleCloudAuthTokenDelete(w, httptest.NewRequest(http.MethodDelete, "/api/cloud/auth/token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d", w.Code, http.StatusOK)
	}
	revision := s.cfg.AuthRevision

	writeDiskCloudCredentialsWithRevision(t, "token-t", api.URL, revision+1)

	status := s.cloudAuthStatus(t.Context())
	if s.cfg.Token != "token-t" {
		t.Fatalf("in-memory token = %q, want the re-login adopted", s.cfg.Token)
	}
	if !status.Authenticated {
		t.Fatalf("status = %+v, want the re-login adopted", status)
	}
}

// An instance running a build without auth_revision saves generation 0; its
// login must still be adopted, or mixed-version setups never log in again.
func TestCloudAuthStatusAdoptsLegacyInstanceLogin(t *testing.T) {
	api := newCloudAuthAPIServer(t, "legacy-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.AuthRevision = 2 // this instance already changed the login once
	writeDiskCloudCredentialsWithRevision(t, "legacy-token", api.URL, 0)

	status := s.cloudAuthStatus(t.Context())
	if s.cfg.Token != "legacy-token" {
		t.Fatalf("in-memory token = %q, want the legacy instance's login adopted", s.cfg.Token)
	}
	if !status.Authenticated {
		t.Fatalf("status = %+v, want the adopted login", status)
	}
}

// The mixed-version fallback must not undo a logout here: the older build
// writes back the token this process just cleared, with no generation to tell
// it apart from a fresh login.
func TestCloudAuthStatusDoesNotAdoptClearedTokenFromLegacyInstance(t *testing.T) {
	api := newCloudAuthAPIServer(t, "token-t", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.Token = "token-t"

	w := httptest.NewRecorder()
	s.handleCloudAuthTokenDelete(w, httptest.NewRequest(http.MethodDelete, "/api/cloud/auth/token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d", w.Code, http.StatusOK)
	}
	if s.cloudClearedToken != "token-t" {
		t.Fatalf("cleared token = %q, want token-t", s.cloudClearedToken)
	}

	// An older build saves its whole config and drops auth_revision entirely.
	writeDiskCloudCredentialsWithRevision(t, "token-t", api.URL, 0)

	status := s.cloudAuthStatus(t.Context())
	if s.cfg.Token != "" {
		t.Fatalf("in-memory token = %q, want the logout kept", s.cfg.Token)
	}
	if status.Authenticated {
		t.Fatalf("status = %+v, want the logout kept", status)
	}
}

func TestRecordCloudTokenValidation(t *testing.T) {
	s := newTestServer(t)
	s.cfg.Token = "current-token"

	tests := []struct {
		name  string
		err   error
		start bool
		want  bool
	}{
		{"unauthorized", &csghub.HTTPError{StatusCode: http.StatusUnauthorized}, false, true},
		{"forbidden", &csghub.HTTPError{StatusCode: http.StatusForbidden}, false, true},
		{"not found", &csghub.HTTPError{StatusCode: http.StatusNotFound}, false, true},
		{"server error keeps rejection", &csghub.HTTPError{StatusCode: http.StatusInternalServerError}, true, true},
		{"transport error keeps rejection", errors.New("dial tcp: connection refused"), true, true},
		{"server error keeps a valid login", &csghub.HTTPError{StatusCode: http.StatusInternalServerError}, false, false},
		{"accepted", nil, true, false},
	}
	for _, tt := range tests {
		s.cloudTokenInvalid = tt.start
		s.recordCloudTokenValidation("current-token", tt.err)
		if s.cloudTokenInvalid != tt.want {
			t.Fatalf("%s: cloudTokenInvalid = %v, want %v", tt.name, s.cloudTokenInvalid, tt.want)
		}
	}

	s.cloudTokenInvalid = true
	s.recordCloudTokenValidation("other-token", nil)
	if !s.cloudTokenInvalid {
		t.Fatal("a stale check cleared the flag for the current token")
	}
}

func TestCredentialsMatchCommunity(t *testing.T) {
	tests := []struct {
		name, disk, local string
		want              bool
	}{
		{"same", "https://hub.opencsg.com", "https://hub.opencsg.com", true},
		{"trailing slash", "https://hub.opencsg.com/", "https://hub.opencsg.com", true},
		{"surrounding spaces", "  https://hub.opencsg.com  ", "https://hub.opencsg.com", true},
		{"scheme and host case", "HTTPS://HUB.OpenCSG.com", "https://hub.opencsg.com", true},
		{"explicit default port", "https://hub.opencsg.com:443", "https://hub.opencsg.com", true},
		{"path trailing slash", "https://hub.opencsg.com/csghub/", "https://hub.opencsg.com/csghub", true},
		{"different port", "https://hub.opencsg.com:8443", "https://hub.opencsg.com", false},
		{"legacy default", "", config.DefaultServerURL, true},
		{"legacy default with slash", "", config.DefaultServerURL + "/", true},
		{"legacy custom", "", "https://other.example.com", false},
		{"different community", "https://other.example.com", "https://hub.opencsg.com", false},
	}
	for _, tt := range tests {
		if got := credentialsMatchCommunity(tt.disk, tt.local); got != tt.want {
			t.Fatalf("%s: credentialsMatchCommunity(%q, %q) = %v, want %v", tt.name, tt.disk, tt.local, got, tt.want)
		}
	}
}

func TestHandleCloudAuthTokenSaveAndDelete(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	api := newCloudAuthAPIServer(t, "test-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.OpenCSGAPIKey = "previous-account-key"

	saveReq := httptest.NewRequest(http.MethodPost, "/api/cloud/auth/token", strings.NewReader(`{"token":" test-token "}`))
	w := httptest.NewRecorder()
	s.handleCloudAuthTokenSave(w, saveReq)

	if w.Code != http.StatusOK {
		t.Fatalf("save status = %d, want %d", w.Code, http.StatusOK)
	}
	if s.cfg.Token != "test-token" {
		t.Fatalf("saved token = %q, want %q", s.cfg.Token, "test-token")
	}
	if s.cfg.OpenCSGAPIKey != "" {
		t.Fatal("new login retained the previous account's manual key")
	}
	cfgPath, err := config.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "previous-account-key") {
		t.Fatal("previous account's key was retained in config.json")
	}

	var saveResp cloudAuthStatus
	if err := json.NewDecoder(w.Body).Decode(&saveResp); err != nil {
		t.Fatalf("decode save response: %v", err)
	}
	if !saveResp.Authenticated {
		t.Fatalf("Authenticated after save = false, want true")
	}
	if saveResp.User == nil || saveResp.User.Username != "alice" {
		t.Fatalf("saved user = %#v, want alice", saveResp.User)
	}

	s.cfg.OpenCSGAPIKey = "session-manual-key"
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/cloud/auth/token", nil)
	w = httptest.NewRecorder()
	s.handleCloudAuthTokenDelete(w, deleteReq)

	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d", w.Code, http.StatusOK)
	}
	if s.cfg.Token != "" {
		t.Fatalf("token after delete = %q, want empty", s.cfg.Token)
	}
	if s.cfg.OpenCSGAPIKey != "" {
		t.Fatal("logout retained the session's manual key")
	}

	var deleteResp cloudAuthStatus
	if err := json.NewDecoder(w.Body).Decode(&deleteResp); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if deleteResp.HasToken {
		t.Fatalf("HasToken after delete = true, want false")
	}
	if deleteResp.Authenticated {
		t.Fatalf("Authenticated after delete = true, want false")
	}
	if deleteResp.HasAPIKey {
		t.Fatal("signed-out status still reports an API key")
	}
}

func TestHandleCloudAuthCallback(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	s := newTestServer(t)
	s.cfg.OpenCSGAPIKey = "previous-account-key"

	saveReq := httptest.NewRequest(http.MethodGet, "/api/cloud/auth/callback?token=%20test-token%20", nil)
	w := httptest.NewRecorder()
	s.handleCloudAuthCallback(w, saveReq)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(w.Body.String(), "Login complete") {
		t.Fatalf("body = %q, want success page", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "window.close()") {
		t.Fatalf("body = %q, want success page to close the tab", w.Body.String())
	}
	if s.cfg.Token != "test-token" {
		t.Fatalf("saved token = %q, want %q", s.cfg.Token, "test-token")
	}
	if s.cfg.OpenCSGAPIKey != "" {
		t.Fatal("SSO callback retained the previous account's manual key")
	}
}

func TestHandleCloudAuthCallbackMissingToken(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/cloud/auth/callback", nil)
	w := httptest.NewRecorder()
	s.handleCloudAuthCallback(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "Login failed") || !strings.Contains(w.Body.String(), "missing_token") {
		t.Fatalf("body = %q, want failure page with missing_token", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "window.close()") {
		t.Fatalf("body = %q, want failure page to stay open for reading", w.Body.String())
	}
}

func TestHandleCloudAPIKeySaveAndDelete(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	s := newTestServer(t)

	saveReq := httptest.NewRequest(http.MethodPost, "/api/cloud/api-key", strings.NewReader(`{"api_key":" manual-key "}`))
	w := httptest.NewRecorder()
	s.handleCloudAPIKeySave(w, saveReq)

	if w.Code != http.StatusOK {
		t.Fatalf("save status = %d, want %d", w.Code, http.StatusOK)
	}
	if s.cfg.OpenCSGAPIKey != "manual-key" {
		t.Fatalf("saved API key = %q, want manual-key", s.cfg.OpenCSGAPIKey)
	}
	// The key belongs to the account session, so saving it moves the login
	// generation other instances compare against.
	if s.cfg.AuthRevision != 1 {
		t.Fatalf("auth revision after save = %d, want 1", s.cfg.AuthRevision)
	}

	var saveResp cloudAuthStatus
	if err := json.NewDecoder(w.Body).Decode(&saveResp); err != nil {
		t.Fatalf("decode save response: %v", err)
	}
	if !saveResp.HasAPIKey || saveResp.APIKeySource != "manual" {
		t.Fatalf("API key status = has:%v source:%q, want manual key", saveResp.HasAPIKey, saveResp.APIKeySource)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/cloud/api-key", nil)
	w = httptest.NewRecorder()
	s.handleCloudAPIKeyDelete(w, deleteReq)

	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d", w.Code, http.StatusOK)
	}
	if s.cfg.OpenCSGAPIKey != "" {
		t.Fatalf("API key after delete = %q, want empty", s.cfg.OpenCSGAPIKey)
	}
	if s.cfg.AuthRevision != 2 {
		t.Fatalf("auth revision after delete = %d, want 2", s.cfg.AuthRevision)
	}
}

func TestNewCloudEngineUsesBuiltinAPIKey(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/token/access-token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"msg": "OK",
				"data": map[string]any{
					"token":     "access-token",
					"user_name": "alice",
					"user_uuid": "user-1",
				},
			})
		case "/api/v1/user/alice":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"msg": "OK",
				"data": map[string]any{
					"username": "alice",
					"uuid":     "user-1",
				},
			})
		case "/api/v1/namespaces/user-1/apikeys/builtin":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"msg": "OK",
				"data": map[string]any{
					"api_key": "builtin-key",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()

	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer builtin-key" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer builtin-key")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": "ok"},
			}},
		})
	}))
	defer modelServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = authServer.URL
	s.cfg.OpenCSGAPIKey = "stale-key-that-would-return-401"
	if err := s.saveCloudAccessToken("access-token"); err != nil {
		t.Fatal(err)
	}
	s.cloud = cloud.NewService(modelServer.URL)

	eng, err := s.newCloudEngine(context.Background(), "cloud/model")
	if err != nil {
		t.Fatalf("newCloudEngine error: %v", err)
	}
	got, err := eng.Generate(context.Background(), "hi", inference.DefaultOptions(), nil)
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("response = %q, want ok", got)
	}
}

func TestNewCloudEngineResolvesProviderModelAlias(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviderModelAllowlist)

	var upstreamModel string
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		upstreamModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": "ok"},
			}},
		})
	}))
	defer modelServer.Close()

	s := newTestServer(t)
	s.cfg.OpenCSGAPIKey = "test-key"
	s.cloud = cloud.NewService(modelServer.URL)
	if err := config.AddProviderModelSelection(config.DefaultCloudProviderName, config.ProviderModelSelection{
		Model:         "cloud-alias",
		OriginalModel: "cloud/original",
	}); err != nil {
		t.Fatalf("save cloud model alias: %v", err)
	}

	eng, err := s.newCloudEngine(context.Background(), "cloud-alias")
	if err != nil {
		t.Fatalf("newCloudEngine error: %v", err)
	}
	if _, err := eng.Generate(context.Background(), "hi", inference.DefaultOptions(), nil); err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if upstreamModel != "cloud/original" {
		t.Fatalf("upstream model = %q, want cloud/original", upstreamModel)
	}
}
