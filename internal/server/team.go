package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/ee/team"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/httpjson"
	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/pkg/api"
)

// sessionCookie carries a signed-in user's session token.
const sessionCookie = "csglite_session"

// Codes the web UI switches on.
const (
	loginRequiredCode    = "login_required"
	permissionDeniedCode = "permission_denied"
)

// requestIdentity is who made a request once accounts are on. Exactly one of
// Local and User is set.
type requestIdentity struct {
	// Local is the operator on this machine, who administers the instance
	// without signing in, as the CLI and the desktop app always have.
	Local bool
	User  team.User
	// SessionToken is set when the user signed in through the web UI rather
	// than presenting an API key.
	SessionToken string
	// APIKey is set when the caller presented an API key.
	APIKey *config.APIKeyRecord
}

func (id requestIdentity) role() team.Role {
	if id.Local {
		return team.RoleAdmin
	}
	return id.User.Role
}

type requestIdentityKey struct{}

func identityFromRequest(r *http.Request) (requestIdentity, bool) {
	id, ok := r.Context().Value(requestIdentityKey{}).(requestIdentity)
	return id, ok
}

// accountsEnabled reports whether user accounts are in force.
func (s *Server) accountsEnabled() bool {
	return s.team != nil && s.team.Enabled()
}

// requestIsAdmin reports whether the caller may see and change everything.
// With accounts off every caller can, as before accounts existed.
func requestIsAdmin(r *http.Request) bool {
	id, ok := identityFromRequest(r)
	return !ok || id.role() == team.RoleAdmin
}

// ownerScope returns the owner that records a request creates or reads are
// scoped to. scoped is false when accounts are off and every record is
// visible. The local operator owns the records that predate accounts.
func ownerScope(r *http.Request) (owner string, scoped bool) {
	id, ok := identityFromRequest(r)
	if !ok {
		return "", false
	}
	if id.Local {
		return "", true
	}
	if id.User.ID == "" && id.APIKey != nil {
		// A key that predates accounts has no user, and must not see the
		// records the local operator owns.
		return "api-key:" + id.APIKey.ID, true
	}
	return id.User.ID, true
}

// accountsMiddleware enforces sign-in and roles once the first user exists.
// Until then it changes nothing, so a single-user installation behaves as it
// always has.
func (s *Server) accountsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || team.PublicPath(r.URL.Path) || !s.accountsEnabled() {
			next.ServeHTTP(w, r)
			return
		}
		id, ok, err := s.resolveIdentity(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check the session")
			return
		}
		if !ok {
			httpjson.WriteCodedError(w, http.StatusUnauthorized, "sign in or present a valid API key", loginRequiredCode)
			return
		}
		if id.SessionToken != "" && !safeMethod(r.Method) && !sameOriginRequest(r) {
			httpjson.WriteCodedError(w, http.StatusForbidden, "cross-origin request refused", permissionDeniedCode)
			return
		}
		if !team.Allowed(id.role(), r.Method, r.URL.Path) {
			httpjson.WriteCodedError(w, http.StatusForbidden, "your role does not allow this action", permissionDeniedCode)
			return
		}
		ctx := context.WithValue(r.Context(), requestIdentityKey{}, id)
		if id.APIKey != nil {
			// Usage is attributed to the key, as apiAuthMiddleware does.
			ctx = context.WithValue(ctx, apiKeyContextKey{}, *id.APIKey)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// resolveIdentity finds who is calling: the local operator, a session cookie,
// or an API key whose owner is an enabled user. A key that predates accounts
// has no owner and acts as a member, so it keeps running inference but
// cannot administer.
func (s *Server) resolveIdentity(r *http.Request) (requestIdentity, bool, error) {
	if s.trustedLocalOperator(r) {
		return requestIdentity{Local: true}, true, nil
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		user, err := s.team.Session(cookie.Value)
		if err == nil {
			return requestIdentity{User: user, SessionToken: cookie.Value}, true, nil
		}
		if !errors.Is(err, team.ErrNotFound) {
			return requestIdentity{}, false, err
		}
	}
	plain := requestAPIKey(r)
	if plain == "" || s.apiKeys == nil {
		return requestIdentity{}, false, nil
	}
	record, ok, err := s.apiKeys.Validate(plain)
	if err != nil || !ok {
		return requestIdentity{}, false, err
	}
	if record.OwnerID == "" {
		return requestIdentity{User: team.User{Username: record.Name, Role: team.RoleMember}, APIKey: &record}, true, nil
	}
	user, err := s.team.User(record.OwnerID)
	if errors.Is(err, team.ErrNotFound) || (err == nil && user.Disabled) {
		return requestIdentity{}, false, nil
	}
	if err != nil {
		return requestIdentity{}, false, err
	}
	return requestIdentity{User: user, APIKey: &record}, true, nil
}

// trustedLocalOperator reports whether a request comes from this machine
// itself. A request that went through a reverse proxy on this machine also
// arrives from loopback, so one carrying forwarding headers is not trusted:
// it is a remote user and must sign in.
func (s *Server) trustedLocalOperator(r *http.Request) bool {
	if s.cfg.DesktopMode {
		return true
	}
	if !isLoopbackRequest(r) {
		return false
	}
	for _, header := range []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded", "X-Forwarded-Host"} {
		if r.Header.Get(header) != "" {
			return false
		}
	}
	return true
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// sameOriginRequest guards cookie-authenticated changes against a page on
// another origin. Browsers send Origin with every cross-origin request that
// can change state, so its absence means a same-origin or non-browser caller.
func sameOriginRequest(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
	return forwarded != "" && strings.EqualFold(parsed.Host, forwarded)
}

// loginLimiter slows password guessing: after loginMaxFailures wrong
// passwords for one username from one address, that pair is refused for
// loginLockout.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string]loginFailures
	now      func() time.Time
}

type loginFailures struct {
	count int
	until time.Time
}

const (
	loginMaxFailures = 5
	loginLockout     = time.Minute
)

func (l *loginLimiter) key(r *http.Request, username string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host + "\x00" + strings.ToLower(strings.TrimSpace(username))
}

func (l *loginLimiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

func (l *loginLimiter) locked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.failures[key]
	return ok && l.clock().Before(entry.until)
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failures == nil {
		l.failures = map[string]loginFailures{}
	}
	now := l.clock()
	for k, entry := range l.failures {
		if entry.count >= loginMaxFailures && !now.Before(entry.until) {
			delete(l.failures, k)
		}
	}
	entry := l.failures[key]
	entry.count++
	if entry.count >= loginMaxFailures {
		entry.until = now.Add(loginLockout)
	}
	l.failures[key] = entry
}

func (l *loginLimiter) succeed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// GET /api/auth/session
func (s *Server) handleAuthSession(w http.ResponseWriter, r *http.Request) {
	resp := api.AuthSessionResponse{
		AccountsEnabled: s.accountsEnabled(),
		FeatureLicensed: s.license.Enabled(license.FeatureTeamManagement),
	}
	if !resp.AccountsEnabled {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	id, ok, err := s.resolveIdentity(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check the session")
		return
	}
	if ok {
		resp.Authenticated = true
		resp.LocalOperator = id.Local
		if !id.Local && id.User.ID != "" {
			user := s.teamUserAPI(id.User, nil)
			resp.User = &user
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/auth/login
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !s.accountsEnabled() {
		writeError(w, http.StatusConflict, "user accounts are not enabled on this instance")
		return
	}
	var req api.AuthLoginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	key := s.loginLimiter.key(r, req.Username)
	if s.loginLimiter.locked(key) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many failed sign-in attempts; try again in a minute")
		return
	}
	user, token, expires, err := s.team.Login(req.Username, req.Password)
	if errors.Is(err, team.ErrInvalidCredential) {
		s.loginLimiter.fail(key)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sign in")
		return
	}
	s.loginLimiter.succeed(key)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		// Lax rather than Strict so following a link to the instance from
		// chat or email arrives signed in; cross-site changes are refused by
		// sameOriginRequest.
		SameSite: http.SameSiteLaxMode,
	})
	view := s.teamUserAPI(user, nil)
	writeJSON(w, http.StatusOK, api.AuthSessionResponse{
		AccountsEnabled: true,
		FeatureLicensed: s.license.Enabled(license.FeatureTeamManagement),
		Authenticated:   true,
		User:            &view,
		ExpiresAt:       &expires,
	})
}

// POST /api/auth/logout
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil && s.team != nil {
		if err := s.team.Logout(cookie.Value); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to sign out")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: requestIsHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed_out"})
}

// PUT /api/auth/password
func (s *Server) handleAuthPasswordChange(w http.ResponseWriter, r *http.Request) {
	if !s.accountsEnabled() {
		writeError(w, http.StatusConflict, "user accounts are not enabled on this instance")
		return
	}
	id, ok, err := s.resolveIdentity(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check the session")
		return
	}
	if !ok || id.SessionToken == "" {
		httpjson.WriteCodedError(w, http.StatusUnauthorized, "sign in to change your password", loginRequiredCode)
		return
	}
	if !sameOriginRequest(r) {
		httpjson.WriteCodedError(w, http.StatusForbidden, "cross-origin request refused", permissionDeniedCode)
		return
	}
	var req api.AuthPasswordChangeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.team.ChangePassword(id.User.ID, req.CurrentPassword, req.NewPassword, id.SessionToken); err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "changed"})
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

// GET /api/team/users
func (s *Server) handleTeamUsersList(w http.ResponseWriter, r *http.Request) {
	users, err := s.team.Users()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load users")
		return
	}
	teams := s.teamNames()
	out := make([]api.TeamUser, 0, len(users))
	for _, user := range users {
		out = append(out, s.teamUserAPI(user, teams))
	}
	writeJSON(w, http.StatusOK, api.TeamUsersResponse{Users: out, MaxUsers: s.license.Limit(license.QuotaMaxUsers)})
}

// POST /api/team/users. With no users yet this is how accounts are switched
// on, and the new user is always an administrator.
func (s *Server) handleTeamUserCreate(w http.ResponseWriter, r *http.Request) {
	var req api.TeamUserCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	maxUsers := s.license.Limit(license.QuotaMaxUsers)
	user, err := s.team.CreateUser(team.NewUser{
		Username:    req.Username,
		Password:    req.Password,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Role:        team.Role(strings.TrimSpace(req.Role)),
		TeamID:      req.TeamID,
	}, maxUsers)
	var seats *team.SeatLimitError
	if errors.As(err, &seats) {
		writeJSON(w, http.StatusForbidden, seatLimitResponse{
			Error:           err.Error(),
			ErrorCode:       http.StatusForbidden,
			Code:            licenseErrorCode,
			Feature:         license.QuotaMaxUsers.Key,
			EditionRequired: license.EditionEnterprise,
			Limit:           seats.Limit,
			Current:         seats.Current,
		})
		return
	}
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.teamUserAPI(user, nil))
}

type seatLimitResponse struct {
	Error           string `json:"error"`
	ErrorCode       int    `json:"errorCode"`
	Code            string `json:"code"`
	Feature         string `json:"feature"`
	EditionRequired string `json:"edition_required"`
	Limit           int    `json:"limit"`
	Current         int    `json:"current"`
}

// PUT /api/team/users/{id}
func (s *Server) handleTeamUserUpdate(w http.ResponseWriter, r *http.Request) {
	var req api.TeamUserUpdateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	patch := team.UserPatch{
		DisplayName: req.DisplayName,
		Email:       req.Email,
		TeamID:      req.TeamID,
		Disabled:    req.Disabled,
		Password:    req.Password,
	}
	if req.Role != nil {
		role := team.Role(strings.TrimSpace(*req.Role))
		patch.Role = &role
	}
	user, err := s.team.UpdateUser(r.PathValue("id"), patch)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.teamUserAPI(user, nil))
}

// DELETE /api/team/users/{id}. The user's API keys go with them, so nothing
// keeps acting with the rights of an account that no longer exists.
func (s *Server) handleTeamUserDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.team.DeleteUser(id); err != nil {
		writeTeamError(w, err)
		return
	}
	if s.apiKeys != nil {
		if _, err := s.apiKeys.DeleteOwnedBy(id); err != nil {
			writeError(w, http.StatusInternalServerError, "user deleted, but removing their API keys failed")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// GET /api/team/teams
func (s *Server) handleTeamGroupsList(w http.ResponseWriter, r *http.Request) {
	teams, err := s.team.Teams()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load teams")
		return
	}
	users, err := s.team.Users()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load users")
		return
	}
	members := map[string]int{}
	for _, user := range users {
		members[user.TeamID]++
	}
	out := make([]api.TeamGroup, 0, len(teams))
	for _, group := range teams {
		out = append(out, teamGroupAPI(group, members[group.ID]))
	}
	writeJSON(w, http.StatusOK, api.TeamGroupsResponse{Teams: out})
}

// POST /api/team/teams
func (s *Server) handleTeamGroupCreate(w http.ResponseWriter, r *http.Request) {
	var req api.TeamGroupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name, description := "", ""
	if req.Name != nil {
		name = *req.Name
	}
	if req.Description != nil {
		description = *req.Description
	}
	group, err := s.team.CreateTeam(name, description)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, teamGroupAPI(group, 0))
}

// PUT /api/team/teams/{id}
func (s *Server) handleTeamGroupUpdate(w http.ResponseWriter, r *http.Request) {
	var req api.TeamGroupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	group, err := s.team.UpdateTeam(r.PathValue("id"), req.Name, req.Description)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, teamGroupAPI(group, s.teamMemberCount(group.ID)))
}

// DELETE /api/team/teams/{id}
func (s *Server) handleTeamGroupDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.team.DeleteTeam(r.PathValue("id")); err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func writeTeamError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, team.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, team.ErrUsernameTaken), errors.Is(err, team.ErrTeamNameTaken), errors.Is(err, team.ErrLastAdmin):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, team.ErrInvalidCredential):
		writeError(w, http.StatusBadRequest, "the current password is wrong")
	case errors.Is(err, team.ErrInvalidUsername), errors.Is(err, team.ErrWeakPassword), errors.Is(err, team.ErrInvalidRole),
		errors.Is(err, team.ErrUnknownTeam), errors.Is(err, team.ErrTeamNameRequired):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "failed to save accounts")
	}
}

// teamNames maps team IDs to names for user views; nil loads them.
func (s *Server) teamNames() map[string]string {
	names := map[string]string{}
	teams, err := s.team.Teams()
	if err != nil {
		return names
	}
	for _, group := range teams {
		names[group.ID] = group.Name
	}
	return names
}

func (s *Server) teamMemberCount(teamID string) int {
	users, err := s.team.Users()
	if err != nil {
		return 0
	}
	count := 0
	for _, user := range users {
		if user.TeamID == teamID {
			count++
		}
	}
	return count
}

func (s *Server) teamUserAPI(user team.User, teams map[string]string) api.TeamUser {
	if teams == nil && user.TeamID != "" {
		teams = s.teamNames()
	}
	var lastLogin *time.Time
	if !user.LastLoginAt.IsZero() {
		lastLogin = &user.LastLoginAt
	}
	return api.TeamUser{
		ID:          user.ID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Email:       user.Email,
		Role:        string(user.Role),
		TeamID:      user.TeamID,
		TeamName:    teams[user.TeamID],
		Disabled:    user.Disabled,
		CreatedAt:   user.CreatedAt,
		UpdatedAt:   user.UpdatedAt,
		LastLoginAt: lastLogin,
	}
}

func teamGroupAPI(group team.Team, members int) api.TeamGroup {
	return api.TeamGroup{
		ID:          group.ID,
		Name:        group.Name,
		Description: group.Description,
		MemberCount: members,
		CreatedAt:   group.CreatedAt,
		UpdatedAt:   group.UpdatedAt,
	}
}

// apiKeyOwnerNames maps owner IDs to a display name for the API key list.
func (s *Server) apiKeyOwnerNames() map[string]string {
	names := map[string]string{}
	if s.team == nil {
		return names
	}
	users, err := s.team.Users()
	if err != nil {
		return names
	}
	for _, user := range users {
		name := user.DisplayName
		if name == "" {
			name = user.Username
		}
		names[user.ID] = name
	}
	return names
}

// visibleAPIKey reports whether the caller may see and delete a key: an
// administrator sees every key, anyone else only their own.
func visibleAPIKey(r *http.Request, record config.APIKeyRecord) bool {
	if requestIsAdmin(r) {
		return true
	}
	id, _ := identityFromRequest(r)
	return id.User.ID != "" && record.OwnerID == id.User.ID
}
