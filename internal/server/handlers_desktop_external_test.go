package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateDesktopExternalURL(t *testing.T) {
	if got, err := validateDesktopExternalURL(" https://opencsg.com/docs "); err != nil || got != "https://opencsg.com/docs" {
		t.Fatalf("valid URL = %q, %v", got, err)
	}
	for _, raw := range []string{
		"http://opencsg.com/docs",
		"file:///tmp/test",
		"https://user:password@opencsg.com/docs",
		"not a URL",
	} {
		if _, err := validateDesktopExternalURL(raw); err == nil {
			t.Fatalf("unsafe external URL accepted: %q", raw)
		}
	}
}

func TestDesktopOpenExternalRequiresDesktopMode(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/system/open-external", strings.NewReader(`{"url":"https://opencsg.com/docs"}`))
	w := httptest.NewRecorder()
	s.handleDesktopOpenExternal(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestDesktopOpenExternalRejectsUnsafeURL(t *testing.T) {
	s := newTestServer(t)
	s.cfg.DesktopMode = true
	req := httptest.NewRequest(http.MethodPost, "/api/system/open-external", strings.NewReader(`{"url":"file:///tmp/test"}`))
	w := httptest.NewRecorder()
	s.handleDesktopOpenExternal(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
