package server

import (
	"context"
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
)

// sessionCookie carries a signed-in user's session token.
const sessionCookie = "csglite_session"

// Codes the web UI switches on.
const (
	loginRequiredCode          = "login_required"
	permissionDeniedCode       = "permission_denied"
	passwordChangeRequiredCode = "password_change_required"
	setupTokenRequiredCode     = "setup_token_required"
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

// requestSeesEverything reports whether the caller reads instance-wide
// records such as every API key: administrators and auditors.
func requestSeesEverything(r *http.Request) bool {
	id, ok := identityFromRequest(r)
	return !ok || team.SeesEverything(id.role())
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
		// A password someone else chose is replaced before anything else is
		// done with the session. API keys are not affected: the user made
		// them after choosing a password of their own, or an administrator
		// made them and is accountable for them.
		if id.SessionToken != "" && id.User.MustChangePassword {
			httpjson.WriteCodedError(w, http.StatusForbidden, "change your password before continuing", passwordChangeRequiredCode)
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

// loginLimiter slows password guessing: after the configured number of wrong
// passwords for one username from one address, that pair is refused for the
// configured lockout.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string]loginFailures
	now      func() time.Time
}

type loginFailures struct {
	count int
	until time.Time
}

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

// locked reports whether key is locked out and for how much longer.
func (l *loginLimiter) locked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.failures[key]
	if !ok {
		return false, 0
	}
	remaining := entry.until.Sub(l.clock())
	return remaining > 0, remaining
}

func (l *loginLimiter) fail(key string, settings team.Settings) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failures == nil {
		l.failures = map[string]loginFailures{}
	}
	now := l.clock()
	for k, entry := range l.failures {
		if !entry.until.IsZero() && !now.Before(entry.until) {
			delete(l.failures, k)
		}
	}
	entry := l.failures[key]
	entry.count++
	if entry.count >= settings.MaxFailedLogins {
		entry.until = now.Add(time.Duration(settings.LockoutMinutes) * time.Minute)
	}
	l.failures[key] = entry
}

func (l *loginLimiter) succeed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}
