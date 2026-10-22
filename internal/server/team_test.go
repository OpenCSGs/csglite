package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/ee/team"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/internal/license/licensetest"
	"github.com/opencsgs/csglite/pkg/api"
)

const remoteAddr = "192.0.2.10:40000"

// teamClient drives the full route stack as one remote browser would,
// keeping the session cookie between calls.
type teamClient struct {
	t       *testing.T
	handler http.Handler
	cookie  *http.Cookie
	apiKey  string
	headers map[string]string
	remote  string
}

func newTeamClient(t *testing.T, s *Server) *teamClient {
	return &teamClient{t: t, handler: s.routes(), remote: remoteAddr}
}

func (c *teamClient) do(method, path string, body any) *httptest.ResponseRecorder {
	c.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = c.remote
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	for name, value := range c.headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	return rec
}

func (c *teamClient) login(username, password string) {
	c.t.Helper()
	rec := c.do(http.MethodPost, "/api/auth/login", api.AuthLoginRequest{Username: username, Password: password})
	if rec.Code != http.StatusOK {
		c.t.Fatalf("login %s: %d %s", username, rec.Code, rec.Body.String())
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionCookie {
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
				c.t.Fatalf("session cookie attributes: %+v", cookie)
			}
			c.cookie = cookie
			return
		}
	}
	c.t.Fatalf("login %s set no session cookie", username)
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int, what string) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("%s: status %d, want %d: %s", what, rec.Code, want, rec.Body.String())
	}
}

func expectCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code, what string) {
	t.Helper()
	expectStatus(t, rec, status, what)
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Code != code {
		t.Fatalf("%s: code %q, want %q: %s", what, body.Code, code, rec.Body.String())
	}
}

// licenseTeamManagement installs an Enterprise license; extra is the
// License.Extra JSON, empty for none.
func licenseTeamManagement(t *testing.T, s *Server, extra string) {
	t.Helper()
	key := installTestLicenseKeys(t, s)
	payload := licensetest.Payload(time.Now())
	payload.Extra = extra
	if _, err := s.license.Install(licensetest.Encode(t, key, payload)); err != nil {
		t.Fatal(err)
	}
}

func createTeamUser(t *testing.T, c *teamClient, req api.TeamUserCreateRequest) api.TeamUser {
	t.Helper()
	rec := c.do(http.MethodPost, "/api/team/users", req)
	expectStatus(t, rec, http.StatusCreated, "create user "+req.Username)
	var user api.TeamUser
	if err := json.Unmarshal(rec.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	return user
}

// newAccountsServer returns a licensed server whose first administrator,
// "admin", was created on the machine itself, and a remote client signed in
// as that administrator.
func newAccountsServer(t *testing.T) (*Server, *teamClient) {
	t.Helper()
	s := newTestServer(t)
	licenseTeamManagement(t, s, "")
	local := newTeamClient(t, s)
	local.remote = "127.0.0.1:50000"
	createTeamUser(t, local, api.TeamUserCreateRequest{Username: "admin", Password: "admin-password"})
	admin := newTeamClient(t, s)
	admin.login("admin", "admin-password")
	return s, admin
}

// inviteUser has admin invite a user, accepts the invitation as that user,
// and returns the user and a client signed in as them.
func inviteUser(t *testing.T, s *Server, admin *teamClient, username, role string) (api.TeamUser, *teamClient) {
	t.Helper()
	invited := createTeamUser(t, admin, api.TeamUserCreateRequest{Username: username, Role: role, Invite: true})
	if !invited.Pending || invited.InviteToken == "" || invited.InviteExpiresAt == nil {
		t.Fatalf("invited user %+v", invited)
	}
	c := newTeamClient(t, s)
	rec := c.do(http.MethodPost, "/api/auth/invite/"+invited.InviteToken, api.TeamInviteAcceptRequest{Password: username + "-password"})
	expectStatus(t, rec, http.StatusOK, "accept invitation for "+username)
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionCookie {
			c.cookie = cookie
		}
	}
	if c.cookie == nil {
		t.Fatalf("accepting the invitation for %s did not sign in", username)
	}
	return invited, c
}

func TestAccountsAreOffUntilTheFirstUser(t *testing.T) {
	s := newTestServer(t)
	c := newTeamClient(t, s)

	rec := c.do(http.MethodGet, "/api/auth/session", nil)
	expectStatus(t, rec, http.StatusOK, "session")
	var session api.AuthSessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &session)
	if session.AccountsEnabled || session.FeatureLicensed {
		t.Fatalf("unlicensed fresh instance session = %+v", session)
	}
	expectStatus(t, c.do(http.MethodGet, "/api/settings", nil), http.StatusOK, "settings with accounts off")

	expectStatus(t, c.do(http.MethodPost, "/api/auth/login", api.AuthLoginRequest{Username: "admin", Password: "x"}),
		http.StatusConflict, "login with accounts off")
}

// The community edition includes one administrator account, so any instance
// can require a login; more users, and managing them, need the license.
func TestCommunityEditionHasOneAdministrator(t *testing.T) {
	s := newTestServer(t)
	local := newTeamClient(t, s)
	local.remote = "127.0.0.1:50000"
	created := createTeamUser(t, local, api.TeamUserCreateRequest{Username: "admin", Password: "admin-password"})
	if created.Role != "admin" {
		t.Fatalf("first user role = %q, want admin", created.Role)
	}

	anon := newTeamClient(t, s)
	expectCode(t, anon.do(http.MethodGet, "/api/tags", nil), http.StatusUnauthorized, loginRequiredCode, "anonymous request once the administrator exists")
	admin := newTeamClient(t, s)
	admin.login("admin", "admin-password")
	expectStatus(t, admin.do(http.MethodGet, "/api/tags", nil), http.StatusOK, "administrator request")

	rec := admin.do(http.MethodGet, "/api/team/users", nil)
	expectStatus(t, rec, http.StatusOK, "list users")
	var list api.TeamUsersResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Users) != 1 || list.MaxUsers != 1 {
		t.Fatalf("community user list = %+v", list)
	}

	rec = admin.do(http.MethodPost, "/api/team/users", api.TeamUserCreateRequest{Username: "bob", Role: "member", Invite: true})
	expectCode(t, rec, http.StatusForbidden, licenseErrorCode, "second user without a license")
	expectCode(t, admin.do(http.MethodGet, "/api/team/teams", nil), http.StatusForbidden, licenseErrorCode, "teams without a license")
	expectCode(t, admin.do(http.MethodPut, "/api/team/users/"+created.ID, api.TeamUserUpdateRequest{}), http.StatusForbidden, licenseErrorCode, "editing users without a license")
	expectStatus(t, admin.do(http.MethodPut, "/api/auth/password", api.AuthPasswordChangeRequest{CurrentPassword: "admin-password", NewPassword: "new-admin-pass"}),
		http.StatusOK, "the administrator changes their own password")
}

func TestSignInIsRequiredOnceAccountsExist(t *testing.T) {
	s, admin := newAccountsServer(t)
	anon := newTeamClient(t, s)

	for _, path := range []string{"/api/settings", "/api/tags", "/v1/models", "/api/license"} {
		expectCode(t, anon.do(http.MethodGet, path, nil), http.StatusUnauthorized, loginRequiredCode, "anonymous GET "+path)
	}
	expectCode(t, anon.do(http.MethodPost, "/v1/chat/completions", map[string]any{"model": "m"}), http.StatusUnauthorized, loginRequiredCode, "anonymous inference")
	expectStatus(t, anon.do(http.MethodGet, "/api/health", nil), http.StatusOK, "health")
	if rec := anon.do(http.MethodGet, "/", nil); rec.Code == http.StatusUnauthorized {
		t.Fatalf("the web UI shell must load so the login page can show: %d", rec.Code)
	}

	rec := anon.do(http.MethodGet, "/api/auth/session", nil)
	var session api.AuthSessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &session)
	if !session.AccountsEnabled || session.Authenticated {
		t.Fatalf("anonymous session = %+v", session)
	}

	expectStatus(t, admin.do(http.MethodGet, "/api/settings", nil), http.StatusOK, "admin settings")
	rec = admin.do(http.MethodGet, "/api/auth/session", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &session)
	if !session.Authenticated || session.User == nil || session.User.Role != "admin" || session.LocalOperator {
		t.Fatalf("admin session = %+v", session)
	}

	// Signing out ends the session.
	expectStatus(t, admin.do(http.MethodPost, "/api/auth/logout", nil), http.StatusOK, "logout")
	expectCode(t, admin.do(http.MethodGet, "/api/settings", nil), http.StatusUnauthorized, loginRequiredCode, "after logout")
}

func TestLocalOperatorNeedsNoSignInButAProxiedRequestDoes(t *testing.T) {
	s, _ := newAccountsServer(t)
	local := newTeamClient(t, s)
	local.remote = "127.0.0.1:50000"
	expectStatus(t, local.do(http.MethodGet, "/api/settings", nil), http.StatusOK, "local operator")
	expectStatus(t, local.do(http.MethodGet, "/api/team/users", nil), http.StatusOK, "local operator lists users")

	proxied := newTeamClient(t, s)
	proxied.remote = "127.0.0.1:50001"
	proxied.headers = map[string]string{"X-Forwarded-For": "203.0.113.7"}
	expectCode(t, proxied.do(http.MethodGet, "/api/settings", nil), http.StatusUnauthorized, loginRequiredCode, "request through a local reverse proxy")
}

func TestLoginFailuresAreThrottled(t *testing.T) {
	s, _ := newAccountsServer(t)
	c := newTeamClient(t, s)
	for i := 0; i < team.DefaultMaxFailedLogins; i++ {
		expectStatus(t, c.do(http.MethodPost, "/api/auth/login", api.AuthLoginRequest{Username: "admin", Password: "wrong-password"}),
			http.StatusUnauthorized, "wrong password")
	}
	expectStatus(t, c.do(http.MethodPost, "/api/auth/login", api.AuthLoginRequest{Username: "admin", Password: "admin-password"}),
		http.StatusTooManyRequests, "right password while locked out")
}

func TestRolesLimitWhatUsersCanDo(t *testing.T) {
	s, admin := newAccountsServer(t)
	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID: "p1", Name: "Upstream", BaseURL: "https://upstream.example.com/v1", APIKey: "sk-upstream-secret", Enabled: true,
		Headers: []config.ProviderHeader{{Name: "X-Org", Value: "org-secret"}},
	}}); err != nil {
		t.Fatal(err)
	}
	_, member := inviteUser(t, s, admin, "member", "member")
	_, auditor := inviteUser(t, s, admin, "auditor", "auditor")

	expectStatus(t, member.do(http.MethodGet, "/api/tags", nil), http.StatusOK, "member reads models")
	expectCode(t, member.do(http.MethodGet, "/api/team/users", nil), http.StatusForbidden, permissionDeniedCode, "member lists users")
	expectCode(t, member.do(http.MethodPost, "/api/settings", map[string]any{}), http.StatusForbidden, permissionDeniedCode, "member changes settings")
	expectCode(t, member.do(http.MethodGet, "/api/observability/requests", nil), http.StatusForbidden, permissionDeniedCode, "member reads other people's prompts")

	rec := member.do(http.MethodGet, "/api/providers?source=config", nil)
	expectStatus(t, rec, http.StatusOK, "member lists providers")
	if strings.Contains(rec.Body.String(), "sk-upstream-secret") || strings.Contains(rec.Body.String(), "org-secret") {
		t.Fatalf("member sees upstream credentials: %s", rec.Body.String())
	}
	rec = admin.do(http.MethodGet, "/api/providers?source=config", nil)
	if !strings.Contains(rec.Body.String(), "sk-upstream-secret") {
		t.Fatalf("admin must still see upstream credentials: %s", rec.Body.String())
	}

	expectStatus(t, auditor.do(http.MethodGet, "/api/tags", nil), http.StatusOK, "auditor reads models")
	expectStatus(t, auditor.do(http.MethodGet, "/api/team/users", nil), http.StatusOK, "auditor lists users")
	expectStatus(t, auditor.do(http.MethodGet, "/api/observability/requests", nil), http.StatusOK, "auditor reads request logs")
	expectCode(t, auditor.do(http.MethodGet, "/api/cluster/token", nil), http.StatusForbidden, permissionDeniedCode, "auditor reads the cluster join token")
	expectCode(t, auditor.do(http.MethodPost, "/api/chat", map[string]any{"model": "m"}), http.StatusForbidden, permissionDeniedCode, "auditor chats")
	expectCode(t, auditor.do(http.MethodPost, "/api/team/teams", api.TeamGroupRequest{Name: ptr("x")}), http.StatusForbidden, permissionDeniedCode, "auditor creates a team")
	rec = auditor.do(http.MethodGet, "/api/providers?source=config", nil)
	if strings.Contains(rec.Body.String(), "sk-upstream-secret") {
		t.Fatalf("auditor sees upstream credentials: %s", rec.Body.String())
	}
}

func TestCrossOriginChangesWithACookieAreRefused(t *testing.T) {
	s, admin := newAccountsServer(t)
	admin.headers = map[string]string{"Origin": "http://evil.example.com"}
	expectCode(t, admin.do(http.MethodPost, "/api/team/teams", api.TeamGroupRequest{Name: ptr("x")}), http.StatusForbidden, permissionDeniedCode, "cross-origin change")
	admin.headers = map[string]string{"Origin": "http://example.com"}
	expectStatus(t, admin.do(http.MethodPost, "/api/team/teams", api.TeamGroupRequest{Name: ptr("x")}), http.StatusCreated, "same-origin change")
	_ = s
}

func TestConversationsAndImagesArePrivateToTheirOwner(t *testing.T) {
	s, admin := newAccountsServer(t)
	_, member := inviteUser(t, s, admin, "member", "member")

	rec := member.do(http.MethodPost, "/api/conversations", api.Conversation{Title: "member secret"})
	expectStatus(t, rec, http.StatusCreated, "member creates a conversation")
	var conv api.Conversation
	_ = json.Unmarshal(rec.Body.Bytes(), &conv)
	if conv.OwnerID == "" {
		t.Fatalf("conversation has no owner: %+v", conv)
	}

	rec = admin.do(http.MethodGet, "/api/conversations", nil)
	if strings.Contains(rec.Body.String(), "member secret") {
		t.Fatalf("admin lists the member's conversation: %s", rec.Body.String())
	}
	expectStatus(t, admin.do(http.MethodGet, "/api/conversations/"+conv.ID, nil), http.StatusNotFound, "admin opens the member's conversation")
	expectStatus(t, admin.do(http.MethodDelete, "/api/conversations/"+conv.ID, nil), http.StatusNotFound, "admin deletes the member's conversation")
	rec = member.do(http.MethodGet, "/api/conversations", nil)
	if !strings.Contains(rec.Body.String(), "member secret") {
		t.Fatalf("member does not see their own conversation: %s", rec.Body.String())
	}

	job, err := s.createImageGenerationJob(api.OpenAIImagesGenerationRequest{Model: "missing-model", Prompt: "p"}, conv.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	expectStatus(t, admin.do(http.MethodGet, "/api/images/jobs/"+job.id, nil), http.StatusNotFound, "admin opens the member's image job")
	expectStatus(t, member.do(http.MethodGet, "/api/images/jobs/"+job.id, nil), http.StatusOK, "member opens their image job")
}

func TestAPIKeysBelongToTheirCreator(t *testing.T) {
	s, admin := newAccountsServer(t)
	member, mc := inviteUser(t, s, admin, "member", "member")
	legacy, legacyPlain, err := s.apiKeys.Create("legacy", "")
	if err != nil {
		t.Fatal(err)
	}

	rec := mc.do(http.MethodPost, "/api/api-keys", api.APIKeyCreateRequest{Name: "mine"})
	expectStatus(t, rec, http.StatusCreated, "member creates a key")
	var created api.APIKeyCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Key.OwnerID != member.ID || created.Key.OwnerName != "member" {
		t.Fatalf("member key owner = %+v", created.Key)
	}

	var listed api.APIKeysResponse
	_ = json.Unmarshal(mc.do(http.MethodGet, "/api/api-keys", nil).Body.Bytes(), &listed)
	if len(listed.Keys) != 1 || listed.Keys[0].ID != created.Key.ID {
		t.Fatalf("member sees keys %+v, want only their own", listed.Keys)
	}
	expectStatus(t, mc.do(http.MethodDelete, "/api/api-keys/"+legacy.ID, nil), http.StatusNotFound, "member deletes someone else's key")
	_ = json.Unmarshal(admin.do(http.MethodGet, "/api/api-keys", nil).Body.Bytes(), &listed)
	if len(listed.Keys) != 2 {
		t.Fatalf("admin sees %d keys, want 2", len(listed.Keys))
	}

	// The key acts with its owner's role: it may run inference but not
	// administer, and stops working when the owner is disabled.
	keyClient := newTeamClient(t, s)
	keyClient.apiKey = created.APIKey
	expectStatus(t, keyClient.do(http.MethodGet, "/api/tags", nil), http.StatusOK, "member key reads models")
	expectCode(t, keyClient.do(http.MethodPost, "/api/team/teams", api.TeamGroupRequest{Name: ptr("x")}), http.StatusForbidden, permissionDeniedCode, "member key administers")
	legacyClient := newTeamClient(t, s)
	legacyClient.apiKey = legacyPlain
	expectStatus(t, legacyClient.do(http.MethodGet, "/api/tags", nil), http.StatusOK, "pre-accounts key reads models")
	expectCode(t, legacyClient.do(http.MethodPost, "/api/settings", map[string]any{}), http.StatusForbidden, permissionDeniedCode, "pre-accounts key administers")

	disabled := true
	expectStatus(t, admin.do(http.MethodPut, "/api/team/users/"+member.ID, api.TeamUserUpdateRequest{Disabled: &disabled}), http.StatusOK, "disable member")
	expectCode(t, keyClient.do(http.MethodGet, "/api/tags", nil), http.StatusUnauthorized, loginRequiredCode, "disabled owner's key")

	// Deleting the user deletes their keys.
	expectStatus(t, admin.do(http.MethodDelete, "/api/team/users/"+member.ID, nil), http.StatusOK, "delete member")
	state, _ := s.apiKeys.State()
	for _, key := range state.Keys {
		if key.OwnerID == member.ID {
			t.Fatalf("key %s outlived its owner", key.ID)
		}
	}
}

func TestSeatLimitComesFromTheLicense(t *testing.T) {
	s := newTestServer(t)
	licenseTeamManagement(t, s, `{"limits": {"quota.lite.max_users": 2}}`)
	c := newTeamClient(t, s)
	c.remote = "127.0.0.1:50000"
	createTeamUser(t, c, api.TeamUserCreateRequest{Username: "admin", Password: "admin-password"})
	createTeamUser(t, c, api.TeamUserCreateRequest{Username: "second", Invite: true})
	rec := c.do(http.MethodPost, "/api/team/users", api.TeamUserCreateRequest{Username: "third", Password: "third-password"})
	expectCode(t, rec, http.StatusForbidden, licenseErrorCode, "third user over a two-seat license")
	var body seatLimitResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Feature != license.QuotaMaxUsers.Key || body.Limit != 2 || body.Current != 2 {
		t.Fatalf("seat limit response %+v", body)
	}
	var users api.TeamUsersResponse
	_ = json.Unmarshal(c.do(http.MethodGet, "/api/team/users", nil).Body.Bytes(), &users)
	if users.MaxUsers != 2 || len(users.Users) != 2 {
		t.Fatalf("users response %+v", users)
	}
}

func TestAccountsKeepProtectingTheInstanceAfterTheLicenseLapses(t *testing.T) {
	s, admin := newAccountsServer(t)
	if _, err := s.license.Remove(); err != nil {
		t.Fatal(err)
	}
	anon := newTeamClient(t, s)
	expectCode(t, anon.do(http.MethodGet, "/api/settings", nil), http.StatusUnauthorized, loginRequiredCode, "anonymous after the license lapsed")
	expectStatus(t, admin.do(http.MethodGet, "/api/settings", nil), http.StatusOK, "signed-in admin after the license lapsed")
	expectCode(t, admin.do(http.MethodPost, "/api/team/users", api.TeamUserCreateRequest{Username: "late", Password: "late-password"}),
		http.StatusForbidden, licenseErrorCode, "creating a user after the license lapsed")
	fresh := newTeamClient(t, s)
	fresh.login("admin", "admin-password")
}

func TestTheLastAdministratorCannotBeRemoved(t *testing.T) {
	s, admin := newAccountsServer(t)
	var users api.TeamUsersResponse
	_ = json.Unmarshal(admin.do(http.MethodGet, "/api/team/users", nil).Body.Bytes(), &users)
	id := users.Users[0].ID
	expectStatus(t, admin.do(http.MethodDelete, "/api/team/users/"+id, nil), http.StatusConflict, "delete the only admin")
	role := "member"
	expectStatus(t, admin.do(http.MethodPut, "/api/team/users/"+id, api.TeamUserUpdateRequest{Role: &role}), http.StatusConflict, "demote the only admin")
	_ = s
}

func TestPasswordChange(t *testing.T) {
	s, admin := newAccountsServer(t)
	expectStatus(t, admin.do(http.MethodPut, "/api/auth/password", api.AuthPasswordChangeRequest{CurrentPassword: "nope-nope", NewPassword: "new-password"}),
		http.StatusBadRequest, "wrong current password")
	expectStatus(t, admin.do(http.MethodPut, "/api/auth/password", api.AuthPasswordChangeRequest{CurrentPassword: "admin-password", NewPassword: "new-password"}),
		http.StatusOK, "change password")
	expectStatus(t, admin.do(http.MethodGet, "/api/settings", nil), http.StatusOK, "the session that changed the password survives")
	other := newTeamClient(t, s)
	other.login("admin", "new-password")
}

func ptr[T any](v T) *T { return &v }

func TestAKeyFromBeforeAccountsCannotMintKeys(t *testing.T) {
	s, _ := newAccountsServer(t)
	_, plain, err := s.apiKeys.Create("legacy", "")
	if err != nil {
		t.Fatal(err)
	}
	c := newTeamClient(t, s)
	c.apiKey = plain
	expectCode(t, c.do(http.MethodPost, "/api/api-keys", api.APIKeyCreateRequest{Name: "more"}), http.StatusForbidden, permissionDeniedCode, "legacy key creating a key")
}

func TestTheFirstAdministratorFromAnotherMachineNeedsTheSetupToken(t *testing.T) {
	s := newTestServer(t)
	licenseTeamManagement(t, s, "")
	remote := newTeamClient(t, s)
	req := api.TeamUserCreateRequest{Username: "admin", Password: "admin-password"}
	expectCode(t, remote.do(http.MethodPost, "/api/team/users", req), http.StatusForbidden, setupTokenRequiredCode, "remote bootstrap without a token")
	data, err := os.ReadFile(s.setupTokenPath())
	if err != nil {
		t.Fatalf("setup token file: %v", err)
	}
	if info, _ := os.Stat(s.setupTokenPath()); runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("setup token file mode %v", info.Mode().Perm())
	}
	req.SetupToken = "wrong"
	expectCode(t, remote.do(http.MethodPost, "/api/team/users", req), http.StatusForbidden, setupTokenRequiredCode, "remote bootstrap with a wrong token")
	req.SetupToken = strings.TrimSpace(string(data))
	expectStatus(t, remote.do(http.MethodPost, "/api/team/users", req), http.StatusCreated, "remote bootstrap with the token")
	if _, err := os.Stat(s.setupTokenPath()); !os.IsNotExist(err) {
		t.Fatalf("the setup token outlived bootstrap: %v", err)
	}
	remote.login("admin", "admin-password")
	var session api.AuthSessionResponse
	_ = json.Unmarshal(remote.do(http.MethodGet, "/api/auth/session", nil).Body.Bytes(), &session)
	if session.User == nil || session.User.MustChangePassword {
		t.Fatalf("the first administrator chose their own password: %+v", session.User)
	}
}

func TestEnvironmentCreatesTheFirstAdministrator(t *testing.T) {
	s := newTestServer(t)
	licenseTeamManagement(t, s, "")
	passwordFile := filepath.Join(t.TempDir(), "admin-password")
	if err := os.WriteFile(passwordFile, []byte("from-a-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envAdminUsername, "root")
	t.Setenv(envAdminPasswordFile, passwordFile)
	s.bootstrapAccounts()
	if !s.accountsEnabled() {
		t.Fatal("the environment did not create an administrator")
	}
	c := newTeamClient(t, s)
	c.login("root", "from-a-secret")
	var settings api.TeamSettings
	_ = json.Unmarshal(c.do(http.MethodGet, "/api/team/settings", nil).Body.Bytes(), &settings)
	if !settings.BootstrapEnvSet {
		t.Fatalf("settings must warn that the bootstrap password is still set: %+v", settings)
	}

	// Once accounts exist the variables are ignored, so a restart never
	// resets the password.
	t.Setenv(envAdminPasswordFile, "")
	t.Setenv(envAdminPassword, "another-password")
	s.bootstrapAccounts()
	c.login("root", "from-a-secret")
}

func TestEnvironmentBootstrapWorksWithoutALicense(t *testing.T) {
	s := newTestServer(t)
	t.Setenv(envAdminPassword, "admin-password")
	s.bootstrapAccounts()
	if !s.accountsEnabled() {
		t.Fatal("the community edition did not create its administrator from the environment")
	}
}

func TestAPasswordAnAdministratorSetMustBeChanged(t *testing.T) {
	s, admin := newAccountsServer(t)
	createTeamUser(t, admin, api.TeamUserCreateRequest{Username: "bob", Password: "temporary1"})
	bob := newTeamClient(t, s)
	bob.login("bob", "temporary1")
	expectCode(t, bob.do(http.MethodGet, "/api/tags", nil), http.StatusForbidden, passwordChangeRequiredCode, "using the instance before changing the password")
	expectStatus(t, bob.do(http.MethodGet, "/api/auth/session", nil), http.StatusOK, "session while a change is required")
	expectStatus(t, bob.do(http.MethodPut, "/api/auth/password", api.AuthPasswordChangeRequest{CurrentPassword: "temporary1", NewPassword: "bob-own-pass"}),
		http.StatusOK, "change the password")
	expectStatus(t, bob.do(http.MethodGet, "/api/tags", nil), http.StatusOK, "after changing the password")
}

func TestInvitations(t *testing.T) {
	s, admin := newAccountsServer(t)
	invited := createTeamUser(t, admin, api.TeamUserCreateRequest{Username: "carol", Role: "auditor", Invite: true})
	anon := newTeamClient(t, s)
	rec := anon.do(http.MethodGet, "/api/auth/invite/"+invited.InviteToken, nil)
	expectStatus(t, rec, http.StatusOK, "open the invitation")
	var info api.TeamInviteInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &info)
	if info.Username != "carol" || info.Role != "auditor" {
		t.Fatalf("invitation info %+v", info)
	}
	expectStatus(t, anon.do(http.MethodPost, "/api/auth/login", api.AuthLoginRequest{Username: "carol", Password: ""}), http.StatusUnauthorized, "invited user signing in")

	rec = admin.do(http.MethodPost, "/api/team/users/"+invited.ID+"/invite", nil)
	expectStatus(t, rec, http.StatusOK, "reissue the invitation")
	var reissued api.TeamUser
	_ = json.Unmarshal(rec.Body.Bytes(), &reissued)
	expectStatus(t, anon.do(http.MethodGet, "/api/auth/invite/"+invited.InviteToken, nil), http.StatusNotFound, "the replaced link")
	anon.headers = map[string]string{"Origin": "http://evil.example.com"}
	expectCode(t, anon.do(http.MethodPost, "/api/auth/invite/"+reissued.InviteToken, api.TeamInviteAcceptRequest{Password: "carol-password"}),
		http.StatusForbidden, permissionDeniedCode, "accepting from another origin")
	anon.headers = nil
	expectStatus(t, anon.do(http.MethodPost, "/api/auth/invite/"+reissued.InviteToken, api.TeamInviteAcceptRequest{Password: "carol-password"}),
		http.StatusOK, "accept the invitation")
	expectStatus(t, admin.do(http.MethodPost, "/api/team/users/"+invited.ID+"/invite", nil), http.StatusConflict, "reinvite an active user")
}

func TestSignInSettings(t *testing.T) {
	s, admin := newAccountsServer(t)
	expectStatus(t, admin.do(http.MethodPut, "/api/team/settings", api.TeamSettings{SessionHours: 0, MaxFailedLogins: 5, LockoutMinutes: 5}),
		http.StatusBadRequest, "invalid settings")
	expectStatus(t, admin.do(http.MethodPut, "/api/team/settings", api.TeamSettings{SessionHours: 12, MaxFailedLogins: 3, LockoutMinutes: 10}),
		http.StatusOK, "save settings")
	c := newTeamClient(t, s)
	for i := 0; i < 3; i++ {
		expectStatus(t, c.do(http.MethodPost, "/api/auth/login", api.AuthLoginRequest{Username: "admin", Password: "wrong-wrong"}), http.StatusUnauthorized, "wrong password")
	}
	rec := c.do(http.MethodPost, "/api/auth/login", api.AuthLoginRequest{Username: "admin", Password: "admin-password"})
	expectStatus(t, rec, http.StatusTooManyRequests, "locked after the configured three failures")
	if retry := rec.Header().Get("Retry-After"); retry != "600" {
		t.Fatalf("Retry-After = %q, want 600", retry)
	}
}
