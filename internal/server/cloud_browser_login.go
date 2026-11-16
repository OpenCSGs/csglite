package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
)

const browserLoginCookie = "csglite_cloud_login"
const browserLoginCallback = "/api/cloud/auth/callback"

type browserLoginAttempt struct {
	platform string
	origin   string
	expires  time.Time
	revision uint64
}

func browserLoginOrigin(r *http.Request) (string, bool) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	parsed, err := url.Parse(scheme + "://" + r.Host)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	return parsed.String(), true
}

func browserLoginStateCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: browserLoginCookie, Value: value, Path: browserLoginCallback, MaxAge: maxAge, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode}
}

// Both local and remote browsers carry their own callback URL in OAuth state,
// as Scion does. The portal's registered Casdoor redirect_uri stays unchanged.
func (s *Server) handleCloudBrowserLogin(w http.ResponseWriter, r *http.Request) {
	origin, ok := browserLoginOrigin(r)
	if !ok {
		writeError(w, 400, "invalid browser origin")
		return
	}
	s.cloudLoginMu.Lock()
	platform, revision := s.cfg.ServerURL, s.cloudLoginRevision
	s.cloudLoginMu.Unlock()
	authorize, err := url.Parse(communityLoginURLForServer(r.Context(), platform))
	if err != nil || authorize.Host == "" || authorize.Query().Get("client_id") == "" {
		writeError(w, 502, "community browser login is unavailable")
		return
	}
	var bytes [32]byte
	if _, err = rand.Read(bytes[:]); err != nil {
		writeError(w, 500, "could not start browser login")
		return
	}
	nonce := hex.EncodeToString(bytes[:])
	now := time.Now()
	s.cloudLoginMu.Lock()
	if platform != s.cfg.ServerURL || revision != s.cloudLoginRevision {
		s.cloudLoginMu.Unlock()
		writeError(w, 409, "community changed while starting browser login")
		return
	}
	if s.cloudLoginAttempts == nil {
		s.cloudLoginAttempts = make(map[string]browserLoginAttempt)
	}
	for key, attempt := range s.cloudLoginAttempts {
		if now.After(attempt.expires) {
			delete(s.cloudLoginAttempts, key)
		}
	}
	if len(s.cloudLoginAttempts) >= 128 {
		s.cloudLoginMu.Unlock()
		writeError(w, 429, "too many pending browser logins")
		return
	}
	s.cloudLoginAttempts[nonce] = browserLoginAttempt{platform: platform, origin: origin, expires: now.Add(10 * time.Minute), revision: revision}
	s.cloudLoginMu.Unlock()
	callback := origin + browserLoginCallback + "?state=" + url.QueryEscape(nonce)
	query := authorize.Query()
	query.Set("state", callback)
	authorize.RawQuery = query.Encode()
	http.SetCookie(w, browserLoginStateCookie(r, nonce, 600))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]string{"login_url": authorize.String()})
}

// This is the only unauthenticated management callback. The initiating
// browser's short-lived HttpOnly nonce and the server's one-use attempt are
// required before a credential is accepted. Legacy token= callbacks remain
// restricted to the dedicated loopback listener.
func (s *Server) handleCloudBrowserCallback(w http.ResponseWriter, r *http.Request) {
	nonce := r.URL.Query().Get("state")
	cookie, err := r.Cookie(browserLoginCookie)
	if err != nil || len(nonce) != 64 || !secureTokenEqual(cookie.Value, nonce) {
		writeError(w, 403, "browser login state is invalid or expired")
		return
	}
	origin, ok := browserLoginOrigin(r)
	s.cloudLoginMu.Lock()
	attempt, exists := s.cloudLoginAttempts[nonce]
	if exists {
		delete(s.cloudLoginAttempts, nonce)
	}
	valid := ok && exists && time.Now().Before(attempt.expires) && attempt.origin == origin && attempt.platform == s.cfg.ServerURL && attempt.revision == s.cloudLoginRevision
	s.cloudLoginMu.Unlock()
	http.SetCookie(w, browserLoginStateCookie(r, "", -1))
	if !valid {
		writeError(w, 403, "browser login state is invalid or expired")
		return
	}
	jwt := strings.TrimSpace(r.URL.Query().Get("jwt_token"))
	if jwt == "" {
		writeCloudAuthCallbackPage(w, false, "missing_token")
		return
	}
	token, err := csghub.NewClient(attempt.platform, jwt).BrowserLoginToken(r.Context())
	if err != nil {
		writeCloudAuthCallbackPage(w, false, "login_failed")
		return
	}
	// Disconnecting or replacing credentials cancels in-flight SSO returns.
	s.cloudLoginMu.Lock()
	defer s.cloudLoginMu.Unlock()
	if attempt.platform != s.cfg.ServerURL || attempt.revision != s.cloudLoginRevision {
		writeCloudAuthCallbackPage(w, false, "login_cancelled")
		return
	}
	if err = s.saveCloudAccessTokenLocked(token); err != nil {
		writeCloudAuthCallbackPage(w, false, "save_failed")
		return
	}
	writeCloudAuthCallbackPage(w, true, "")
}
