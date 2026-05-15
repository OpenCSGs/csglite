package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csghub-lite/internal/cloud"
	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/inference"
)

func newCloudAuthAPIServer(t *testing.T, token, username string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/token/" + token:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"msg": "OK",
				"data": map[string]any{
					"token":       token,
					"token_name":  "token-name",
					"application": "git",
					"user_name":   username,
					"user_uuid":   "user-1",
				},
			})
		case "/api/v1/user/" + username:
			if got := r.Header.Get("Authorization"); got != "Bearer "+token {
				t.Fatalf("Authorization = %q, want %q", got, "Bearer "+token)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"msg": "OK",
				"data": map[string]any{
					"username": username,
					"nickname": "Alice",
					"email":    "alice@example.com",
					"avatar":   "https://example.com/alice.png",
					"uuid":     "user-1",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestHandleCloudAuthStatus(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/cloud/auth", nil)
	w := httptest.NewRecorder()
	s.handleCloudAuthStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp cloudAuthStatus
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.AuthMode != "token" {
		t.Fatalf("AuthMode = %q, want token", resp.AuthMode)
	}
	if resp.LoginURL != cloud.DefaultLoginURL {
		t.Fatalf("LoginURL = %q, want %q", resp.LoginURL, cloud.DefaultLoginURL)
	}
	if resp.HasToken {
		t.Fatalf("HasToken = true, want false")
	}
	if resp.Authenticated {
		t.Fatalf("Authenticated = true, want false")
	}
	if resp.User != nil {
		t.Fatalf("User = %#v, want nil", resp.User)
	}
}

func TestHandleCloudAuthStatusWithUser(t *testing.T) {
	api := newCloudAuthAPIServer(t, "test-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL
	s.cfg.Token = "test-token"

	req := httptest.NewRequest(http.MethodGet, "/api/cloud/auth", nil)
	w := httptest.NewRecorder()
	s.handleCloudAuthStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp cloudAuthStatus
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.HasToken {
		t.Fatalf("HasToken = false, want true")
	}
	if !resp.Authenticated {
		t.Fatalf("Authenticated = false, want true")
	}
	if resp.User == nil {
		t.Fatal("User = nil, want non-nil")
	}
	if resp.User.Username != "alice" {
		t.Fatalf("Username = %q, want %q", resp.User.Username, "alice")
	}
	if resp.User.Email != "alice@example.com" {
		t.Fatalf("Email = %q, want %q", resp.User.Email, "alice@example.com")
	}
}

func TestHandleCloudAuthTokenSaveAndDelete(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	api := newCloudAuthAPIServer(t, "test-token", "alice")
	defer api.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = api.URL

	saveReq := httptest.NewRequest(http.MethodPost, "/api/cloud/auth/token", strings.NewReader(`{"token":" test-token "}`))
	w := httptest.NewRecorder()
	s.handleCloudAuthTokenSave(w, saveReq)

	if w.Code != http.StatusOK {
		t.Fatalf("save status = %d, want %d", w.Code, http.StatusOK)
	}
	if s.cfg.Token != "test-token" {
		t.Fatalf("saved token = %q, want %q", s.cfg.Token, "test-token")
	}

	var saveResp cloudAuthStatus
	if err := json.NewDecoder(w.Body).Decode(&saveResp); err != nil {
		t.Fatalf("decode save response: %v", err)
	}
	if !saveResp.Authenticated {
		t.Fatalf("Authenticated after save = false, want true")
	}
	if saveResp.User == nil || saveResp.User.Username != "alice" {
		t.Fatalf("saved user = %#v, want alice", saveResp.User)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/cloud/auth/token", nil)
	w = httptest.NewRecorder()
	s.handleCloudAuthTokenDelete(w, deleteReq)

	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d", w.Code, http.StatusOK)
	}
	if s.cfg.Token != "" {
		t.Fatalf("token after delete = %q, want empty", s.cfg.Token)
	}

	var deleteResp cloudAuthStatus
	if err := json.NewDecoder(w.Body).Decode(&deleteResp); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if deleteResp.HasToken {
		t.Fatalf("HasToken after delete = true, want false")
	}
	if deleteResp.Authenticated {
		t.Fatalf("Authenticated after delete = true, want false")
	}
}

func TestHandleCloudAPIKeySaveAndDelete(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	s := newTestServer(t)

	saveReq := httptest.NewRequest(http.MethodPost, "/api/cloud/api-key", strings.NewReader(`{"api_key":" manual-key "}`))
	w := httptest.NewRecorder()
	s.handleCloudAPIKeySave(w, saveReq)

	if w.Code != http.StatusOK {
		t.Fatalf("save status = %d, want %d", w.Code, http.StatusOK)
	}
	if s.cfg.OpenCSGAPIKey != "manual-key" {
		t.Fatalf("saved API key = %q, want manual-key", s.cfg.OpenCSGAPIKey)
	}

	var saveResp cloudAuthStatus
	if err := json.NewDecoder(w.Body).Decode(&saveResp); err != nil {
		t.Fatalf("decode save response: %v", err)
	}
	if !saveResp.HasAPIKey || saveResp.APIKeySource != "manual" {
		t.Fatalf("API key status = has:%v source:%q, want manual key", saveResp.HasAPIKey, saveResp.APIKeySource)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/cloud/api-key", nil)
	w = httptest.NewRecorder()
	s.handleCloudAPIKeyDelete(w, deleteReq)

	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d", w.Code, http.StatusOK)
	}
	if s.cfg.OpenCSGAPIKey != "" {
		t.Fatalf("API key after delete = %q, want empty", s.cfg.OpenCSGAPIKey)
	}
}

func TestNewCloudEngineUsesBuiltinAPIKey(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/token/access-token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"msg": "OK",
				"data": map[string]any{
					"token":     "access-token",
					"user_name": "alice",
					"user_uuid": "user-1",
				},
			})
		case "/api/v1/user/alice":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"msg": "OK",
				"data": map[string]any{
					"username": "alice",
					"uuid":     "user-1",
				},
			})
		case "/api/v1/namespaces/user-1/apikeys/builtin":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"msg": "OK",
				"data": map[string]any{
					"api_key": "builtin-key",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()

	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer builtin-key" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer builtin-key")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": "ok"},
			}},
		})
	}))
	defer modelServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = authServer.URL
	s.cfg.Token = "access-token"
	s.cloud = cloud.NewService(modelServer.URL)

	eng, err := s.newCloudEngine(context.Background(), "cloud/model")
	if err != nil {
		t.Fatalf("newCloudEngine error: %v", err)
	}
	got, err := eng.Generate(context.Background(), "hi", inference.DefaultOptions(), nil)
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("response = %q, want ok", got)
	}
}
