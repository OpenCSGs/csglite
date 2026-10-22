package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/httpjson"
	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/internal/team"
	"github.com/opencsgs/csglite/pkg/api"
)

// GET /api/auth/session
func (s *Server) handleAuthSession(w http.ResponseWriter, r *http.Request) {
	resp := api.AuthSessionResponse{
		AccountsEnabled: s.accountsEnabled(),
		FeatureLicensed: s.license.Enabled(license.FeatureTeamManagement),
	}
	if !resp.AccountsEnabled {
		resp.LocalOperator = s.trustedLocalOperator(r)
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
			user := s.teamUserAPI(id.User, nil, nil)
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
	settings, _ := s.team.Settings()
	key := s.loginLimiter.key(r, req.Username)
	if locked, remaining := s.loginLimiter.locked(key); locked {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(remaining.Seconds()))))
		writeError(w, http.StatusTooManyRequests, "too many failed sign-in attempts; try again later")
		return
	}
	user, token, expires, err := s.team.Login(req.Username, req.Password)
	if errors.Is(err, team.ErrInvalidCredential) {
		s.loginLimiter.fail(key, settings)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sign in")
		return
	}
	s.loginLimiter.succeed(key)
	s.writeSignedIn(w, r, user, token, expires)
}

// writeSignedIn sets the session cookie and answers with the new session.
func (s *Server) writeSignedIn(w http.ResponseWriter, r *http.Request, user team.User, token string, expires time.Time) {
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
	view := s.teamUserAPI(user, nil, nil)
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

// GET /api/auth/invite/{token}
func (s *Server) handleAuthInviteGet(w http.ResponseWriter, r *http.Request) {
	if s.team == nil {
		writeTeamError(w, team.ErrInviteInvalid)
		return
	}
	user, invite, err := s.team.InvitedUser(r.PathValue("token"))
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.TeamInviteInfo{
		Username: user.Username, DisplayName: user.DisplayName, Role: string(user.Role), ExpiresAt: invite.ExpiresAt,
	})
}

// POST /api/auth/invite/{token}. Accepting sets the password the invited
// user chose and signs them in. It is not license-gated: the invitation was
// issued under a license, and the person holding it should not be stranded.
func (s *Server) handleAuthInviteAccept(w http.ResponseWriter, r *http.Request) {
	if s.team == nil {
		writeTeamError(w, team.ErrInviteInvalid)
		return
	}
	if !sameOriginRequest(r) {
		httpjson.WriteCodedError(w, http.StatusForbidden, "cross-origin request refused", permissionDeniedCode)
		return
	}
	var req api.TeamInviteAcceptRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user, token, expires, err := s.team.AcceptInvite(r.PathValue("token"), req.Password)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	s.writeSignedIn(w, r, user, token, expires)
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
	invites := s.inviteExpiries()
	out := make([]api.TeamUser, 0, len(users))
	for _, user := range users {
		out = append(out, s.teamUserAPI(user, teams, invites))
	}
	writeJSON(w, http.StatusOK, api.TeamUsersResponse{Users: out})
}

// POST /api/team/users. With no users yet this is how accounts are switched
// on: the new user is always an administrator, and a caller on another
// machine must present the setup token so nobody on the network can claim the
// instance first.
func (s *Server) handleTeamUserCreate(w http.ResponseWriter, r *http.Request) {
	var req api.TeamUserCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	bootstrap := !s.accountsEnabled()
	if bootstrap && !s.trustedLocalOperator(r) && !s.checkSetupToken(req.SetupToken) {
		httpjson.WriteCodedError(w, http.StatusForbidden,
			fmt.Sprintf("creating the first administrator from another machine needs the setup token in %s on the server", s.setupTokenPath()),
			setupTokenRequiredCode)
		return
	}
	if !s.roleLicensed(team.Role(strings.TrimSpace(req.Role))) {
		writeLicenseRequired(w, license.FeatureTeamManagement)
		return
	}
	id, _ := identityFromRequest(r)
	user, inviteToken, err := s.team.CreateUser(team.NewUser{
		Username:    req.Username,
		Password:    req.Password,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Role:        team.Role(strings.TrimSpace(req.Role)),
		TeamID:      req.TeamID,
		Invite:      req.Invite,
		SelfChosen:  bootstrap,
		CreatedBy:   id.User.ID,
	})
	if err != nil {
		writeTeamError(w, err)
		return
	}
	if bootstrap {
		s.clearSetupToken()
	}
	view := s.teamUserAPI(user, nil, s.inviteExpiries())
	view.InviteToken = inviteToken
	writeJSON(w, http.StatusCreated, view)
}

// roleLicensed reports whether role may be given out: the auditor role is an
// Enterprise feature. Auditors made under a license keep their role after it
// lapses.
func (s *Server) roleLicensed(role team.Role) bool {
	return role != team.RoleAuditor || s.license.Enabled(license.FeatureTeamManagement)
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
		if !s.roleLicensed(role) {
			if current, err := s.team.User(r.PathValue("id")); err != nil || current.Role != role {
				writeLicenseRequired(w, license.FeatureTeamManagement)
				return
			}
		}
		patch.Role = &role
	}
	user, err := s.team.UpdateUser(r.PathValue("id"), patch)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.teamUserAPI(user, nil, s.inviteExpiries()))
}

// POST /api/team/users/{id}/invite issues a new invitation link for a user
// who has not accepted theirs; the earlier link stops working.
func (s *Server) handleTeamUserReinvite(w http.ResponseWriter, r *http.Request) {
	id, _ := identityFromRequest(r)
	token, _, err := s.team.ReissueInvite(r.PathValue("id"), id.User.ID)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	user, err := s.team.User(r.PathValue("id"))
	if err != nil {
		writeTeamError(w, err)
		return
	}
	view := s.teamUserAPI(user, nil, s.inviteExpiries())
	view.InviteToken = token
	writeJSON(w, http.StatusOK, view)
}

// DELETE /api/team/users/{id}. The user's API keys go with them, so nothing
// keeps acting with the rights of an account that no longer exists. Deleting
// an invited user revokes the invitation.
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

// GET /api/team/settings
func (s *Server) handleTeamSettingsGet(w http.ResponseWriter, r *http.Request) {
	settings, err := s.team.Settings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load sign-in settings")
		return
	}
	writeJSON(w, http.StatusOK, s.teamSettingsAPI(settings))
}

// PUT /api/team/settings
func (s *Server) handleTeamSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var req api.TeamSettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	settings, err := s.team.UpdateSettings(team.Settings{
		SessionHours: req.SessionHours, MaxFailedLogins: req.MaxFailedLogins, LockoutMinutes: req.LockoutMinutes,
	})
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.teamSettingsAPI(settings))
}

func (s *Server) teamSettingsAPI(settings team.Settings) api.TeamSettings {
	_, set := bootstrapAdminPassword()
	return api.TeamSettings{
		SessionHours:    settings.SessionHours,
		MaxFailedLogins: settings.MaxFailedLogins,
		LockoutMinutes:  settings.LockoutMinutes,
		BootstrapEnvSet: set,
	}
}

func writeTeamError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, team.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, team.ErrInviteInvalid):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, team.ErrUsernameTaken), errors.Is(err, team.ErrTeamNameTaken), errors.Is(err, team.ErrLastAdmin),
		errors.Is(err, team.ErrNotPending):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, team.ErrInvalidCredential):
		writeError(w, http.StatusBadRequest, "the current password is wrong")
	case errors.Is(err, team.ErrInvalidUsername), errors.Is(err, team.ErrWeakPassword), errors.Is(err, team.ErrInvalidRole),
		errors.Is(err, team.ErrUnknownTeam), errors.Is(err, team.ErrTeamNameRequired), errors.Is(err, team.ErrInvalidSettings):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "failed to save accounts")
	}
}

// teamNames maps team IDs to names for user views.
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

// inviteExpiries maps invited users to when their invitation lapses.
func (s *Server) inviteExpiries() map[string]time.Time {
	out := map[string]time.Time{}
	invites, err := s.team.Invites()
	if err != nil {
		return out
	}
	for _, invite := range invites {
		out[invite.UserID] = invite.ExpiresAt
	}
	return out
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

// teamUserAPI is the public view of a user. teams and invites are loaded when
// nil.
func (s *Server) teamUserAPI(user team.User, teams map[string]string, invites map[string]time.Time) api.TeamUser {
	if teams == nil && user.TeamID != "" {
		teams = s.teamNames()
	}
	var lastLogin, inviteExpires *time.Time
	if !user.LastLoginAt.IsZero() {
		lastLogin = &user.LastLoginAt
	}
	if expires, ok := invites[user.ID]; ok && user.Pending() {
		inviteExpires = &expires
	}
	return api.TeamUser{
		ID:                 user.ID,
		Username:           user.Username,
		DisplayName:        user.DisplayName,
		Email:              user.Email,
		Role:               string(user.Role),
		TeamID:             user.TeamID,
		TeamName:           teams[user.TeamID],
		Disabled:           user.Disabled,
		Pending:            user.Pending(),
		MustChangePassword: user.MustChangePassword,
		CreatedAt:          user.CreatedAt,
		UpdatedAt:          user.UpdatedAt,
		LastLoginAt:        lastLogin,
		InviteExpiresAt:    inviteExpires,
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

// visibleAPIKey reports whether the caller may see a key: administrators and
// auditors see every key, anyone else only their own.
func visibleAPIKey(r *http.Request, record config.APIKeyRecord) bool {
	if requestSeesEverything(r) {
		return true
	}
	return ownsAPIKey(r, record)
}

// deletableAPIKey reports whether the caller may delete a key: an
// administrator any key, anyone else only their own.
func deletableAPIKey(r *http.Request, record config.APIKeyRecord) bool {
	return requestIsAdmin(r) || ownsAPIKey(r, record)
}

func ownsAPIKey(r *http.Request, record config.APIKeyRecord) bool {
	id, _ := identityFromRequest(r)
	return id.User.ID != "" && record.OwnerID == id.User.ID
}

func secureEqual(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
