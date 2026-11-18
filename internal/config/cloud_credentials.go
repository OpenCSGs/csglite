package config

import (
	"encoding/json"
	"os"
	"strings"
)

// CloudCredentials are the credential fields another csglite process may have
// written to config.json. They move together: a login clears the manual
// gateway key, and a key change belongs to the same account session.
type CloudCredentials struct {
	Token         string
	ServerURL     string
	OpenCSGAPIKey string
	AuthRevision  uint64
}

// CloudCredentialsOnDisk reads the cloud access token, the community it was
// issued for and the login generation from the persisted config without
// touching the process-wide cached Config. A server uses it to notice a login
// written by another csglite process: the loopback SSO callback port is fixed,
// so when two instances run only one of them receives the browser redirect.
// Errors, including a missing or partially written file, mean "nothing to
// adopt" and must never clear credentials held in memory.
func CloudCredentialsOnDisk() (CloudCredentials, error) {
	cfgPath, err := ConfigPath()
	if err != nil {
		return CloudCredentials{}, err
	}
	return readCloudCredentials(cfgPath)
}

// readCloudCredentials reads the login fields from cfgPath. A missing or
// unreadable file is an error, so a caller never mistakes it for a logout.
func readCloudCredentials(cfgPath string) (CloudCredentials, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return CloudCredentials{}, err
	}
	var stored struct {
		Token         string `json:"token"`
		ServerURL     string `json:"server_url"`
		OpenCSGAPIKey string `json:"opencsg_api_key"`
		AuthRevision  uint64 `json:"auth_revision"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return CloudCredentials{}, err
	}
	return CloudCredentials{
		Token:         strings.TrimSpace(stored.Token),
		ServerURL:     strings.TrimSpace(stored.ServerURL),
		OpenCSGAPIKey: strings.TrimSpace(stored.OpenCSGAPIKey),
		AuthRevision:  stored.AuthRevision,
	}, nil
}

// NextAuthRevision moves cfg.AuthRevision one past every revision this process
// has seen, in memory or on disk, before a login, logout or community switch
// is saved. Reading the disk first keeps the counter monotonic when another
// process has advanced it in the meantime. Two processes bumping at the same
// instant can still pick the same number, so one of two simultaneous auth
// changes may not be propagated; the window is between this call and Save.
func NextAuthRevision(cfg *Config) {
	if cfg == nil {
		return
	}
	if cfgPath, err := ConfigPath(); err == nil {
		if disk, err := readCloudCredentials(cfgPath); err == nil && disk.AuthRevision > cfg.AuthRevision {
			cfg.AuthRevision = disk.AuthRevision
		}
	}
	cfg.AuthRevision++
}
