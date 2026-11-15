// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package public

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// AdminTokenInfo is safe for CLI listings. Neither plaintext nor token hashes
// leave the credential store through this type.
type AdminTokenInfo struct {
	ID        string     `json:"id"`
	Name      string     `json:"name,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type adminCredentialState struct {
	Version int               `json:"version"`
	Tokens  []adminCredential `json:"tokens"`
}

func (s adminCredentialState) find(hash string, now time.Time) (adminCredential, bool) {
	for _, c := range s.Tokens {
		if equalSecret(c.Hash, hash) && (c.ExpiresAt == nil || now.Before(*c.ExpiresAt)) {
			return c, true
		}
	}
	return adminCredential{}, false
}

func (c adminCredential) info() AdminTokenInfo {
	return AdminTokenInfo{ID: c.ID, Name: c.Name, CreatedAt: c.CreatedAt, ExpiresAt: c.ExpiresAt}
}

func loadAdminCredentials(path string) (adminCredentialState, error) {
	state := adminCredentialState{Version: 1, Tokens: []adminCredential{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	var format struct {
		Version *int            `json:"version"`
		Tokens  json.RawMessage `json:"tokens"`
	}
	if err := json.Unmarshal(data, &format); err != nil {
		return state, fmt.Errorf("read management tokens: %w", err)
	}
	if format.Version == nil && format.Tokens == nil {
		// Read the original single-token schema, preserving its hash and exact
		// expiry. The next mutation writes only the versioned collection.
		var legacy adminCredential
		if err := json.Unmarshal(data, &legacy); err != nil || !validCredentialHex(legacy.Hash, 64) {
			return state, fmt.Errorf("invalid legacy management credential")
		}
		legacy.ID = legacy.Hash[:16]
		state.Tokens = append(state.Tokens, legacy)
	} else {
		if format.Version == nil || *format.Version != 1 || string(format.Tokens) == "null" || format.Tokens == nil {
			return state, fmt.Errorf("unsupported management credential format")
		}
		if err := json.Unmarshal(data, &state); err != nil {
			return state, fmt.Errorf("read management tokens: %w", err)
		}
	}
	ids, hashes := map[string]bool{}, map[string]bool{}
	for _, c := range state.Tokens {
		if !validCredentialHex(c.Hash, 64) || !validCredentialHex(c.ID, 16) || ids[c.ID] || hashes[c.Hash] {
			return adminCredentialState{}, fmt.Errorf("invalid or duplicate management credential")
		}
		ids[c.ID], hashes[c.Hash] = true, true
	}
	return state, nil
}

func validCredentialHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func saveAdminCredentials(path string, state adminCredentialState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".public-admin-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Credential changes are serialized across CLI processes. Authentication only
// reads atomically replaced snapshots, so issuing/revoking needs no restart.
func lockAdminCredentials(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockAdminFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { unlockAdminFile(f); _ = f.Close() }, nil
}

// CreateAdminToken adds one independent administrator credential. All tokens
// grant the existing administrator role; this does not create CSGHub accounts.
func CreateAdminToken(path, name string, ttl time.Duration) (AdminTokenInfo, string, error) {
	if ttl < 0 {
		return AdminTokenInfo{}, "", fmt.Errorf("token TTL must not be negative")
	}
	name = strings.TrimSpace(name)
	if len(name) > 128 || strings.ContainsFunc(name, unicode.IsControl) {
		return AdminTokenInfo{}, "", fmt.Errorf("token name must be at most 128 bytes without control characters")
	}
	unlock, err := lockAdminCredentials(path)
	if err != nil {
		return AdminTokenInfo{}, "", err
	}
	defer unlock()
	state, err := loadAdminCredentials(path)
	if err != nil {
		return AdminTokenInfo{}, "", err
	}
	secret, err := randomSecret()
	if err != nil {
		return AdminTokenInfo{}, "", err
	}
	token := "csgadmin_" + secret
	hash := tokenHash(token)
	c := adminCredential{ID: hash[:16], Name: name, Hash: hash, CreatedAt: time.Now().UTC()}
	if ttl > 0 {
		expires := c.CreatedAt.Add(ttl)
		c.ExpiresAt = &expires
	}
	for _, existing := range state.Tokens {
		if existing.ID == c.ID {
			return AdminTokenInfo{}, "", fmt.Errorf("management token ID collision; retry generation")
		}
	}
	state.Tokens = append(state.Tokens, c)
	if err := saveAdminCredentials(path, state); err != nil {
		return AdminTokenInfo{}, "", err
	}
	return c.info(), token, nil
}

func ListAdminTokens(path string) ([]AdminTokenInfo, error) {
	state, err := loadAdminCredentials(path)
	if err != nil {
		return nil, err
	}
	out := make([]AdminTokenInfo, 0, len(state.Tokens))
	for _, c := range state.Tokens {
		out = append(out, c.info())
	}
	return out, nil
}

// RevokeAdminToken invalidates only this credential and its sessions. Sessions
// check membership and expiry against the latest snapshot on every request.
func RevokeAdminToken(path, id string) (bool, error) {
	if !validCredentialHex(id, 16) {
		return false, fmt.Errorf("a management token ID from login-token list is required")
	}
	unlock, err := lockAdminCredentials(path)
	if err != nil {
		return false, err
	}
	defer unlock()
	state, err := loadAdminCredentials(path)
	if err != nil {
		return false, err
	}
	for i, c := range state.Tokens {
		if c.ID == id {
			state.Tokens = append(state.Tokens[:i], state.Tokens[i+1:]...)
			return true, saveAdminCredentials(path, state)
		}
	}
	return false, nil
}
