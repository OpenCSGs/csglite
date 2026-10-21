// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

// Package team keeps the user accounts, teams and login sessions of a CSGLite
// instance shared by several people. Accounts are switched on by creating the
// first user; until then the instance behaves exactly as a single-user
// installation does.
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

// File is the runtime file under the app home that holds users, teams and
// sessions. It lives beside api_keys.json rather than in config.json because
// it holds credentials and changes on every login.
const File = "team.json"

// SessionTTL is how long a login lasts. Sessions do not slide, so a stolen
// cookie stops working on a fixed schedule.
const SessionTTL = 7 * 24 * time.Hour

const minPasswordLength = 8

// Role decides what a signed-in user may do.
type Role string

const (
	// RoleAdmin administers the instance: providers, models, settings, users.
	RoleAdmin Role = "admin"
	// RoleMember uses models: chat, inference and their own API keys.
	RoleMember Role = "member"
	// RoleViewer reads dashboards and cannot run inference or change anything.
	RoleViewer Role = "viewer"
)

// ValidRole reports whether role is one of the three roles.
func ValidRole(role Role) bool {
	switch role {
	case RoleAdmin, RoleMember, RoleViewer:
		return true
	}
	return false
}

// User is one account. PasswordHash never leaves this package's callers in an
// API response; the server maps users onto a public view.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name,omitempty"`
	Email        string    `json:"email,omitempty"`
	Role         Role      `json:"role"`
	TeamID       string    `json:"team_id,omitempty"`
	Disabled     bool      `json:"disabled,omitempty"`
	PasswordHash string    `json:"password_hash"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	LastLoginAt  time.Time `json:"last_login_at,omitempty"`
}

// Team groups users. Usage and budgets are attributed to the team of the user
// who owns a request.
type Team struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	Sessions []session `json:"sessions"`
}

// Errors returned for requests the caller can correct. The server maps them
// onto 400, 404 and 409 responses.
var (
	ErrNotFound          = errors.New("not found")
	ErrUsernameTaken     = errors.New("username already exists")
	ErrTeamNameTaken     = errors.New("team name already exists")
	ErrInvalidUsername   = errors.New("username must be 2-64 characters of letters, digits, '.', '_', '-' or '@'")
	ErrWeakPassword      = fmt.Errorf("password must be at least %d characters", minPasswordLength)
	ErrInvalidRole       = errors.New("role must be admin, member or viewer")
	ErrUnknownTeam       = errors.New("team does not exist")
	ErrLastAdmin         = errors.New("at least one enabled administrator must remain")
	ErrInvalidCredential = errors.New("invalid username or password")
	ErrTeamNameRequired  = errors.New("team name is required")
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{2,64}$`)

// Store reads and writes team.json. Every method loads the file, so a change
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
	users := append([]User(nil), st.Users...)
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

// Teams returns every team, ordered by name.
func (s *Store) Teams() ([]Team, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	teams := append([]Team(nil), st.Teams...)
	sort.Slice(teams, func(i, j int) bool { return strings.ToLower(teams[i].Name) < strings.ToLower(teams[j].Name) })
	return teams, nil
}

// NewUser is what CreateUser needs. The first user of an instance is always
// made an administrator, whatever Role says, so turning accounts on can
// never lock everyone out.
type NewUser struct {
	Username    string
	Password    string
	DisplayName string
	Email       string
	Role        Role
	TeamID      string
}

// CreateUser adds a user. maxUsers caps the total number of users; 0 means
// unlimited. ErrSeatLimit is returned when the cap is reached.
func (s *Store) CreateUser(in NewUser, maxUsers int) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return User{}, err
	}
	username := strings.TrimSpace(in.Username)
	if !usernamePattern.MatchString(username) {
		return User{}, ErrInvalidUsername
	}
	if st.usernameTaken(username, "") {
		return User{}, ErrUsernameTaken
	}
	if len(in.Password) < minPasswordLength {
		return User{}, ErrWeakPassword
	}
	role := in.Role
	if len(st.Users) == 0 {
		role = RoleAdmin
	}
	if role == "" {
		role = RoleMember
	}
	if !ValidRole(role) {
		return User{}, ErrInvalidRole
	}
	teamID := strings.TrimSpace(in.TeamID)
	if teamID != "" && st.teamIndex(teamID) < 0 {
		return User{}, ErrUnknownTeam
	}
	if maxUsers > 0 && len(st.Users) >= maxUsers {
		return User{}, &SeatLimitError{Limit: maxUsers, Current: len(st.Users)}
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return User{}, err
	}
	id, err := randomHex(8)
	if err != nil {
		return User{}, err
	}
	now := s.now().UTC()
	user := User{
		ID:           id,
		Username:     username,
		DisplayName:  strings.TrimSpace(in.DisplayName),
		Email:        strings.TrimSpace(in.Email),
		Role:         role,
		TeamID:       teamID,
		PasswordHash: hash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	st.Users = append(st.Users, user)
	if err := s.saveLocked(st); err != nil {
		return User{}, err
	}
	return user, nil
}

// SeatLimitError reports that the licensed number of users is reached.
type SeatLimitError struct {
	Limit   int
	Current int
}

func (e *SeatLimitError) Error() string {
	return fmt.Sprintf("the license allows %d users and %d already exist", e.Limit, e.Current)
}

// UserPatch changes the fields that are set. Password resets the password and
// ends the user's sessions.
type UserPatch struct {
	DisplayName *string
	Email       *string
	Role        *Role
	TeamID      *string
	Disabled    *bool
	Password    *string
}

// UpdateUser applies patch. It refuses a change that would leave no enabled
// administrator. A user who is disabled, demoted or given a new password is
// signed out everywhere.
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
		endSessions = true
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

// DeleteUser removes a user and their sessions.
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
	st.Users[i].UpdatedAt = s.now().UTC()
	keep := tokenHash(keepToken)
	st.dropSessions(func(sess session) bool { return sess.UserID == id && sess.TokenHash != keep })
	return s.saveLocked(st)
}

// Login checks a username and password and opens a session. It returns the
// session token to set as a cookie. A disabled user cannot sign in.
func (s *Store) Login(username, password string) (User, string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	i := -1
	for j := range st.Users {
		if strings.EqualFold(st.Users[j].Username, strings.TrimSpace(username)) {
			i = j
			break
		}
	}
	if i < 0 {
		// Hash anyway so a missing user takes as long as a wrong password.
		_ = checkPassword(dummyPasswordHash(), password)
		return User{}, "", time.Time{}, ErrInvalidCredential
	}
	if !checkPassword(st.Users[i].PasswordHash, password) || st.Users[i].Disabled {
		return User{}, "", time.Time{}, ErrInvalidCredential
	}
	token, err := randomHex(32)
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	now := s.now().UTC()
	expires := now.Add(SessionTTL)
	st.dropSessions(func(sess session) bool { return !sess.ExpiresAt.After(now) })
	st.Sessions = append(st.Sessions, session{
		TokenHash: tokenHash(token), UserID: st.Users[i].ID, CreatedAt: now, ExpiresAt: expires,
	})
	st.Users[i].LastLoginAt = now
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
	s.cached, s.cachedSize, s.cachedMTime, s.cachedOK = st.clone(), info.Size(), info.ModTime(), true
	return st, nil
}

// clone copies the slices so a caller changing the returned state cannot
// change the cache.
func (st state) clone() state {
	return state{
		Users:    append([]User(nil), st.Users...),
		Teams:    append([]Team(nil), st.Teams...),
		Sessions: append([]session(nil), st.Sessions...),
	}
}

func (s *Store) saveLocked(st state) error {
	if st.Users == nil {
		st.Users = []User{}
	}
	if st.Teams == nil {
		st.Teams = []Team{}
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

func (st *state) userIndex(id string) int {
	for i := range st.Users {
		if st.Users[i].ID == id {
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

func (st *state) usernameTaken(username, exceptID string) bool {
	for _, user := range st.Users {
		if user.ID != exceptID && strings.EqualFold(user.Username, username) {
			return true
		}
	}
	return false
}

func (st *state) teamNameTaken(name, exceptID string) bool {
	for _, team := range st.Teams {
		if team.ID != exceptID && strings.EqualFold(team.Name, name) {
			return true
		}
	}
	return false
}

func (st *state) hasEnabledAdmin() bool {
	for _, user := range st.Users {
		if user.Role == RoleAdmin && !user.Disabled {
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
