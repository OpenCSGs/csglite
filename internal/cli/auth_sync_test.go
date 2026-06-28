package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
)

func TestSyncRunningServerCloudTokenSavesToken(t *testing.T) {
	var gotToken string
	server := newTokenSyncTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode token request: %v", err)
		}
		gotToken = body["token"]
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()

	cfg := &config.Config{
		ListenAddr: strings.TrimPrefix(server.URL, "http://"),
		Token:      " test-token ",
	}
	if err := syncRunningServerCloudToken(cfg); err != nil {
		t.Fatalf("syncRunningServerCloudToken() error: %v", err)
	}
	if gotToken != "test-token" {
		t.Fatalf("token = %q, want test-token", gotToken)
	}
}

func TestSyncRunningServerCloudTokenDeletesEmptyToken(t *testing.T) {
	var gotDelete bool
	server := newTokenSyncTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", r.Method)
		}
		gotDelete = true
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()

	cfg := &config.Config{ListenAddr: strings.TrimPrefix(server.URL, "http://")}
	if err := syncRunningServerCloudToken(cfg); err != nil {
		t.Fatalf("syncRunningServerCloudToken() error: %v", err)
	}
	if !gotDelete {
		t.Fatal("DELETE /api/cloud/auth/token was not called")
	}
}

func TestLoginCommandSyncsRunningServerToken(t *testing.T) {
	setupCLIConfigHome(t)
	var gotToken string
	server := newTokenSyncTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode token request: %v", err)
		}
		gotToken = body["token"]
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()
	saveListenAddr(t, strings.TrimPrefix(server.URL, "http://"))

	cmd := newLoginCmd()
	cmd.SetArgs([]string{"--token", "test-token"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("login command error: %v", err)
	}
	if gotToken != "test-token" {
		t.Fatalf("synced token = %q, want test-token", gotToken)
	}
}

func TestRunConfigSetTokenSyncsRunningServerToken(t *testing.T) {
	setupCLIConfigHome(t)
	var gotToken string
	server := newTokenSyncTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode token request: %v", err)
		}
		gotToken = body["token"]
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()
	saveListenAddr(t, strings.TrimPrefix(server.URL, "http://"))

	if err := runConfigSet(nil, []string{"token", "new-token"}); err != nil {
		t.Fatalf("runConfigSet(token) error: %v", err)
	}
	if gotToken != "new-token" {
		t.Fatalf("synced token = %q, want new-token", gotToken)
	}
}

func TestEnsureServerSyncsConfiguredToken(t *testing.T) {
	var gotToken string
	server := newTokenSyncTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode token request: %v", err)
		}
		gotToken = body["token"]
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()

	cfg := &config.Config{
		ListenAddr: strings.TrimPrefix(server.URL, "http://"),
		Token:      "existing-token",
	}
	serverURL, err := ensureServer(cfg)
	if err != nil {
		t.Fatalf("ensureServer() error: %v", err)
	}
	if serverURL != server.URL {
		t.Fatalf("serverURL = %q, want %q", serverURL, server.URL)
	}
	if gotToken != "existing-token" {
		t.Fatalf("synced token = %q, want existing-token", gotToken)
	}
}

func newTokenSyncTestServer(t *testing.T, authHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			w.WriteHeader(http.StatusOK)
		case "/api/cloud/auth/token":
			authHandler(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
}

func saveListenAddr(t *testing.T, listenAddr string) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}
	cfg.ListenAddr = listenAddr
	if err := config.Save(cfg); err != nil {
		t.Fatalf("config.Save() error: %v", err)
	}
}
