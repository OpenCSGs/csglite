// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package team

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	previous := passwordIterations
	passwordIterations = 1000
	t.Cleanup(func() { passwordIterations = previous })
	return NewStore(t.TempDir())
}

func mustCreate(t *testing.T, s *Store, in NewUser) User {
	t.Helper()
	user, _, err := s.CreateUser(in)
	if err != nil {
		t.Fatalf("create %s: %v", in.Username, err)
	}
	return user
}

func TestFirstUserIsAlwaysAnAdministrator(t *testing.T) {
	s := newTestStore(t)
	if s.Enabled() {
		t.Fatal("a store without users must leave accounts off")
	}
	first := mustCreate(t, s, NewUser{Username: "alice", Password: "password1", Role: RoleAuditor, Invite: true})
	if first.Role != RoleAdmin || first.Pending() || first.MustChangePassword {
		t.Fatalf("first user = %+v, want an administrator who signs in with the password given", first)
	}
	if !s.Enabled() {
		t.Fatal("creating a user must switch accounts on")
	}
	second := mustCreate(t, s, NewUser{Username: "bob", Password: "password1"})
	if second.Role != RoleMember || !second.MustChangePassword {
		t.Fatalf("second user = %+v, want a member who must change the password an administrator chose", second)
	}
	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("team.json mode = %v, want owner-only", mode)
	}
}

func TestCreateUserValidates(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, NewUser{Username: "alice", Password: "password1"})
	cases := []struct {
		in   NewUser
		want error
	}{
		{NewUser{Username: "ALICE", Password: "password1"}, ErrUsernameTaken},
		{NewUser{Username: "a", Password: "password1"}, ErrInvalidUsername},
		{NewUser{Username: "has space", Password: "password1"}, ErrInvalidUsername},
		{NewUser{Username: "carol", Password: "short"}, ErrWeakPassword},
		{NewUser{Username: "carol", Password: "password1", Role: "owner"}, ErrInvalidRole},
		{NewUser{Username: "carol", Password: "password1", TeamID: "missing"}, ErrUnknownTeam},
	}
	for _, tc := range cases {
		if _, _, err := s.CreateUser(tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%+v: err = %v, want %v", tc.in, err, tc.want)
		}
	}
}

func TestAnEnabledAdministratorAlwaysRemains(t *testing.T) {
	s := newTestStore(t)
	admin := mustCreate(t, s, NewUser{Username: "alice", Password: "password1"})
	member := mustCreate(t, s, NewUser{Username: "bob", Password: "password1"})

	auditor := RoleAuditor
	if _, err := s.UpdateUser(admin.ID, UserPatch{Role: &auditor}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demoting the only admin: %v", err)
	}
	disabled := true
	if _, err := s.UpdateUser(admin.ID, UserPatch{Disabled: &disabled}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disabling the only admin: %v", err)
	}
	if err := s.DeleteUser(admin.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("deleting the only admin: %v", err)
	}

	adminRole := RoleAdmin
	if _, err := s.UpdateUser(member.ID, UserPatch{Role: &adminRole}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(admin.ID); err != nil {
		t.Fatalf("deleting one of two admins: %v", err)
	}
}

func TestLoginSessionsAndSignOut(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	user := mustCreate(t, s, NewUser{Username: "alice", Password: "password1"})

	if _, _, _, err := s.Login("alice", "wrong-password"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, _, _, err := s.Login("nobody", "password1"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("unknown user: %v", err)
	}
	got, token, expires, err := s.Login("ALICE", "password1")
	if err != nil || got.ID != user.ID || !expires.Equal(now.Add(DefaultSessionHours*time.Hour)) {
		t.Fatalf("login = %+v %v %v", got, expires, err)
	}
	if found, err := s.Session(token); err != nil || found.ID != user.ID {
		t.Fatalf("session = %+v %v", found, err)
	}
	if _, err := s.Session("not-a-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}

	now = now.Add(DefaultSessionHours * time.Hour)
	if _, err := s.Session(token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session: %v", err)
	}

	_, token, _, err = s.Login("alice", "password1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session after sign-out: %v", err)
	}
}

func TestDisablingAUserEndsTheirSessions(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, NewUser{Username: "alice", Password: "password1"})
	bob := mustCreate(t, s, NewUser{Username: "bob", Password: "password1"})
	_, token, _, err := s.Login("bob", "password1")
	if err != nil {
		t.Fatal(err)
	}
	disabled := true
	if _, err := s.UpdateUser(bob.ID, UserPatch{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled user's session: %v", err)
	}
	if _, _, _, err := s.Login("bob", "password1"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("disabled user signing in: %v", err)
	}
}

func TestChangePasswordKeepsOnlyTheCurrentSession(t *testing.T) {
	s := newTestStore(t)
	alice := mustCreate(t, s, NewUser{Username: "alice", Password: "password1"})
	_, current, _, _ := s.Login("alice", "password1")
	_, other, _, _ := s.Login("alice", "password1")

	if err := s.ChangePassword(alice.ID, "wrong-password", "password2", current); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("wrong current password: %v", err)
	}
	if err := s.ChangePassword(alice.ID, "password1", "password2", current); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(current); err != nil {
		t.Fatalf("the session that changed the password must survive: %v", err)
	}
	if _, err := s.Session(other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other sessions must end: %v", err)
	}
	if _, _, _, err := s.Login("alice", "password2"); err != nil {
		t.Fatalf("new password: %v", err)
	}
}

func TestDeletingATeamKeepsItsMembers(t *testing.T) {
	s := newTestStore(t)
	group, err := s.CreateTeam("Research", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTeam("research", ""); !errors.Is(err, ErrTeamNameTaken) {
		t.Fatalf("duplicate team name: %v", err)
	}
	alice := mustCreate(t, s, NewUser{Username: "alice", Password: "password1", TeamID: group.ID})
	if err := s.DeleteTeam(group.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.User(alice.ID)
	if err != nil || got.TeamID != "" {
		t.Fatalf("member after team deletion = %+v %v", got, err)
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	newTestStore(t)
	hash, err := hashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !checkPassword(hash, "correct horse") || checkPassword(hash, "correct horsE") {
		t.Fatal("password check is wrong")
	}
	for _, bad := range []string{"", "plain", "pbkdf2-sha256$x$y$z", "md5$1$a$b"} {
		if checkPassword(bad, "correct horse") {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestAllowed(t *testing.T) {
	cases := []struct {
		role   Role
		method string
		path   string
		want   bool
	}{
		{RoleAdmin, http.MethodPost, "/api/providers", true},
		{RoleAdmin, http.MethodGet, "/api/observability/requests", true},

		{RoleMember, http.MethodGet, "/api/tags", true},
		{RoleMember, http.MethodPost, "/v1/chat/completions", true},
		{RoleMember, http.MethodPost, "/providers/abc/v1/messages", true},
		{RoleMember, http.MethodPost, "/api/chat", true},
		{RoleMember, http.MethodPost, "/api/conversations", true},
		{RoleMember, http.MethodPut, "/api/conversations/abc", true},
		{RoleMember, http.MethodPost, "/api/api-keys", true},
		{RoleMember, http.MethodDelete, "/api/api-keys/abc", true},
		{RoleMember, http.MethodPost, "/api/api-keys/settings", false},
		{RoleMember, http.MethodPost, "/api/providers", false},
		{RoleMember, http.MethodPost, "/api/pull", false},
		{RoleMember, http.MethodDelete, "/api/delete", false},
		{RoleMember, http.MethodPost, "/api/settings", false},
		{RoleMember, http.MethodGet, "/api/observability/requests", false},
		{RoleMember, http.MethodGet, "/api/logs", false},
		{RoleMember, http.MethodGet, "/api/cluster/token", false},
		{RoleMember, http.MethodGet, "/api/apps/shell/abc/ws", false},
		{RoleMember, http.MethodGet, "/api/team/users", false},

		{RoleAuditor, http.MethodGet, "/api/tags", true},
		{RoleAuditor, http.MethodGet, "/v1/models", true},
		{RoleAuditor, http.MethodGet, "/api/observability/requests", true},
		{RoleAuditor, http.MethodGet, "/api/logs", true},
		{RoleAuditor, http.MethodGet, "/api/team/users", true},
		{RoleAuditor, http.MethodPost, "/api/show", true},
		{RoleAuditor, http.MethodPost, "/api/api-keys", true},
		{RoleAuditor, http.MethodDelete, "/api/api-keys/abc", true},
		{RoleAuditor, http.MethodGet, "/api/cluster/token", false},
		{RoleAuditor, http.MethodGet, "/api/apps/shell/abc/ws", false},
		{RoleAuditor, http.MethodPost, "/v1/chat/completions", false},
		{RoleAuditor, http.MethodGet, "/v1/realtime", false},
		{RoleAuditor, http.MethodPost, "/api/conversations", false},
		{RoleAuditor, http.MethodPost, "/api/team/users", false},
		{RoleAuditor, http.MethodPut, "/api/team/settings", false},
		{RoleAuditor, http.MethodPost, "/api/settings", false},

		{Role("owner"), http.MethodGet, "/api/tags", false},
		{legacyRoleViewer, http.MethodGet, "/api/tags", false},
	}
	for _, tc := range cases {
		if got := Allowed(tc.role, tc.method, tc.path); got != tc.want {
			t.Errorf("Allowed(%s, %s %s) = %v, want %v", tc.role, tc.method, tc.path, got, tc.want)
		}
	}
}

func TestPublicPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/":                      true,
		"/chat":                  true,
		"/assets/index.js":       true,
		"/api/health":            true,
		"/api/auth/login":        true,
		"/api/auth/session":      true,
		"/api/settings":          false,
		"/api/authx":             false,
		"/v1/models":             false,
		"/anthropic/v1/messages": false,
		"/providers/x/v1/models": false,
	} {
		if got := PublicPath(path); got != want {
			t.Errorf("PublicPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestStoreSeesChangesMadeByAnotherProcess(t *testing.T) {
	s := newTestStore(t)
	alice := mustCreate(t, s, NewUser{Username: "alice", Password: "password1"})
	if _, err := s.Users(); err != nil {
		t.Fatal(err)
	}
	// A second store on the same file stands in for the CLI.
	other := NewStore(filepath.Dir(s.Path()))
	name := "Alice Liddell"
	if _, err := other.UpdateUser(alice.ID, UserPatch{DisplayName: &name}); err != nil {
		t.Fatal(err)
	}
	got, err := s.User(alice.ID)
	if err != nil || got.DisplayName != name {
		t.Fatalf("user after another process changed it = %+v %v", got, err)
	}
}

func TestReturnedStateDoesNotAliasTheCache(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, NewUser{Username: "alice", Password: "password1"})
	users, _ := s.Users()
	users[0].Role = RoleAuditor
	again, _ := s.Users()
	if again[0].Role != RoleAdmin {
		t.Fatal("changing a returned user changed the store's cache")
	}
}

func TestInvitationFlow(t *testing.T) {
	s := newTestStore(t)
	admin := mustCreate(t, s, NewUser{Username: "alice", Password: "password1"})
	bob, token, err := s.CreateUser(NewUser{Username: "bob", Role: RoleMember, Invite: true, CreatedBy: admin.ID})
	if err != nil || token == "" || !bob.Pending() {
		t.Fatalf("invite = %+v %q %v", bob, token, err)
	}
	if _, _, _, err := s.Login("bob", ""); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("an invited user signed in before accepting: %v", err)
	}
	got, invite, err := s.InvitedUser(token)
	if err != nil || got.ID != bob.ID || invite.CreatedBy != admin.ID {
		t.Fatalf("invited user = %+v %+v %v", got, invite, err)
	}

	// A new link revokes the old one.
	next, _, err := s.ReissueInvite(bob.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.InvitedUser(token); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("the replaced link still works: %v", err)
	}
	if _, _, _, err := s.AcceptInvite(next, "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak password: %v", err)
	}
	accepted, session, _, err := s.AcceptInvite(next, "bob-password")
	if err != nil || accepted.Pending() || accepted.MustChangePassword || session == "" {
		t.Fatalf("accept = %+v %q %v", accepted, session, err)
	}
	if _, _, _, err := s.AcceptInvite(next, "bob-password"); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("an invitation worked twice: %v", err)
	}
	if _, _, err := s.ReissueInvite(bob.ID, admin.ID); !errors.Is(err, ErrNotPending) {
		t.Fatalf("reinviting an active user: %v", err)
	}
	if _, _, _, err := s.Login("bob", "bob-password"); err != nil {
		t.Fatalf("sign-in after accepting: %v", err)
	}
}

func TestInvitationsExpire(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	mustCreate(t, s, NewUser{Username: "alice", Password: "password1"})
	_, token, err := s.CreateUser(NewUser{Username: "bob", Invite: true})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(InviteTTL)
	if _, _, _, err := s.AcceptInvite(token, "bob-password"); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("an expired invitation was accepted: %v", err)
	}
}

func TestAnInvitedAdministratorDoesNotKeepTheInstanceAdministered(t *testing.T) {
	s := newTestStore(t)
	alice := mustCreate(t, s, NewUser{Username: "alice", Password: "password1"})
	if _, _, err := s.CreateUser(NewUser{Username: "bob", Role: RoleAdmin, Invite: true}); err != nil {
		t.Fatal(err)
	}
	member := RoleMember
	if _, err := s.UpdateUser(alice.ID, UserPatch{Role: &member}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demoting the only administrator who can sign in: %v", err)
	}
}

func TestAdministratorResetMakesTheUserChooseAPassword(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, NewUser{Username: "alice", Password: "password1"})
	bob := mustCreate(t, s, NewUser{Username: "bob", Password: "password1"})
	if err := s.ChangePassword(bob.ID, "password1", "bob-own-pass", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.User(bob.ID)
	if got.MustChangePassword {
		t.Fatal("choosing a password must clear the change requirement")
	}
	reset := "temporary1"
	got, err := s.UpdateUser(bob.ID, UserPatch{Password: &reset})
	if err != nil || !got.MustChangePassword {
		t.Fatalf("after an administrator reset = %+v %v", got, err)
	}
}

func TestHostResetPasswordRecoversALockedOutAdministrator(t *testing.T) {
	s := newTestStore(t)
	alice := mustCreate(t, s, NewUser{Username: "alice", Password: "password1"})
	_, token, _, _ := s.Login("alice", "password1")
	got, err := s.ResetPassword("ALICE", "recovered1", false)
	if err != nil || got.ID != alice.ID || got.MustChangePassword {
		t.Fatalf("reset = %+v %v", got, err)
	}
	if _, err := s.Session(token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sessions survived a host reset: %v", err)
	}
	if _, _, _, err := s.Login("alice", "recovered1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetPassword("nobody", "recovered1", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestSettings(t *testing.T) {
	s := newTestStore(t)
	got, err := s.Settings()
	if err != nil || got.SessionHours != DefaultSessionHours || got.MaxFailedLogins != DefaultMaxFailedLogins || got.LockoutMinutes != DefaultLockoutMinutes {
		t.Fatalf("defaults = %+v %v", got, err)
	}
	if _, err := s.UpdateSettings(Settings{SessionHours: 0, MaxFailedLogins: 5, LockoutMinutes: 5}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("zero session length: %v", err)
	}
	if _, err := s.UpdateSettings(Settings{SessionHours: 8, MaxFailedLogins: 3, LockoutMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	mustCreate(t, s, NewUser{Username: "alice", Password: "password1"})
	_, _, expires, err := s.Login("alice", "password1")
	if err != nil || !expires.Equal(now.Add(8*time.Hour)) {
		t.Fatalf("session expiry = %v %v, want 8 hours", expires, err)
	}
}

func TestLegacyViewersLoadAsMembers(t *testing.T) {
	s := newTestStore(t)
	data := `{"users":[{"id":"a","username":"alice","role":"admin","password_hash":"x"},{"id":"b","username":"bob","role":"viewer","password_hash":"x"}]}`
	if err := os.WriteFile(s.Path(), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.User("b")
	if err != nil || got.Role != RoleMember {
		t.Fatalf("legacy viewer = %+v %v", got, err)
	}
}
