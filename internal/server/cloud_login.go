package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
)

const communityLoginState = "lite"

// communityLoginClient does not follow redirects. The portal /login response
// is a Casdoor authorize URL whose state must be rewritten before the browser
// opens it.
var communityLoginClient = &http.Client{
	Timeout: 8 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type cachedCommunityLogin struct {
	url     string
	failed  bool
	expires time.Time
}

var (
	communityLoginMu    sync.Mutex
	communityLoginCache = map[string]cachedCommunityLogin{}
)

func resetCommunityLoginCache() {
	communityLoginMu.Lock()
	communityLoginCache = map[string]cachedCommunityLogin{}
	communityLoginMu.Unlock()
}

// communityLoginURL is the browser URL that signs the user in and returns an
// access token to this process. The main site uses the built-in Casdoor URL.
// Every other CSGHub server publishes the same authorize URL from /login, but
// with state=casdoor, which only logs into that website. Rewriting the state
// to lite makes the user service redirect the access token to the local
// callback instead.
func (s *Server) communityLoginURL(ctx context.Context) string {
	serverURL := strings.TrimRight(strings.TrimSpace(s.cfg.ServerURL), "/")
	if serverURL == "" || serverURL == config.DefaultServerURL {
		return cloud.DefaultLoginURL
	}
	if cached, ok := lookupCommunityLogin(serverURL); ok {
		if cached != "" {
			return cached
		}
		return serverURL + "/login"
	}

	resolveCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	resolved, err := resolveCommunityLoginURL(resolveCtx, serverURL)
	if err != nil {
		log.Printf("cloud login: %v", err)
		storeCommunityLogin(serverURL, cachedCommunityLogin{failed: true, expires: time.Now().Add(30 * time.Second)})
		return serverURL + "/login"
	}
	storeCommunityLogin(serverURL, cachedCommunityLogin{url: resolved})
	return resolved
}

func lookupCommunityLogin(serverURL string) (string, bool) {
	communityLoginMu.Lock()
	defer communityLoginMu.Unlock()
	cached, ok := communityLoginCache[serverURL]
	if !ok {
		return "", false
	}
	if cached.failed && time.Now().After(cached.expires) {
		delete(communityLoginCache, serverURL)
		return "", false
	}
	if cached.failed {
		return "", true
	}
	return cached.url, true
}

func storeCommunityLogin(serverURL string, entry cachedCommunityLogin) {
	communityLoginMu.Lock()
	communityLoginCache[serverURL] = entry
	communityLoginMu.Unlock()
}

func resolveCommunityLoginURL(ctx context.Context, serverURL string) (string, error) {
	loginPage := strings.TrimRight(strings.TrimSpace(serverURL), "/") + "/login"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loginPage, nil)
	if err != nil {
		return "", fmt.Errorf("build login request for %s: %w", serverURL, err)
	}
	req.Header.Set("Accept", "text/html")
	resp, err := communityLoginClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch login redirect for %s: %w", serverURL, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("login page for %s returned %d, want a redirect", serverURL, resp.StatusCode)
	}
	location := strings.TrimSpace(resp.Header.Get("Location"))
	if location == "" {
		return "", fmt.Errorf("login page for %s redirected without a location", serverURL)
	}
	base, err := url.Parse(loginPage)
	if err != nil {
		return "", fmt.Errorf("parse login page %s: %w", loginPage, err)
	}
	ref, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("parse login redirect for %s: %w", serverURL, err)
	}
	resolved := base.ResolveReference(ref)
	if (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Host == "" {
		return "", fmt.Errorf("login redirect for %s is not an http URL", serverURL)
	}
	query := resolved.Query()
	query.Set("state", communityLoginState)
	resolved.RawQuery = query.Encode()
	return resolved.String(), nil
}
