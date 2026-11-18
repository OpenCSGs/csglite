package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeTestConfigFile(t *testing.T, content string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	appHome := filepath.Join(home, AppDir)
	if err := os.MkdirAll(appHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appHome, ConfigFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCloudCredentialsOnDisk(t *testing.T) {
	writeTestConfigFile(t, `{"token":" disk-token ","server_url":" https://hub.example.com ","opencsg_api_key":" disk-key ","auth_revision":7}`)

	creds, err := CloudCredentialsOnDisk()
	if err != nil {
		t.Fatal(err)
	}
	if creds.Token != "disk-token" {
		t.Fatalf("token = %q, want %q", creds.Token, "disk-token")
	}
	if creds.ServerURL != "https://hub.example.com" {
		t.Fatalf("server URL = %q, want %q", creds.ServerURL, "https://hub.example.com")
	}
	if creds.OpenCSGAPIKey != "disk-key" {
		t.Fatalf("API key = %q, want %q", creds.OpenCSGAPIKey, "disk-key")
	}
	if creds.AuthRevision != 7 {
		t.Fatalf("auth revision = %d, want 7", creds.AuthRevision)
	}
}

func TestCloudCredentialsOnDiskMissingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if _, err := CloudCredentialsOnDisk(); err == nil {
		t.Fatal("CloudCredentialsOnDisk() error = nil, want an error for a missing config file")
	}
}

// A valid config that has no token yet is "nothing to adopt", not an error.
func TestCloudCredentialsOnDiskWithoutToken(t *testing.T) {
	writeTestConfigFile(t, `{"server_url":"https://hub.example.com"}`)

	creds, err := CloudCredentialsOnDisk()
	if err != nil {
		t.Fatal(err)
	}
	if creds.Token != "" {
		t.Fatalf("token = %q, want empty", creds.Token)
	}
	if creds.ServerURL != "https://hub.example.com" {
		t.Fatalf("server URL = %q, want %q", creds.ServerURL, "https://hub.example.com")
	}
	if creds.AuthRevision != 0 {
		t.Fatalf("auth revision = %d, want 0 for a config written before it existed", creds.AuthRevision)
	}
}

// A login, logout or community switch must move the generation past whatever
// another process has already written.
func TestNextAuthRevisionMovesPastDisk(t *testing.T) {
	writeTestConfigFile(t, `{"auth_revision":5}`)

	cfg := &Config{}
	NextAuthRevision(cfg)
	if cfg.AuthRevision != 6 {
		t.Fatalf("auth revision = %d, want 6", cfg.AuthRevision)
	}

	// A process that already knows a newer generation keeps going from there.
	cfg.AuthRevision = 9
	NextAuthRevision(cfg)
	if cfg.AuthRevision != 10 {
		t.Fatalf("auth revision = %d, want 10", cfg.AuthRevision)
	}
}

// Saving a whole in-memory config from a stale process must not overwrite a
// login another process applied after its own, or a restart would pick up the
// older token again. The manual key belongs to the login generation, so the
// disk state wins for it too.
func TestSaveKeepsNewerDiskLogin(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	writeTestConfigFile(t, `{"token":"disk-token","server_url":"https://disk.example.com","auth_revision":5}`)

	cfg := &Config{
		Token:         "stale-token",
		ServerURL:     DefaultServerURL,
		AuthRevision:  2,
		OpenCSGAPIKey: "local-key",
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "stale-token" || cfg.AuthRevision != 2 {
		t.Fatalf("in-memory auth = %q/%d, want the stale values left untouched", cfg.Token, cfg.AuthRevision)
	}
	creds, err := CloudCredentialsOnDisk()
	if err != nil {
		t.Fatal(err)
	}
	if creds.Token != "disk-token" || creds.ServerURL != "https://disk.example.com" || creds.AuthRevision != 5 {
		t.Fatalf("saved credentials = %+v, want the newer disk login", creds)
	}
	// The disk login cleared the manual key of the account it replaced; writing
	// the stale process's key back would pair the new account with the old key.
	if got := readSavedConfig(t).OpenCSGAPIKey; got != "" {
		t.Fatalf("saved API key = %q, want the disk (cleared) value", got)
	}
}

// A key saved together with the newer login is adopted as well.
func TestSaveKeepsNewerDiskAPIKey(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	writeTestConfigFile(t, `{"token":"disk-token","server_url":"https://hub.example.com","opencsg_api_key":"disk-key","auth_revision":5}`)

	cfg := &Config{
		Token:         "stale-token",
		ServerURL:     "https://hub.example.com",
		AuthRevision:  2,
		OpenCSGAPIKey: "local-key",
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	if got := readSavedConfig(t).OpenCSGAPIKey; got != "disk-key" {
		t.Fatalf("saved API key = %q, want the disk key", got)
	}
}

// A logout in the other instance must survive a stale save here: writing the
// old token back would sign the user in again on the next start.
func TestSaveKeepsNewerDiskLogout(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	writeTestConfigFile(t, `{"token":"","server_url":"https://hub.example.com","auth_revision":5}`)

	cfg := &Config{Token: "stale-token", ServerURL: "https://hub.example.com", AuthRevision: 2}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	creds, err := CloudCredentialsOnDisk()
	if err != nil {
		t.Fatal(err)
	}
	if creds.Token != "" {
		t.Fatalf("saved token = %q, want the newer logout kept", creds.Token)
	}
	if creds.AuthRevision != 5 {
		t.Fatalf("saved auth revision = %d, want 5", creds.AuthRevision)
	}
}

// The process that changed the login last owns the file; its own save is not
// overridden by an older disk state.
func TestSaveWritesLocalLoginWhenItIsNewest(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	writeTestConfigFile(t, `{"token":"old-token","server_url":"https://hub.example.com","auth_revision":2}`)

	cfg := &Config{
		Token:         "new-token",
		ServerURL:     "https://hub.example.com",
		AuthRevision:  3,
		OpenCSGAPIKey: "new-key",
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	creds, err := CloudCredentialsOnDisk()
	if err != nil {
		t.Fatal(err)
	}
	if creds.Token != "new-token" || creds.AuthRevision != 3 {
		t.Fatalf("saved credentials = %+v, want the local login", creds)
	}
	if creds.OpenCSGAPIKey != "new-key" {
		t.Fatalf("saved API key = %q, want the local key", creds.OpenCSGAPIKey)
	}
}

func readSavedConfig(t *testing.T) Config {
	t.Helper()
	cfgPath, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A half-written config must report an error instead of empty credentials: the
// caller treats errors as "nothing to adopt" and keeps its in-memory login.
func TestCloudCredentialsOnDiskMalformedFile(t *testing.T) {
	writeTestConfigFile(t, `{"token":"disk-token"`)

	if _, err := CloudCredentialsOnDisk(); err == nil {
		t.Fatal("CloudCredentialsOnDisk() error = nil, want an error for malformed JSON")
	}
}
