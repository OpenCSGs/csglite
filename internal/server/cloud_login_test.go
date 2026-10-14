package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
)

func TestResolveCommunityLoginURLRewritesCasdoorState(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "https://iam.example.test/login/oauth/authorize?client_id=abc&response_type=code&redirect_uri=https%3A%2F%2Fexample.test%2Fapi%2Fv1%2Fcallback%2Fcasdoor&scope=read&state=casdoor", http.StatusFound)
	}))
	defer upstream.Close()

	got, err := resolveCommunityLoginURL(t.Context(), upstream.URL)
	if err != nil {
		t.Fatalf("resolveCommunityLoginURL: %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse login URL: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "iam.example.test" {
		t.Fatalf("login URL = %s", got)
	}
	query := parsed.Query()
	if query.Get("state") != "lite" {
		t.Fatalf("state = %q, want lite", query.Get("state"))
	}
	if query.Get("client_id") != "abc" {
		t.Fatalf("client_id = %q", query.Get("client_id"))
	}
	if query.Get("redirect_uri") != "https://example.test/api/v1/callback/casdoor" {
		t.Fatalf("redirect_uri = %q", query.Get("redirect_uri"))
	}
}

func TestCommunityLoginURLUsesDefaultForMainSite(t *testing.T) {
	s := newTestServer(t)
	s.cfg.ServerURL = config.DefaultServerURL
	if got := s.communityLoginURL(t.Context()); got != cloud.DefaultLoginURL {
		t.Fatalf("login URL = %q, want default", got)
	}
}

func TestCommunityLoginURLCachesResolvedRedirect(t *testing.T) {
	resetCommunityLoginCache()
	t.Cleanup(resetCommunityLoginCache)

	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "https://iam.example.test/login/oauth/authorize?client_id=abc&state=casdoor", http.StatusFound)
	}))
	defer upstream.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = upstream.URL
	first := s.communityLoginURL(t.Context())
	second := s.communityLoginURL(t.Context())
	if first != second {
		t.Fatalf("cached login URL = %q, first %q", second, first)
	}
	if hits.Load() != 1 {
		t.Fatalf("login page hits = %d, want 1", hits.Load())
	}
	parsed, err := url.Parse(first)
	if err != nil || parsed.Query().Get("state") != "lite" {
		t.Fatalf("login URL = %s", first)
	}
}

func TestCommunityLoginURLFallsBackWhenLoginDoesNotRedirect(t *testing.T) {
	resetCommunityLoginCache()
	t.Cleanup(resetCommunityLoginCache)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = upstream.URL
	if got := s.communityLoginURL(t.Context()); got != upstream.URL+"/login" {
		t.Fatalf("login URL = %q, want fallback", got)
	}
}
