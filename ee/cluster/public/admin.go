// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package public

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const AdminCookie = "__Host-csglite_admin"
const CSRFCookie = "__Host-csglite_csrf"
const CSRFHeader = "X-CSGLite-CSRF"

// AdminTokenPath deliberately separates administrative authority from inference
// API keys and node enrollment credentials. Only a hash is persisted.
func AdminTokenPath(storageRoot string) string {
	return filepath.Join(storageRoot, "cluster", "public_admin.json")
}

type adminCredential struct {
	Hash      string    `json:"token_hash"`
	CreatedAt time.Time `json:"created_at"`
}

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// GenerateAdminToken rotates the credential atomically. The plaintext is returned
// once to the CLI and is never written to the application's credential store.
func GenerateAdminToken(path string) (string, error) {
	secret, err := randomSecret()
	if err != nil {
		return "", err
	}
	token := "csgadmin_" + secret
	data, err := json.MarshalIndent(adminCredential{tokenHash(token), time.Now().UTC()}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".public-admin-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	return token, nil
}

type adminSession struct {
	Credential string
	CSRF       string
	Expires    time.Time
}
type loginAttempt struct {
	Count int
	Until time.Time
}

type AdminAuth struct {
	path     string
	mu       sync.Mutex
	sessions map[string]adminSession
	attempts map[string]loginAttempt
}

func NewAdminAuth(path string) *AdminAuth {
	return &AdminAuth{path: path, sessions: make(map[string]adminSession), attempts: make(map[string]loginAttempt)}
}

func (a *AdminAuth) credential() (string, error) {
	data, err := os.ReadFile(a.path)
	if err != nil {
		return "", err
	}
	var c adminCredential
	if err := json.Unmarshal(data, &c); err != nil {
		return "", err
	}
	if len(c.Hash) != 64 {
		return "", fmt.Errorf("invalid administrative credential")
	}
	return c.Hash, nil
}

func equalSecret(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// SameOrigin also works behind a TLS proxy: public origins are always HTTPS.
func SameOrigin(r *http.Request) bool {
	u, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && u.Scheme == "https" && strings.EqualFold(u.Host, r.Host) && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

func adminJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (a *AdminAuth) Login(w http.ResponseWriter, r *http.Request) {
	if !SameOrigin(r) {
		adminJSON(w, 403, map[string]string{"error": "same origin required"})
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	a.mu.Lock()
	now := time.Now()
	for key, attempt := range a.attempts {
		if now.After(attempt.Until) {
			delete(a.attempts, key)
		}
	}
	attempt := a.attempts[ip]
	if attempt.Count >= 10 || len(a.attempts) >= 1024 {
		a.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		adminJSON(w, 429, map[string]string{"error": "too many login attempts"})
		return
	}
	if attempt.Count == 0 {
		attempt.Until = now.Add(time.Minute)
	}
	attempt.Count++
	a.attempts[ip] = attempt
	a.mu.Unlock()
	var body struct {
		Token string `json:"token"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		adminJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	hash, err := a.credential()
	if err != nil || !equalSecret(tokenHash(strings.TrimSpace(body.Token)), hash) {
		adminJSON(w, 401, map[string]string{"error": "invalid login token"})
		return
	}
	secret, err := randomSecret()
	if err != nil {
		adminJSON(w, 500, map[string]string{"error": "cannot create session"})
		return
	}
	csrf, err := randomSecret()
	if err != nil {
		adminJSON(w, 500, map[string]string{"error": "cannot create session"})
		return
	}
	expires := now.Add(8 * time.Hour)
	a.mu.Lock()
	for key, session := range a.sessions {
		if now.After(session.Expires) || session.Credential != hash {
			delete(a.sessions, key)
		}
	}
	if len(a.sessions) >= 256 {
		a.mu.Unlock()
		adminJSON(w, 429, map[string]string{"error": "too many sessions"})
		return
	}
	// Logging in again replaces this browser's prior session.
	if cookie, err := r.Cookie(AdminCookie); err == nil {
		delete(a.sessions, tokenHash(cookie.Value))
	}
	a.sessions[tokenHash(secret)] = adminSession{hash, csrf, expires}
	delete(a.attempts, ip)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: AdminCookie, Value: secret, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 3600, Expires: expires})
	http.SetCookie(w, &http.Cookie{Name: CSRFCookie, Value: csrf, Path: "/", Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 3600, Expires: expires})
	adminJSON(w, 200, map[string]any{"authenticated": true, "expires_at": expires})
}

func (a *AdminAuth) Authenticate(r *http.Request) bool {
	cookie, err := r.Cookie(AdminCookie)
	if err != nil {
		return false
	}
	hash, err := a.credential()
	if err != nil {
		return false
	}
	a.mu.Lock()
	session, ok := a.sessions[tokenHash(cookie.Value)]
	if !ok || time.Now().After(session.Expires) || !equalSecret(session.Credential, hash) {
		delete(a.sessions, tokenHash(cookie.Value))
		a.mu.Unlock()
		return false
	}
	a.mu.Unlock()
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return SameOrigin(r) && equalSecret(r.Header.Get(CSRFHeader), session.CSRF)
	}
	// WebSocket upgrades need Origin validation because they can mutate state.
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return SameOrigin(r)
	}
	return r.Header.Get("Origin") == "" || SameOrigin(r)
}

func (a *AdminAuth) Logout(w http.ResponseWriter, r *http.Request) {
	if !a.Authenticate(r) {
		adminJSON(w, 401, map[string]string{"error": "session and CSRF token required"})
		return
	}
	cookie, _ := r.Cookie(AdminCookie)
	a.mu.Lock()
	delete(a.sessions, tokenHash(cookie.Value))
	a.mu.Unlock()
	for _, name := range []string{AdminCookie, CSRFCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Path: "/", Secure: true, HttpOnly: name == AdminCookie, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	}
	adminJSON(w, 200, map[string]bool{"authenticated": false})
}

// SessionExpiry returns metadata only after validating the session and origin.
func (a *AdminAuth) SessionExpiry(r *http.Request) (time.Time, bool) {
	if !a.Authenticate(r) {
		return time.Time{}, false
	}
	cookie, _ := r.Cookie(AdminCookie)
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[tokenHash(cookie.Value)]
	return session.Expires, ok
}
