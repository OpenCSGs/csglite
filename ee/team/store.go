// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

// Package team keeps the user accounts, teams, invitations and login sessions
// of a CSGLite instance shared by several people. Accounts are switched on by
// creating the first user; until then the instance behaves exactly as a
// single-user installation does.
package team

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/safefile"
)

// File is the runtime file under the app home that holds users, teams,
// invitations, sessions and sign-in settings. It lives beside api_keys.json
// rather than in config.json because it holds credentials and changes on
// every login.
const File = "team.json"

// InviteTTL is how long an invitation link stays valid.
const InviteTTL = 7 * 24 * time.Hour

const minPasswordLength = 8

// Role decides what a signed-in user may do. The three roles mirror the
// global roles of LiteLLM (proxy_admin, proxy_admin_viewer, internal_user).
type Role string

const (
	// RoleAdmin administers the instance: providers, models, settings, users.
	RoleAdmin Role = "admin"
	// RoleAuditor reads everything an administrator can, including request
	// logs, usage and every API key, and changes nothing.
	RoleAuditor Role = "auditor"
	// RoleMember uses models: chat, inference and their own API keys.
	RoleMember Role = "member"
)

// legacyRoleViewer was a read-only role in the first, unreleased cut. Users
// saved with it load as members.
const legacyRoleViewer Role = "viewer"

// ValidRole reports whether role is one of the three roles.
func ValidRole(role Role) bool {
	switch role {
	case RoleAdmin, RoleAuditor, RoleMember:
		return true
	}
	return false
}

// User is one account. PasswordHash never leaves this package's callers in an
// API response; the server maps users onto a public view. A user with no
// password hash has been invited and has not accepted yet.
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name,omitempty"`
	Email        string `json:"email,omitempty"`
	Role         Role   `json:"role"`
	TeamID       string `json:"team_id,omitempty"`
	Disabled     bool   `json:"disabled,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
	// MustChangePassword is set when someone other than the user chose the
	// password, so the user replaces it before doing anything else.
	MustChangePassword bool      `json:"must_change_password,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	LastLoginAt        time.Time `json:"last_login_at,omitempty"`
}

// Pending reports whether the user was invited and has not set a password.
func (u User) Pending() bool { return u.PasswordHash == "" }

// Team groups users. Usage and budgets are attributed to the team of the user
// who owns a request.
type Team struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Invite is an outstanding invitation. Only the SHA-256 of its token is kept.
type Invite struct {
	TokenHash string    `json:"token_hash"`
	UserID    string    `json:"user_id"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Settings tune sign-in. Zero values mean the defaults.
type Settings struct {
	// SessionHours is how long a sign-in lasts. Sessions do not slide, so a
	// stolen cookie stops working on a fixed schedule.
	SessionHours int `json:"session_hours,omitempty"`
	// MaxFailedLogins wrong passwords for one username from one address lock
	// that pair out for LockoutMinutes.
	MaxFailedLogins int `json:"max_failed_logins,omitempty"`
	LockoutMinutes  int `json:"lockout_minutes,omitempty"`
}

const (
	DefaultSessionHours    = 7 * 24
	DefaultMaxFailedLogins = 5
	DefaultLockoutMinutes  = 5
)

// Effective fills defaults in.
func (s Settings) Effective() Settings {
	if s.SessionHours <= 0 {
		s.SessionHours = DefaultSessionHours
	}
	if s.MaxFailedLogins <= 0 {
		s.MaxFailedLogins = DefaultMaxFailedLogins
	}
	if s.LockoutMinutes <= 0 {
		s.LockoutMinutes = DefaultLockoutMinutes
	}
	return s
}

// Validate checks the ranges the UI offers.
func (s Settings) Validate() error {
	switch {
	case s.SessionHours < 1 || s.SessionHours > 90*24:
		return ErrInvalidSettings
	case s.MaxFailedLogins < 3 || s.MaxFailedLogins > 50:
		return ErrInvalidSettings
	case s.LockoutMinutes < 1 || s.LockoutMinutes > 24*60:
		return ErrInvalidSettings
	}
	return nil
}

// session is stored by the SHA-256 of its token, so reading team.json does not
// hand out a usable cookie.
type session struct {
	TokenHash string    `json:"token_hash"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type state struct {
	Users    []User    `json:"users"`
	Teams    []Team    `json:"teams"`
	Invites  []Invite  `json:"invites"`
	Sessions []session `json:"sessions"`
	Settings Settings  `json:"settings"`
}

// Errors returned for requests the caller can correct. The server maps them
// onto 400, 404 and 409 responses.
var (
	ErrNotFound          = errors.New("not found")
	ErrUsernameTaken     = errors.New("username already exists")
	ErrTeamNameTaken     = errors.New("team name already exists")
	ErrInvalidUsername   = errors.New("username must be 2-64 characters of letters, digits, '.', '_', '-' or '@'")
	ErrWeakPassword      = fmt.Errorf("password must be at least %d characters", minPasswordLength)
	ErrInvalidRole       = errors.New("role must be admin, auditor or member")
	ErrUnknownTeam       = errors.New("team does not exist")
	ErrLastAdmin         = errors.New("at least one enabled administrator must remain")
	ErrInvalidCredential = errors.New("invalid username or password")
	ErrTeamNameRequired  = errors.New("team name is required")
	ErrInviteInvalid     = errors.New("the invitation is invalid or has expired")
	ErrNotPending        = errors.New("the user has already accepted their invitation")
	ErrInvalidSettings   = errors.New("sessions last 1 hour to 90 days, 3 to 50 failed sign-ins are allowed, and a lockout lasts 1 minute to 1 day")
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{2,64}$`)

// Store reads and writes team.json. Every method checks the file, so a change
// made by another process (the CLI resetting a password) is seen at once.
type Store struct {
	path string
	mu   sync.Mutex
	now  func() time.Time

	// cached is the last state read, valid while the file's size and
	// modification time are unchanged. Every request asks whether accounts
	// are on, so re-reading the file each time would put disk I/O on the
	// inference path.
	cached      state
	cachedSize  int64
	cachedMTime time.Time
	cachedOK    bool
}

// NewStore opens the store kept in appHome.
func NewStore(appHome string) *Store {
	return &Store{path: filepath.Join(appHome, File), now: time.Now}
}

// Path returns the file the store reads and writes.
func (s *Store) Path() string { return s.path }

// Enabled reports whether accounts are in use, which is the case as soon as
// one user exists. A store that cannot be read counts as enabled: failing
// open would hand an unauthenticated caller the whole instance.
func (s *Store) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return true
	}
	return len(st.Users) > 0
}

// Users returns every user, ordered by username.
func (s *Store) Users() ([]User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	users := st.Users
	sort.Slice(users, func(i, j int) bool { return strings.ToLower(users[i].Username) < strings.ToLower(users[j].Username) })
	return users, nil
}

// User returns one user by ID.
func (s *Store) User(id string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return User{}, err
	}
	if i := st.userIndex(id); i >= 0 {
		return st.Users[i], nil
	}
	return User{}, ErrNotFound
}

// UserByName returns one user by username, ignoring case.
func (s *Store) UserByName(username string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return User{}, err
	}
	if i := st.usernameIndex(username); i >= 0 {
		return st.Users[i], nil
	}
	return User{}, ErrNotFound
}

// Teams returns every team, ordered by name.
func (s *Store) Teams() ([]Team, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	teams := st.Teams
	sort.Slice(teams, func(i, j int) bool { return strings.ToLower(teams[i].Name) < strings.ToLower(teams[j].Name) })
	return teams, nil
}

// Invites returns the outstanding invitations that have not expired.
func (s *Store) Invites() ([]Invite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]Invite, 0, len(st.Invites))
	for _, invite := range st.Invites {
		if invite.ExpiresAt.After(now) {
			out = append(out, invite)
		}
	}
	return out, nil
}

// Settings returns the sign-in settings with defaults filled in.
func (s *Store) Settings() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return Settings{}.Effective(), err
	}
	return st.Settings.Effective(), nil
}

// UpdateSettings replaces the sign-in settings.
func (s *Store) UpdateSettings(next Settings) (Settings, error) {
	if err := next.Validate(); err != nil {
		return Settings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return Settings{}, err
	}
	st.Settings = next
	if err := s.saveLocked(st); err != nil {
		return Settings{}, err
	}
	return next, nil
}

// NewUser is what CreateUser needs. With Invite set, the user has no password
// and joins by accepting the invitation CreateUser returns. Otherwise the
// password is set now, and the user must change it at first sign-in unless
// they chose it themselves (SelfChosen).
//
// The first user of an instance is always an administrator, whatever Role
// says, so turning accounts on can never lock everyone out; it cannot be an
// invitation, since nobody could sign in to accept it.
type NewUser struct {
	Username    string
	Password    string
	DisplayName string
	Email       string
	Role        Role
	TeamID      string
	Invite      bool
	SelfChosen  bool
	CreatedBy   string
}

// CreateUser adds a user. It returns the invitation token when one was
// issued.
func (s *Store) CreateUser(in NewUser) (User, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return User{}, "", err
	}
	username := strings.TrimSpace(in.Username)
	if !usernamePattern.MatchString(username) {
		return User{}, "", ErrInvalidUsername
	}
	if st.usernameIndex(username) >= 0 {
		return User{}, "", ErrUsernameTaken
	}
	first := len(st.Users) == 0
	invite := in.Invite && !first
	if !invite && len(in.Password) < minPasswordLength {
		return User{}, "", ErrWeakPassword
	}
	role := in.Role
	if first {
		role = RoleAdmin
	}
	if role == "" {
		role = RoleMember
	}
	if !ValidRole(role) {
		return User{}, "", ErrInvalidRole
	}
	teamID := strings.TrimSpace(in.TeamID)
	if teamID != "" && st.teamIndex(teamID) < 0 {
		return User{}, "", ErrUnknownTeam
	}
	id, err := randomHex(8)
	if err != nil {
		return User{}, "", err
	}
	now := s.now().UTC()
	user := User{
		ID:          id,
		Username:    username,
		DisplayName: strings.TrimSpace(in.DisplayName),
		Email:       strings.TrimSpace(in.Email),
		Role:        role,
		TeamID:      teamID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if !invite {
		hash, err := hashPassword(in.Password)
		if err != nil {
			return User{}, "", err
		}
		user.PasswordHash = hash
		user.MustChangePassword = !in.SelfChosen && !first
	}
	st.Users = append(st.Users, user)
	token := ""
	if invite {
		token, err = st.issueInvite(user.ID, in.CreatedBy, now)
		if err != nil {
			return User{}, "", err
		}
	}
	if err := s.saveLocked(st); err != nil {
		return User{}, "", err
	}
	return user, token, nil
}

// ReissueInvite replaces a pending user's invitation with a new one, which
// also revokes the link sent before.
func (s *Store) ReissueInvite(userID, createdBy string) (string, Invite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return "", Invite{}, err
	}
	i := st.userIndex(userID)
	if i < 0 {
		return "", Invite{}, ErrNotFound
	}
	if !st.Users[i].Pending() {
		return "", Invite{}, ErrNotPending
	}
	token, err := st.issueInvite(userID, createdBy, s.now().UTC())
	if err != nil {
		return "", Invite{}, err
	}
	if err := s.saveLocked(st); err != nil {
		return "", Invite{}, err
	}
	return token, st.Invites[len(st.Invites)-1], nil
}

// InvitedUser resolves an invitation token to the user it invites.
func (s *Store) InvitedUser(token string) (User, Invite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return User{}, Invite{}, err
	}
	i, invite := st.findInvite(token, s.now())
	if i < 0 {
		return User{}, Invite{}, ErrInviteInvalid
	}
	return st.Users[i], invite, nil
}

// AcceptInvite sets the invited user's password, spends the invitation and
// signs the user in.
func (s *Store) AcceptInvite(token, password string) (User, string, time.Time, error) {
	if len(password) < minPasswordLength {
		return User{}, "", time.Time{}, ErrWeakPassword
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	i, _ := st.findInvite(token, s.now())
	if i < 0 {
		return User{}, "", time.Time{}, ErrInviteInvalid
	}
	hash, err := hashPassword(password)
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	now := s.now().UTC()
	st.Users[i].PasswordHash = hash
	st.Users[i].MustChangePassword = false
	st.Users[i].UpdatedAt = now
	userID := st.Users[i].ID
	st.dropInvites(func(invite Invite) bool { return invite.UserID == userID })
	sessionToken, expires, err := st.openSession(i, now)
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	if err := s.saveLocked(st); err != nil {
		return User{}, "", time.Time{}, err
	}
	return st.Users[i], sessionToken, expires, nil
}

// UserPatch changes the fields that are set. Password resets the password,
// ends the user's sessions and makes them choose a new one at next sign-in.
type UserPatch struct {
	DisplayName *string
	Email       *string
	Role        *Role
	TeamID      *string
	Disabled    *bool
	Password    *string
}

// UpdateUser applies patch. It refuses a change that would leave no enabled
// administrator. A user who is disabled, given another role or a new password
// is signed out everywhere.
func (s *Store) UpdateUser(id string, patch UserPatch) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return User{}, err
	}
	i := st.userIndex(id)
	if i < 0 {
		return User{}, ErrNotFound
	}
	user := st.Users[i]
	endSessions := false
	if patch.DisplayName != nil {
		user.DisplayName = strings.TrimSpace(*patch.DisplayName)
	}
	if patch.Email != nil {
		user.Email = strings.TrimSpace(*patch.Email)
	}
	if patch.Role != nil {
		if !ValidRole(*patch.Role) {
			return User{}, ErrInvalidRole
		}
		if *patch.Role != user.Role {
			endSessions = true
		}
		user.Role = *patch.Role
	}
	if patch.TeamID != nil {
		teamID := strings.TrimSpace(*patch.TeamID)
		if teamID != "" && st.teamIndex(teamID) < 0 {
			return User{}, ErrUnknownTeam
		}
		user.TeamID = teamID
	}
	if patch.Disabled != nil {
		if *patch.Disabled && !user.Disabled {
			endSessions = true
		}
		user.Disabled = *patch.Disabled
	}
	if patch.Password != nil {
		if len(*patch.Password) < minPasswordLength {
			return User{}, ErrWeakPassword
		}
		hash, err := hashPassword(*patch.Password)
		if err != nil {
			return User{}, err
		}
		user.PasswordHash = hash
		user.MustChangePassword = true
		endSessions = true
		// A password set by an administrator replaces the invitation.
		st.dropInvites(func(invite Invite) bool { return invite.UserID == id })
	}
	st.Users[i] = user
	if !st.hasEnabledAdmin() {
		return User{}, ErrLastAdmin
	}
	user.UpdatedAt = s.now().UTC()
	st.Users[i] = user
	if endSessions {
		st.dropSessions(func(sess session) bool { return sess.UserID == id })
	}
	if err := s.saveLocked(st); err != nil {
		return User{}, err
	}
	return user, nil
}

// ResetPassword sets a user's password from the host itself, the way out when
// the last administrator forgot theirs. It is not license-gated, re-enables
// the user, and makes them choose another password at next sign-in only when
// requireChange is set.
func (s *Store) ResetPassword(username, password string, requireChange bool) (User, error) {
	if len(password) < minPasswordLength {
		return User{}, ErrWeakPassword
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return User{}, err
	}
	i := st.usernameIndex(username)
	if i < 0 {
		return User{}, ErrNotFound
	}
	hash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	id := st.Users[i].ID
	st.Users[i].PasswordHash = hash
	st.Users[i].MustChangePassword = requireChange
	st.Users[i].Disabled = false
	st.Users[i].UpdatedAt = s.now().UTC()
	st.dropSessions(func(sess session) bool { return sess.UserID == id })
	st.dropInvites(func(invite Invite) bool { return invite.UserID == id })
	if err := s.saveLocked(st); err != nil {
		return User{}, err
	}
	return st.Users[i], nil
}

// DeleteUser removes a user with their sessions and invitation.
func (s *Store) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return err
	}
	i := st.userIndex(id)
	if i < 0 {
		return ErrNotFound
	}
	st.Users = append(st.Users[:i], st.Users[i+1:]...)
	// Deleting the last administrator would switch accounts off and reopen
	// the instance to anyone, so that takes removing team.json on the host.
	if !st.hasEnabledAdmin() {
		return ErrLastAdmin
	}
	st.dropSessions(func(sess session) bool { return sess.UserID == id })
	st.dropInvites(func(invite Invite) bool { return invite.UserID == id })
	return s.saveLocked(st)
}

// ChangePassword lets a user replace their own password. Their other sessions
// end; the one making the change is kept when keepToken is set.
func (s *Store) ChangePassword(id, current, next, keepToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return err
	}
	i := st.userIndex(id)
	if i < 0 {
		return ErrNotFound
	}
	if !checkPassword(st.Users[i].PasswordHash, current) {
		return ErrInvalidCredential
	}
	if len(next) < minPasswordLength {
		return ErrWeakPassword
	}
	hash, err := hashPassword(next)
	if err != nil {
		return err
	}
	st.Users[i].PasswordHash = hash
	st.Users[i].MustChangePassword = false
	st.Users[i].UpdatedAt = s.now().UTC()
	keep := tokenHash(keepToken)
	st.dropSessions(func(sess session) bool { return sess.UserID == id && sess.TokenHash != keep })
	return s.saveLocked(st)
}

// Login checks a username and password and opens a session. It returns the
// session token to set as a cookie. A disabled or invited user cannot sign in.
func (s *Store) Login(username, password string) (User, string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	i := st.usernameIndex(username)
	if i < 0 || st.Users[i].Pending() {
		// Hash anyway so a missing user takes as long as a wrong password.
		_ = checkPassword(dummyPasswordHash(), password)
		return User{}, "", time.Time{}, ErrInvalidCredential
	}
	if !checkPassword(st.Users[i].PasswordHash, password) || st.Users[i].Disabled {
		return User{}, "", time.Time{}, ErrInvalidCredential
	}
	token, expires, err := st.openSession(i, s.now().UTC())
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	if err := s.saveLocked(st); err != nil {
		return User{}, "", time.Time{}, err
	}
	return st.Users[i], token, expires, nil
}

// Session resolves a session token to its user. An expired session, or one
// whose user is gone or disabled, resolves to ErrNotFound.
func (s *Store) Session(token string) (User, error) {
	if token == "" {
		return User{}, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return User{}, err
	}
	hash := tokenHash(token)
	now := s.now()
	for _, sess := range st.Sessions {
		if subtle.ConstantTimeCompare([]byte(sess.TokenHash), []byte(hash)) != 1 {
			continue
		}
		if !sess.ExpiresAt.After(now) {
			return User{}, ErrNotFound
		}
		i := st.userIndex(sess.UserID)
		if i < 0 || st.Users[i].Disabled {
			return User{}, ErrNotFound
		}
		return st.Users[i], nil
	}
	return User{}, ErrNotFound
}

// Logout ends one session. An unknown token is not an error.
func (s *Store) Logout(token string) error {
	if token == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return err
	}
	hash := tokenHash(token)
	before := len(st.Sessions)
	st.dropSessions(func(sess session) bool { return sess.TokenHash == hash })
	if len(st.Sessions) == before {
		return nil
	}
	return s.saveLocked(st)
}

// CreateTeam adds a team.
func (s *Store) CreateTeam(name, description string) (Team, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return Team{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Team{}, ErrTeamNameRequired
	}
	if st.teamNameTaken(name, "") {
		return Team{}, ErrTeamNameTaken
	}
	id, err := randomHex(8)
	if err != nil {
		return Team{}, err
	}
	now := s.now().UTC()
	team := Team{ID: id, Name: name, Description: strings.TrimSpace(description), CreatedAt: now, UpdatedAt: now}
	st.Teams = append(st.Teams, team)
	if err := s.saveLocked(st); err != nil {
		return Team{}, err
	}
	return team, nil
}

// UpdateTeam renames a team or changes its description.
func (s *Store) UpdateTeam(id string, name, description *string) (Team, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return Team{}, err
	}
	i := st.teamIndex(id)
	if i < 0 {
		return Team{}, ErrNotFound
	}
	team := st.Teams[i]
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" {
			return Team{}, ErrTeamNameRequired
		}
		if st.teamNameTaken(trimmed, id) {
			return Team{}, ErrTeamNameTaken
		}
		team.Name = trimmed
	}
	if description != nil {
		team.Description = strings.TrimSpace(*description)
	}
	team.UpdatedAt = s.now().UTC()
	st.Teams[i] = team
	if err := s.saveLocked(st); err != nil {
		return Team{}, err
	}
	return team, nil
}

// DeleteTeam removes a team. Its members stay, without a team.
func (s *Store) DeleteTeam(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return err
	}
	i := st.teamIndex(id)
	if i < 0 {
		return ErrNotFound
	}
	st.Teams = append(st.Teams[:i], st.Teams[i+1:]...)
	for j := range st.Users {
		if st.Users[j].TeamID == id {
			st.Users[j].TeamID = ""
		}
	}
	return s.saveLocked(st)
}

func (s *Store) loadLocked() (state, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.cachedOK = false
			return state{}, nil
		}
		return state{}, err
	}
	if s.cachedOK && info.Size() == s.cachedSize && info.ModTime().Equal(s.cachedMTime) {
		return s.cached.clone(), nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return state{}, err
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return state{}, fmt.Errorf("reading %s: %w", s.path, err)
	}
	for i := range st.Users {
		if st.Users[i].Role == legacyRoleViewer {
			st.Users[i].Role = RoleMember
		}
	}
	s.cached, s.cachedSize, s.cachedMTime, s.cachedOK = st.clone(), info.Size(), info.ModTime(), true
	return st, nil
}

// clone copies the slices so a caller changing the returned state cannot
// change the cache.
func (st state) clone() state {
	return state{
		Users:    append([]User(nil), st.Users...),
		Teams:    append([]Team(nil), st.Teams...),
		Invites:  append([]Invite(nil), st.Invites...),
		Sessions: append([]session(nil), st.Sessions...),
		Settings: st.Settings,
	}
}

func (s *Store) saveLocked(st state) error {
	if st.Users == nil {
		st.Users = []User{}
	}
	if st.Teams == nil {
		st.Teams = []Team{}
	}
	if st.Invites == nil {
		st.Invites = []Invite{}
	}
	if st.Sessions == nil {
		st.Sessions = []session{}
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	// A write within the file system's timestamp resolution may leave size
	// and mtime as they were, so the cache is dropped rather than trusted.
	s.cachedOK = false
	return safefile.Write(s.path, data, 0o600)
}

// openSession records a new session for user i and their sign-in time.
func (st *state) openSession(i int, now time.Time) (string, time.Time, error) {
	token, err := randomHex(32)
	if err != nil {
		return "", time.Time{}, err
	}
	expires := now.Add(time.Duration(st.Settings.Effective().SessionHours) * time.Hour)
	st.dropSessions(func(sess session) bool { return !sess.ExpiresAt.After(now) })
	st.Sessions = append(st.Sessions, session{TokenHash: tokenHash(token), UserID: st.Users[i].ID, CreatedAt: now, ExpiresAt: expires})
	st.Users[i].LastLoginAt = now
	return token, expires, nil
}

// issueInvite replaces any invitation the user had with a new one.
func (st *state) issueInvite(userID, createdBy string, now time.Time) (string, error) {
	token, err := randomHex(24)
	if err != nil {
		return "", err
	}
	st.dropInvites(func(invite Invite) bool { return invite.UserID == userID || !invite.ExpiresAt.After(now) })
	st.Invites = append(st.Invites, Invite{
		TokenHash: tokenHash(token), UserID: userID, CreatedBy: createdBy, CreatedAt: now, ExpiresAt: now.Add(InviteTTL),
	})
	return token, nil
}

// findInvite returns the index of the pending user an unexpired invitation
// token belongs to, or -1.
func (st *state) findInvite(token string, now time.Time) (int, Invite) {
	if token == "" {
		return -1, Invite{}
	}
	hash := tokenHash(token)
	for _, invite := range st.Invites {
		if subtle.ConstantTimeCompare([]byte(invite.TokenHash), []byte(hash)) != 1 || !invite.ExpiresAt.After(now) {
			continue
		}
		i := st.userIndex(invite.UserID)
		if i < 0 || !st.Users[i].Pending() || st.Users[i].Disabled {
			return -1, Invite{}
		}
		return i, invite
	}
	return -1, Invite{}
}

func (st *state) userIndex(id string) int {
	for i := range st.Users {
		if st.Users[i].ID == id {
			return i
		}
	}
	return -1
}

func (st *state) usernameIndex(username string) int {
	username = strings.TrimSpace(username)
	for i := range st.Users {
		if strings.EqualFold(st.Users[i].Username, username) {
			return i
		}
	}
	return -1
}

func (st *state) teamIndex(id string) int {
	for i := range st.Teams {
		if st.Teams[i].ID == id {
			return i
		}
	}
	return -1
}

func (st *state) teamNameTaken(name, exceptID string) bool {
	for _, team := range st.Teams {
		if team.ID != exceptID && strings.EqualFold(team.Name, name) {
			return true
		}
	}
	return false
}

// hasEnabledAdmin reports whether some administrator can still sign in. An
// invited administrator who has not accepted does not count.
func (st *state) hasEnabledAdmin() bool {
	for _, user := range st.Users {
		if user.Role == RoleAdmin && !user.Disabled && !user.Pending() {
			return true
		}
	}
	return false
}

func (st *state) dropSessions(drop func(session) bool) {
	kept := st.Sessions[:0]
	for _, sess := range st.Sessions {
		if !drop(sess) {
			kept = append(kept, sess)
		}
	}
	st.Sessions = kept
}

func (st *state) dropInvites(drop func(Invite) bool) {
	kept := st.Invites[:0]
	for _, invite := range st.Invites {
		if !drop(invite) {
			kept = append(kept, invite)
		}
	}
	st.Invites = kept
}

func tokenHash(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
