package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
)

func TestPublicClusterAlwaysAuthenticatesAndHasNoManagement(t *testing.T) {
	s := &Server{apiKeys: config.NewAPIKeyStore(t.TempDir())}
	_, key, err := s.apiKeys.Create("public-test")
	if err != nil {
		t.Fatal(err)
	}
	h := s.publicClusterInferenceRoutes()
	for _, tc := range []struct {
		path, key string
		status    int
	}{
		{"/v1/models", "", 401}, {"/v1/models", "invalid", 401}, {"/v1/models", key, 200},
		{"/api/settings", key, 404}, {"/api/cluster/token", key, 404}, {"/api/license", key, 404},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer "+tc.key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%s => %d, want %d", tc.path, w.Code, tc.status)
		}
	}
	for _, source := range []string{"cloud", "provider:test", "pool:test"} {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test","source":"`+source+`"}`))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Errorf("source %s => %d", source, w.Code)
		}
	}
}

func TestPublicManagementListenerCannotBindHostInterfaces(t *testing.T) {
	for _, mode := range []string{"gateway", "worker"} {
		for _, addr := range []string{":11435", "0.0.0.0:11435", "192.0.2.1:11435", "[::]:11435"} {
			if validatePublicManagementListener(mode, addr) == nil {
				t.Errorf("%s accepted %s", mode, addr)
			}
		}
		for _, addr := range []string{"127.0.0.1:11435", "localhost:11435", "[::1]:11435"} {
			if err := validatePublicManagementListener(mode, addr); err != nil {
				t.Errorf("%s rejected %s: %v", mode, addr, err)
			}
		}
	}
	if err := validatePublicManagementListener("", ":11435"); err != nil {
		t.Fatal("changed existing LAN listener behavior")
	}
}
