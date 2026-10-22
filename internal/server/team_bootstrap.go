package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/internal/safefile"
	"github.com/opencsgs/csglite/internal/team"
)

// Environment variables that create the first administrator at start-up, for
// containers and unattended installs. They only act while no user exists, so
// leaving them set never resets a password; the Users & Teams page warns when
// they outlive their use.
const (
	envAdminUsername     = "CSGHUB_LITE_ADMIN_USERNAME"
	envAdminPassword     = "CSGHUB_LITE_ADMIN_PASSWORD"
	envAdminPasswordFile = "CSGHUB_LITE_ADMIN_PASSWORD_FILE"
	defaultAdminUsername = "admin"
)

// setupTokenFile holds the one-time token that lets someone on another
// machine create the first administrator. It is written only to this file,
// never to the log: before accounts exist the log is readable over the
// network.
const setupTokenFile = "team-setup-token"

// bootstrapAdminPassword returns the administrator password from the
// environment, and whether one is configured.
func bootstrapAdminPassword() (string, bool) {
	if password := os.Getenv(envAdminPassword); password != "" {
		return password, true
	}
	path := strings.TrimSpace(os.Getenv(envAdminPasswordFile))
	if path == "" {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("accounts: reading %s: %v", envAdminPasswordFile, err)
		return "", true
	}
	return strings.TrimRight(string(data), "\r\n"), true
}

// bootstrapAccounts runs at start-up. With no user yet, it creates the
// administrator named by the environment, or prepares a setup token so the
// first administrator can be created from another machine without anyone on
// the network claiming the instance first.
func (s *Server) bootstrapAccounts() {
	if s.team == nil {
		return
	}
	password, envSet := bootstrapAdminPassword()
	if s.accountsEnabled() {
		s.clearSetupToken()
		if envSet {
			log.Printf("accounts: %s is set but accounts already exist, so it is ignored; remove it", envAdminPassword)
		}
		return
	}
	if envSet {
		username := strings.TrimSpace(os.Getenv(envAdminUsername))
		if username == "" {
			username = defaultAdminUsername
		}
		_, _, err := s.team.CreateUser(team.NewUser{Username: username, Password: password, SelfChosen: true})
		if err != nil {
			log.Printf("accounts: creating administrator %q from %s: %v", username, envAdminPassword, err)
			return
		}
		log.Printf("accounts: created administrator %q from the environment; remote access now requires signing in", username)
		s.clearSetupToken()
		return
	}
	if _, err := s.ensureSetupToken(); err != nil {
		log.Printf("accounts: preparing the setup token: %v", err)
	}
}

func (s *Server) setupTokenPath() string {
	if s.team == nil {
		return setupTokenFile
	}
	return filepath.Join(filepath.Dir(s.team.Path()), setupTokenFile)
}

// ensureSetupToken returns the setup token, creating it when missing.
func (s *Server) ensureSetupToken() (string, error) {
	s.setupTokenMu.Lock()
	defer s.setupTokenMu.Unlock()
	path := s.setupTokenPath()
	if data, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(data)); token != "" {
			return token, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	if err := safefile.Write(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	log.Printf("accounts: no administrator yet. Create one in the web UI on this machine, set %s, or from another machine use the setup token in %s", envAdminPassword, path)
	return token, nil
}

// checkSetupToken reports whether got is the setup token. A missing token is
// created first, so a license imported after start-up still gets one.
func (s *Server) checkSetupToken(got string) bool {
	want, err := s.ensureSetupToken()
	if err != nil {
		log.Printf("accounts: preparing the setup token: %v", err)
		return false
	}
	return secureEqual(strings.TrimSpace(got), want)
}

func (s *Server) clearSetupToken() {
	s.setupTokenMu.Lock()
	defer s.setupTokenMu.Unlock()
	if err := os.Remove(s.setupTokenPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("accounts: removing the setup token: %v", err)
	}
}
