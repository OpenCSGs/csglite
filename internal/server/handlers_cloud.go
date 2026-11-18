package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/pkg/api"
)

type cloudAuthUser struct {
	Username string `json:"username"`
	Nickname string `json:"nickname,omitempty"`
	Email    string `json:"email,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	UUID     string `json:"uuid,omitempty"`
}

type cloudAuthStatus struct {
	AuthMode       string         `json:"auth_mode"`
	HasToken       bool           `json:"has_token"`
	Authenticated  bool           `json:"authenticated"`
	LoginURL       string         `json:"login_url"`
	AccessTokenURL string         `json:"access_token_url"`
	RechargeURL    string         `json:"recharge_url"`
	HasAPIKey      bool           `json:"has_api_key"`
	APIKeySource   string         `json:"api_key_source,omitempty"`
	APIKeyPrefix   string         `json:"api_key_prefix,omitempty"`
	APIKeyError    string         `json:"api_key_error,omitempty"`
	User           *cloudAuthUser `json:"user,omitempty"`
}

type cloudAuthTokenRequest struct {
	Token string `json:"token"`
}

type cloudAPIKeyRequest struct {
	APIKey string `json:"api_key"`
}

func (s *Server) handleCloudAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cloudAuthStatus(r.Context()))
}

func (s *Server) handleCloudAuthTokenSave(w http.ResponseWriter, r *http.Request) {
	var req cloudAuthTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	token := strings.TrimSpace(req.Token)
	if token == "" {
		writeError(w, http.StatusBadRequest, "token cannot be empty")
		return
	}

	if err := s.saveCloudAccessToken(token); err != nil {
		writeError(w, http.StatusInternalServerError, "saving token: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, s.cloudAuthStatus(r.Context()))
}

func (s *Server) saveCloudAccessToken(token string) error {
	s.cloudLoginMu.Lock()
	defer s.cloudLoginMu.Unlock()
	return s.saveCloudAccessTokenLocked(token)
}

func (s *Server) saveCloudAccessTokenLocked(token string) error {
	token = strings.TrimSpace(token)
	previousToken, previousAPIKey := s.cfg.Token, s.cfg.OpenCSGAPIKey
	previousInvalid, previousRevision := s.cloudTokenInvalid, s.cfg.AuthRevision
	s.cfg.Token = token
	// A new account session must not inherit an older manual gateway key; a
	// login in another process clears it on disk the same way.
	s.cfg.OpenCSGAPIKey = ""
	s.cloudTokenInvalid = false
	config.NextAuthRevision(s.cfg)
	if err := config.Save(s.cfg); err != nil {
		s.cfg.Token, s.cfg.OpenCSGAPIKey = previousToken, previousAPIKey
		s.cfg.AuthRevision = previousRevision
		s.cloudTokenInvalid = previousInvalid
		return err
	}
	s.cloudLoginRevision++
	clear(s.cloudLoginAttempts)
	s.refreshCloudCredentials()
	// A fresh login is the second write-off trigger: a license imported while
	// offline can now be reported. The server context lets shutdown cancel an
	// in-flight report, as it does for the startup scan.
	go s.reportLicenseActivation(s.engineLoadContext())
	return nil
}

// handleCloudAuthCallback is the browser redirect target for the "lite" SSO
// flow. The user service redirects here after a successful web login with the
// access token in the query string; we persist it and return a small page that
// asks the browser to close the tab so the waiting web UI can pick up the new
// login state. Browsers that refuse to close it keep the manual hint.
func (s *Server) handleCloudAuthCallback(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		writeCloudAuthCallbackPage(w, false, "missing_token")
		return
	}
	if err := s.saveCloudAccessToken(token); err != nil {
		log.Printf("cloud auth callback: saving token: %v", err)
		writeCloudAuthCallbackPage(w, false, "save_failed")
		return
	}
	writeCloudAuthCallbackPage(w, true, "")
}

func writeCloudAuthCallbackPage(w http.ResponseWriter, success bool, errCode string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	if success {
		_, _ = io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><title>Login complete</title></head><body style="font-family:system-ui;display:grid;place-items:center;height:100vh;margin:0"><p>Login complete. You can close this window and return to csglite.</p><script>history.replaceState(null,"",location.pathname);window.close();</script></body></html>`)
		return
	}
	_, _ = io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><title>Login failed</title></head><body style="font-family:system-ui;display:grid;place-items:center;height:100vh;margin:0"><p>Login failed (`+errCode+`). Please try again.</p><script>history.replaceState(null,"",location.pathname);</script></body></html>`)
}

func (s *Server) handleCloudAuthTokenDelete(w http.ResponseWriter, r *http.Request) {
	// Catch up with a login another instance saved, so the logout clears the
	// current session and remembers the token it actually cleared.
	s.adoptExternalCloudCredentials()
	s.cloudLoginMu.Lock()
	previousToken, previousAPIKey := s.cfg.Token, s.cfg.OpenCSGAPIKey
	previousRevision := s.cfg.AuthRevision
	s.cfg.Token = ""
	s.cfg.OpenCSGAPIKey = ""
	config.NextAuthRevision(s.cfg)
	if err := config.Save(s.cfg); err != nil {
		s.cfg.Token, s.cfg.OpenCSGAPIKey = previousToken, previousAPIKey
		s.cfg.AuthRevision = previousRevision
		writeError(w, http.StatusInternalServerError, "clearing token: "+err.Error())
		s.cloudLoginMu.Unlock()
		return
	}
	s.cloudLoginRevision++
	clear(s.cloudLoginAttempts)
	s.cloudTokenInvalid = false
	// A repeated logout must not forget the token an earlier one cleared: a
	// build without auth_revision could otherwise write it back.
	if previousToken != "" {
		s.cloudClearedToken = previousToken
	}
	s.cloudLoginMu.Unlock()
	s.refreshCloudCredentials()

	writeJSON(w, http.StatusOK, s.cloudAuthStatus(r.Context()))
}

func (s *Server) handleCloudAPIKeySave(w http.ResponseWriter, r *http.Request) {
	var req cloudAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		writeError(w, http.StatusBadRequest, "api key cannot be empty")
		return
	}

	// The key belongs to the account session, so it moves the login generation
	// and other instances pick it up like a login. Another instance may have
	// logged in since this page was rendered: apply that first, so the key is
	// saved on top of the current session instead of writing this process's
	// older token back with a newer generation.
	s.adoptExternalCloudCredentials()
	s.cloudLoginMu.Lock()
	previousKey, previousRevision := s.cfg.OpenCSGAPIKey, s.cfg.AuthRevision
	s.cfg.OpenCSGAPIKey = apiKey
	config.NextAuthRevision(s.cfg)
	if err := config.Save(s.cfg); err != nil {
		s.cfg.OpenCSGAPIKey, s.cfg.AuthRevision = previousKey, previousRevision
		s.cloudLoginMu.Unlock()
		writeError(w, http.StatusInternalServerError, "saving API key: "+err.Error())
		return
	}
	s.cloudLoginMu.Unlock()
	s.refreshCloudCredentials()

	writeJSON(w, http.StatusOK, s.cloudAuthStatus(r.Context()))
}

func (s *Server) handleCloudAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	// Catch up with a login another instance saved before changing the session.
	s.adoptExternalCloudCredentials()
	s.cloudLoginMu.Lock()
	previousKey, previousRevision := s.cfg.OpenCSGAPIKey, s.cfg.AuthRevision
	s.cfg.OpenCSGAPIKey = ""
	config.NextAuthRevision(s.cfg)
	if err := config.Save(s.cfg); err != nil {
		s.cfg.OpenCSGAPIKey, s.cfg.AuthRevision = previousKey, previousRevision
		s.cloudLoginMu.Unlock()
		writeError(w, http.StatusInternalServerError, "clearing API key: "+err.Error())
		return
	}
	s.cloudLoginMu.Unlock()
	s.refreshCloudCredentials()

	writeJSON(w, http.StatusOK, s.cloudAuthStatus(r.Context()))
}

// Inference resolves the current account's key per request. Invalidate the
// related model, marketplace and license caches together after credentials change.
func (s *Server) refreshCloudCredentials() {
	if s.cloud != nil {
		s.cloud.SetAccessToken(s.cfg.Token)
	}
	clearMarketplaceCache()
	s.invalidateLicenseCloudCache()
	s.cloudRefreshMu.Lock()
	s.cloudRefreshAt = time.Time{}
	s.cloudRefreshMu.Unlock()
}

// adoptExternalCloudCredentials picks up a login, logout or gateway key change
// another csglite process saved to config.json. The loopback SSO callback port
// is fixed, so when two instances run only the one holding the port receives
// the browser redirect; without this the other instance keeps its old session
// until a restart. The saved login generation decides: a newer one always wins,
// so a stale process writing its whole config back cannot resurrect a session
// that was signed out elsewhere. A generation-0 config comes from a build
// without the field and cannot say when it was written, so it is only adopted
// when this process has no usable login.
func (s *Server) adoptExternalCloudCredentials() {
	s.adoptExternalCloudCredentialsFrom(config.CloudCredentialsOnDisk)
}

// adoptExternalCloudCredentialsFrom takes the credential source as a parameter
// so a test can hold the read open and prove a login completed in this process
// is not overwritten by a snapshot taken before it.
func (s *Server) adoptExternalCloudCredentialsFrom(read func() (config.CloudCredentials, error)) {
	if s == nil || s.cfg == nil {
		return
	}
	// A missing or partially written config is nothing to adopt, never a
	// reason to clear credentials held in memory.
	creds, err := read()
	if err != nil {
		return
	}
	s.cloudLoginMu.Lock()
	// Never hand this instance's community credentials minted for another one.
	if !credentialsMatchCommunity(creds.ServerURL, s.cfg.ServerURL) {
		s.cloudLoginMu.Unlock()
		return
	}
	localToken := strings.TrimSpace(s.cfg.Token)
	// A token value alone cannot tell a fresh login from a stale process
	// writing the previous one back, because logins reuse tokens. The saved
	// generation does, and a newer one always wins: it can only have been
	// written after this process's last auth change.
	newer := creds.AuthRevision > s.cfg.AuthRevision
	if !newer {
		// Generation 0 is a build without auth_revision (mixed versions) or a
		// config from before it existed. It cannot say when it was written, so
		// it only applies to an instance with no usable login, and never for
		// the token this process cleared itself.
		if creds.AuthRevision != 0 || creds.Token == "" {
			s.cloudLoginMu.Unlock()
			return
		}
		if !(localToken == "" || s.cloudTokenInvalid) {
			s.cloudLoginMu.Unlock()
			return
		}
		if creds.Token == localToken || creds.Token == s.cloudClearedToken {
			s.cloudLoginMu.Unlock()
			return
		}
	}
	tokenChanged := creds.Token != localToken
	keyChanged := creds.OpenCSGAPIKey != s.cfg.OpenCSGAPIKey
	if !tokenChanged && !keyChanged && !newer {
		s.cloudLoginMu.Unlock()
		return
	}
	s.cfg.Token = creds.Token
	s.cfg.OpenCSGAPIKey = creds.OpenCSGAPIKey
	// The generation is monotonic in this process: adopting an older build's
	// write must not roll it back.
	if newer {
		s.cfg.AuthRevision = creds.AuthRevision
	}
	cancelled := 0
	if tokenChanged {
		// The rejection was about the previous token; a different one starts
		// unvalidated. A key-only change leaves that state alone.
		s.cloudTokenInvalid = false
		if creds.Token == "" {
			s.cloudClearedToken = localToken
		}
		s.cloudLoginRevision++
		cancelled = len(s.cloudLoginAttempts)
		clear(s.cloudLoginAttempts)
	}
	s.cloudLoginMu.Unlock()

	origin := "another csglite process"
	if creds.AuthRevision == 0 && !newer {
		origin = "an instance without auth_revision"
	}
	switch {
	case tokenChanged && creds.Token == "":
		log.Printf("cloud auth: adopted sign-out written by %s", origin)
	case tokenChanged:
		log.Printf("cloud auth: adopted access token written by %s", origin)
	default:
		log.Printf("cloud auth: adopted credential change written by %s", origin)
	}
	if cancelled > 0 {
		log.Printf("cloud auth: cancelled %d pending browser login(s); the other instance already signed in", cancelled)
	}
	s.refreshCloudCredentials()
	if tokenChanged && creds.Token != "" {
		// A license imported offline on this instance can now be written off,
		// the same trigger a login in this process provides.
		go s.reportLicenseActivation(s.engineLoadContext())
	}
}

// credentialsMatchCommunity reports whether a token written for diskURL may be
// used against localURL. A config from before server_url was tracked carries no
// community, so only the default one is safe to assume.
func credentialsMatchCommunity(diskURL, localURL string) bool {
	if strings.TrimSpace(diskURL) == "" {
		return sameCommunityURL(localURL, config.DefaultServerURL)
	}
	return sameCommunityURL(diskURL, localURL)
}

// adoptExternalCredentialsInterval bounds how long a headless instance waits
// before noticing a login another process saved. It is far below the five
// minute login wait, and each tick costs one small config read.
var adoptExternalCredentialsInterval = 15 * time.Second

// adoptExternalCredentialsLoop picks up logins saved by another csglite process
// for instances that never poll the status endpoint, such as a headless server
// used only through the OpenAI-compatible API.
func (s *Server) adoptExternalCredentialsLoop(ctx context.Context) {
	ticker := time.NewTicker(adoptExternalCredentialsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.adoptExternalCloudCredentials()
		}
	}
}

// sameCommunityURL compares base community URLs. Scheme and host are matched
// case-insensitively, a default port counts as absent and a trailing slash is
// ignored, so a URL edited by hand still matches the one another process wrote.
func sameCommunityURL(a, b string) bool {
	ua, errA := url.Parse(strings.TrimSpace(a))
	ub, errB := url.Parse(strings.TrimSpace(b))
	if errA != nil || errB != nil || ua.Host == "" || ub.Host == "" {
		return strings.TrimRight(strings.TrimSpace(a), "/") == strings.TrimRight(strings.TrimSpace(b), "/")
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) &&
		strings.EqualFold(ua.Hostname(), ub.Hostname()) &&
		communityPort(ua) == communityPort(ub) &&
		strings.TrimRight(ua.EscapedPath(), "/") == strings.TrimRight(ub.EscapedPath(), "/")
}

// communityPort returns the effective port, so "https://host" and
// "https://host:443" compare equal.
func communityPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}

// recordCloudTokenValidation records a definitive authentication answer from
// the platform for token; a nil error means the token was accepted. The token
// endpoint answers 404 for an unknown or expired token, so 401, 403 and 404 all
// count as rejected. Transport and server errors are ignored: a flaky network
// must not make a working session look unusable, nor an unusable one usable.
// The answer is only recorded while token is still the session in use, so a
// slow check cannot label a newer login.
func (s *Server) recordCloudTokenValidation(token string, err error) {
	rejected := false
	if err != nil {
		httpErr, ok := csghub.AsHTTPError(err)
		if !ok || (httpErr.StatusCode != http.StatusUnauthorized &&
			httpErr.StatusCode != http.StatusForbidden &&
			httpErr.StatusCode != http.StatusNotFound) {
			return
		}
		rejected = true
	}
	token = strings.TrimSpace(token)
	s.cloudLoginMu.Lock()
	if strings.TrimSpace(s.cfg.Token) != token {
		s.cloudLoginMu.Unlock()
		return
	}
	changed := s.cloudTokenInvalid != rejected
	s.cloudTokenInvalid = rejected
	s.cloudLoginMu.Unlock()
	if changed {
		if rejected {
			log.Printf("cloud auth: the platform rejected the saved access token; waiting for a fresh login")
		} else {
			log.Printf("cloud auth: the saved access token is accepted again")
		}
	}
}

func (s *Server) cloudAuthStatus(ctx context.Context) cloudAuthStatus {
	s.adoptExternalCloudCredentials()

	displayURL := strings.TrimRight(s.cfg.DisplayURL(), "/")
	loginURL := s.communityLoginURL(ctx)

	status := cloudAuthStatus{
		AuthMode:       "token",
		LoginURL:       loginURL,
		AccessTokenURL: displayURL + "/settings/access-token",
		RechargeURL:    displayURL + "/settings/recharge-payment",
	}
	if apiKey := strings.TrimSpace(s.cfg.OpenCSGAPIKey); apiKey != "" {
		status.HasAPIKey = true
		status.APIKeySource = "manual"
		status.APIKeyPrefix = apiKeyDisplayPrefix(apiKey)
	}
	token := strings.TrimSpace(s.cfg.Token)
	status.HasToken = token != ""
	if !status.HasToken {
		return status
	}

	client := csghub.NewClient(s.cfg.ServerURL, token)
	user, err := client.GetCurrentUser(ctx)
	if err != nil || user == nil {
		if err != nil {
			s.recordCloudTokenValidation(token, err)
		}
		return status
	}
	s.recordCloudTokenValidation(token, nil)

	status.Authenticated = true
	status.User = &cloudAuthUser{
		Username: strings.TrimSpace(user.Username),
		Nickname: strings.TrimSpace(user.Nickname),
		Email:    strings.TrimSpace(user.Email),
		Avatar:   strings.TrimSpace(user.Avatar),
		UUID:     strings.TrimSpace(user.UUID),
	}
	if !status.HasAPIKey {
		apiKey, err := s.builtinCloudAPIKey(ctx, client, user)
		if err != nil {
			status.APIKeyError = err.Error()
		} else {
			status.HasAPIKey = true
			status.APIKeySource = "builtin"
			status.APIKeyPrefix = apiKeyDisplayPrefix(apiKey)
		}
	}
	return status
}

func (s *Server) getChatEngine(ctx context.Context, modelID, source string, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string) (inference.Engine, error) {
	var err error
	source, err = effectiveRequestSource(ctx, source)
	if err != nil {
		return nil, inference.NewHTTPStatusError(http.StatusBadRequest, err.Error())
	}
	route, clusterSource, err := s.routeForSource(modelID, source, true)
	if err != nil {
		return nil, err
	}
	routedCluster := func(src string) (inference.Engine, error) {
		return s.clusterRoutedEngine(ctx, cluster.EngineChat, modelID, src, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype)
	}
	switch route {
	case routeProviderPool:
		pool, ok := providerPoolForRequest(modelID, source)
		if !ok {
			return nil, inference.NewHTTPStatusError(http.StatusNotFound, "provider pool not found or disabled")
		}
		return s.newProviderPoolChatEngine(ctx, pool, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype)
	case routeThirdPartyProvider:
		return newThirdPartyProviderEngine(source, modelID)
	case routeCloud:
		return s.newCloudEngine(ctx, modelID)
	case routeCluster:
		return routedCluster(clusterSource)
	}
	normalizedSource := strings.ToLower(source)

	eng, err := s.getOrLoadEngineWithOpts(modelID, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype)
	if err == nil {
		return eng, nil
	}
	if normalizedSource == "local" {
		return nil, err
	}
	// The local load failed: a peer that holds the model is the closest
	// substitute, ahead of third-party providers and the cloud.
	if s.clusterIsTheClosestSubstitute(modelID, source) {
		return routedCluster(cluster.SourceCluster)
	}

	if !s.hasCloudCredential() {
		if providerSource := s.thirdPartyProviderSourceForModel(ctx, modelID); providerSource != "" {
			return newThirdPartyProviderEngine(providerSource, modelID)
		}
	}

	models, cloudErr := s.listCloudModels(ctx, false)
	if cloudErr != nil {
		if providerSource := s.thirdPartyProviderSourceForModel(ctx, modelID); providerSource != "" {
			return newThirdPartyProviderEngine(providerSource, modelID)
		}
		return nil, err
	}
	if modelInfoListContains(models, modelID) {
		return s.newCloudEngine(ctx, modelID)
	}

	if s.cloud == nil {
		return nil, err
	}
	models, cloudErr = s.refreshCloudModelsAfterMiss(ctx)
	if cloudErr != nil {
		if providerSource := s.thirdPartyProviderSourceForModel(ctx, modelID); providerSource != "" {
			return newThirdPartyProviderEngine(providerSource, modelID)
		}
		return nil, err
	}
	if modelInfoListContains(models, modelID) {
		return s.newCloudEngine(ctx, modelID)
	}

	if providerSource := s.thirdPartyProviderSourceForModel(ctx, modelID); providerSource != "" {
		return newThirdPartyProviderEngine(providerSource, modelID)
	}
	if pool, ok := providerPoolForRequest(modelID, source); ok {
		return s.newProviderPoolChatEngine(ctx, pool, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype)
	}

	return nil, err
}

func (s *Server) getEmbeddingEngine(ctx context.Context, modelID, source string, numCtx, nGPULayers int, dtype string) (inference.Engine, error) {
	var err error
	source, err = effectiveRequestSource(ctx, source)
	if err != nil {
		return nil, inference.NewHTTPStatusError(http.StatusBadRequest, err.Error())
	}
	route, clusterSource, err := s.routeForSource(modelID, source, true)
	if err != nil {
		return nil, err
	}
	routedCluster := func(src string) (inference.Engine, error) {
		return s.clusterRoutedEngine(ctx, cluster.EngineEmbedding, modelID, src, numCtx, 0, nGPULayers, "", "", dtype)
	}
	switch route {
	case routeProviderPool:
		pool, ok := providerPoolForRequest(modelID, source)
		if !ok {
			return nil, inference.NewHTTPStatusError(http.StatusNotFound, "provider pool not found or disabled")
		}
		return s.newProviderPoolEmbeddingEngine(ctx, pool, numCtx, nGPULayers, dtype)
	case routeThirdPartyProvider:
		return newThirdPartyProviderEngine(source, modelID)
	case routeCloud:
		return s.newCloudEngine(ctx, modelID)
	case routeCluster:
		return routedCluster(clusterSource)
	}
	normalizedSource := strings.ToLower(source)

	// An embeddings request carries no slot count: the concurrency a loaded
	// engine serves is set when it is loaded, so 0 keeps whatever is running.
	eng, err := s.getOrLoadEmbeddingEngineWithOpts(ctx, modelID, numCtx, 0, nGPULayers, dtype)
	if err == nil {
		return eng, nil
	}
	if normalizedSource == "local" {
		return nil, err
	}
	if s.clusterIsTheClosestSubstitute(modelID, source) {
		return routedCluster(cluster.SourceCluster)
	}

	if providerSource := s.thirdPartyProviderSourceForModel(ctx, modelID); providerSource != "" {
		return newThirdPartyProviderEngine(providerSource, modelID)
	}
	if pool, ok := providerPoolForRequest(modelID, source); ok {
		return s.newProviderPoolEmbeddingEngine(ctx, pool, numCtx, nGPULayers, dtype)
	}
	return nil, err
}

func (s *Server) thirdPartyProviderSourceForModel(ctx context.Context, modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return ""
	}
	for _, item := range s.listSelectedThirdPartyProviderModels(ctx) {
		if strings.TrimSpace(item.Model) == modelID {
			return strings.TrimSpace(item.Source)
		}
	}
	return ""
}

func (s *Server) newCloudEngine(ctx context.Context, modelID string) (inference.Engine, error) {
	apiKey, err := s.cloudAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	baseURL := resolveCloudURL(s.cfg)
	if s.cloud != nil && strings.TrimSpace(s.cloud.BaseURL()) != "" {
		baseURL = s.cloud.BaseURL()
	}
	modelID = s.resolveCloudOriginalModelID(modelID)
	return inference.NewOpenAIEngine(baseURL, modelID, apiKey), nil
}

func (s *Server) hasCloudCredential() bool {
	if s == nil || s.cfg == nil {
		return false
	}
	return s.cfg.IsCloudProviderEnabled() && (strings.TrimSpace(s.cfg.OpenCSGAPIKey) != "" || strings.TrimSpace(s.cfg.Token) != "")
}

func (s *Server) cloudAPIKey(ctx context.Context) (string, error) {
	if s != nil && !s.cfg.IsCloudProviderEnabled() {
		return "", inference.NewHTTPStatusError(http.StatusForbidden, "cloud provider is disabled")
	}
	if s == nil || s.cfg == nil {
		return "", inference.NewHTTPStatusError(http.StatusUnauthorized, "Cloud login required. Please sign in to OpenCSG or save an API Key.")
	}
	if apiKey := strings.TrimSpace(s.cfg.OpenCSGAPIKey); apiKey != "" {
		return apiKey, nil
	}
	token := strings.TrimSpace(s.cfg.Token)
	if token == "" {
		return "", inference.NewHTTPStatusError(http.StatusUnauthorized, "Cloud login required. Please sign in to OpenCSG or save an API Key.")
	}
	client := csghub.NewClient(s.cfg.ServerURL, token)
	user, err := client.GetCurrentUser(ctx)
	if err != nil || user == nil {
		if err != nil {
			// An instance used only through this API never polls the status
			// endpoint; record the rejection so a token written by another
			// process can replace the dead one.
			s.recordCloudTokenValidation(token, err)
		}
		return "", inference.NewHTTPStatusError(http.StatusUnauthorized, "OpenCSG login expired or token is invalid. Please log in again.")
	}
	apiKey, err := s.builtinCloudAPIKey(ctx, client, user)
	if err != nil {
		return "", inference.NewHTTPStatusError(http.StatusUnauthorized, "Failed to load OpenCSG built-in API Key. Please sign in again or save an API Key.")
	}
	return apiKey, nil
}

func (s *Server) builtinCloudAPIKey(ctx context.Context, client *csghub.Client, user *csghub.User) (string, error) {
	namespace := strings.TrimSpace(user.UUID)
	if namespace == "" {
		namespace = strings.TrimSpace(user.Username)
	}
	if namespace == "" {
		return "", inference.NewHTTPStatusError(http.StatusUnauthorized, "OpenCSG user namespace is unavailable.")
	}
	apiKey, err := client.GetBuiltinAPIKey(ctx, namespace)
	if err != nil {
		return "", err
	}
	return apiKey, nil
}

func apiKeyDisplayPrefix(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	if len(apiKey) <= 10 {
		return apiKey
	}
	return apiKey[:6] + "..." + apiKey[len(apiKey)-4:]
}

func modelInfoListContains(models []api.ModelInfo, modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return false
	}
	for _, item := range models {
		if strings.TrimSpace(item.Model) == modelID {
			return true
		}
	}
	return false
}

func (s *Server) cloudModelListContainsMatching(ctx context.Context, modelID string, matches func(api.ModelInfo) bool) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return false
	}
	contains := func(models []api.ModelInfo) bool {
		for _, item := range models {
			if strings.TrimSpace(item.Model) == modelID && matches(item) {
				return true
			}
		}
		return false
	}

	models, err := s.listCloudModels(ctx, false)
	if err == nil && contains(models) {
		return true
	}
	if s == nil || s.cloud == nil {
		return false
	}
	models, err = s.refreshCloudModelsAfterMiss(ctx)
	return err == nil && contains(models)
}

func cloudPipelineTagMatcher(pipelineTags ...string) func(api.ModelInfo) bool {
	allowed := make(map[string]struct{}, len(pipelineTags))
	for _, tag := range pipelineTags {
		tag = strings.TrimSpace(strings.ToLower(tag))
		if tag != "" {
			allowed[tag] = struct{}{}
		}
	}
	return func(item api.ModelInfo) bool {
		_, ok := allowed[strings.TrimSpace(strings.ToLower(item.PipelineTag))]
		return ok
	}
}
