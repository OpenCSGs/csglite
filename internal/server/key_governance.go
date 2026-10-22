package server

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	eebudget "github.com/opencsgs/csglite/ee/budget"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/httpjson"
	"github.com/opencsgs/csglite/internal/keygov"
	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/internal/team"
	"github.com/opencsgs/csglite/pkg/api"
)

// Codes inference callers and the web UI switch on.
const (
	apiKeyExpiredCode   = "api_key_expired"
	modelNotAllowedCode = "model_not_allowed"
	rateLimitedCode     = "rate_limited"
	budgetExceededCode  = "budget_exceeded"
)

// budgetWarningHeader reports budgets past 80% on a response, with an
// Enterprise license.
const budgetWarningHeader = "X-CSGLite-Budget-Warning"

// Key statuses in API responses.
const (
	apiKeyStatusActive    = "active"
	apiKeyStatusExpired   = "expired"
	apiKeyStatusExhausted = "exhausted"
)

// keyGovernanceMiddleware applies a key's limits, and the budgets of the user
// and team behind a request, to inference calls. Requests without a key or a
// signed-in user, such as the local operator's, are not limited.
func (s *Server) keyGovernanceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !identifiesAPIKeyForUsage(r) {
			next.ServeHTTP(w, r)
			return
		}
		now := time.Now()
		key, hasKey := authenticatedAPIKey(r)
		var limits *api.KeyLimits
		if hasKey {
			limits = key.Limits
		}
		if keygov.Expired(limits, now) {
			writeGovernanceError(w, r, http.StatusUnauthorized, apiKeyExpiredCode, "this API key has expired")
			return
		}
		if limits != nil && len(limits.Models) > 0 {
			model := peekRequestModel(r)
			if model != "" && !keygov.AllowsModel(limits, model) {
				writeGovernanceError(w, r, http.StatusForbidden, modelNotAllowedCode, "this API key may not call model "+model)
				return
			}
		}

		statuses, err := keygov.Check(s.spend, s.requestBudgetScopes(r), now)
		if err != nil {
			writeGovernanceError(w, r, http.StatusInternalServerError, "server_error", "failed to read budgets")
			return
		}
		for _, status := range statuses {
			if status.Exhausted {
				writeGovernanceError(w, r, http.StatusTooManyRequests, budgetExceededCode, "the "+status.Scope.Kind+" budget is used up")
				return
			}
		}
		if limits != nil {
			if ok, wait := s.rateLimits.Allow(keygov.KeySubject(key.ID), limits.RPM, limits.TPM, now); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
				writeGovernanceError(w, r, http.StatusTooManyRequests, rateLimitedCode, "this API key is over its rate limit")
				return
			}
		}
		if s.license.Enabled(license.FeatureBudgets) {
			if warning := eebudget.WarningHeader(statuses); warning != "" {
				w.Header().Set(budgetWarningHeader, warning)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requestBudgetScopes lists the budgets a request counts against: its key's,
// and its user's and its user's team's, which only a license sets.
func (s *Server) requestBudgetScopes(r *http.Request) []keygov.Scope {
	var scopes []keygov.Scope
	if key, ok := authenticatedAPIKey(r); ok && key.Limits != nil && key.Limits.Budget != nil {
		scopes = append(scopes, keygov.Scope{Kind: "key", Subject: keygov.KeySubject(key.ID), Budget: key.Limits.Budget})
	}
	if id, ok := identityFromRequest(r); ok && !id.Local && id.User.ID != "" {
		scopes = append(scopes, eebudget.OwnerScopes(id.User, s.teamByID(id.User.TeamID))...)
	}
	return scopes
}

func (s *Server) teamByID(id string) *team.Team {
	if id == "" || s.team == nil {
		return nil
	}
	teams, err := s.team.Teams()
	if err != nil {
		return nil
	}
	for i := range teams {
		if teams[i].ID == id {
			return &teams[i]
		}
	}
	return nil
}

// recordSpend counts a finished request against its key, user and team, and
// against the key's rate limit window.
func (s *Server) recordSpend(r *http.Request, tokens int64, cost float64, currency string) {
	now := time.Now()
	var subjects []string
	if key, ok := authenticatedAPIKey(r); ok {
		subjects = append(subjects, keygov.KeySubject(key.ID))
		if s.rateLimits != nil {
			s.rateLimits.AddTokens(keygov.KeySubject(key.ID), tokens, now)
		}
	}
	if id, ok := identityFromRequest(r); ok && !id.Local && id.User.ID != "" {
		subjects = append(subjects, keygov.UserSubject(id.User.ID))
		if id.User.TeamID != "" {
			subjects = append(subjects, keygov.TeamSubject(id.User.TeamID))
		}
	}
	_ = s.spend.Record(subjects, now, tokens, cost, currency)
}

// budgetSpend reports where subject stands against budget, or nil without a
// budget. Warnings are an Enterprise feature.
func (s *Server) budgetSpend(subject string, budget *api.Budget) *api.BudgetSpend {
	if budget == nil {
		return nil
	}
	spend, since, err := s.spend.Current(subject, budget, time.Now())
	if err != nil {
		return nil
	}
	out := &api.BudgetSpend{
		Tokens:    spend.Tokens,
		Cost:      spend.Cost,
		Exhausted: keygov.Exhausted(budget, spend),
		Warning:   keygov.Warning(budget, spend) && s.license.Enabled(license.FeatureBudgets),
	}
	if !since.IsZero() {
		out.Since = &since
	}
	return out
}

func (s *Server) apiKeyInfo(record config.APIKeyRecord, owners map[string]string) api.APIKeyInfo {
	var lastUsedAt *time.Time
	if !record.LastUsedAt.IsZero() {
		lastUsedAt = &record.LastUsedAt
	}
	info := api.APIKeyInfo{
		ID:         record.ID,
		Name:       record.Name,
		Prefix:     record.Prefix,
		CreatedAt:  record.CreatedAt,
		LastUsedAt: lastUsedAt,
		OwnerID:    record.OwnerID,
		OwnerName:  owners[record.OwnerID],
		Limits:     record.Limits,
		Status:     apiKeyStatusActive,
	}
	if record.Limits != nil {
		info.Spend = s.budgetSpend(keygov.KeySubject(record.ID), record.Limits.Budget)
	}
	switch {
	case keygov.Expired(record.Limits, time.Now()):
		info.Status = apiKeyStatusExpired
	case info.Spend != nil && info.Spend.Exhausted:
		info.Status = apiKeyStatusExhausted
	}
	return info
}

// filterModelsForKey drops the models a key's allowlist leaves out of a
// model list. /v1/models is not metered, so the key is looked up here
// without marking it used.
func (s *Server) filterModelsForKey(r *http.Request, models []api.ModelInfo) []api.ModelInfo {
	key, ok := authenticatedAPIKey(r)
	if !ok && s.apiKeys != nil {
		key, ok, _ = s.apiKeys.Find(requestAPIKey(r))
	}
	if !ok || key.Limits == nil || len(key.Limits.Models) == 0 {
		return models
	}
	out := models[:0:0]
	for _, model := range models {
		if keygov.AllowsModel(key.Limits, model.Model) {
			out = append(out, model)
		}
	}
	return out
}

// peekRequestModel reads the model a request names without consuming its
// body: a JSON body is read and put back, and a multipart form is read only
// up to its model field.
func peekRequestModel(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType == "multipart/form-data" && params["boundary"] != "" {
		var seen bytes.Buffer
		reader := multipart.NewReader(io.TeeReader(r.Body, &seen), params["boundary"])
		model := ""
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			if part.FormName() == "model" {
				value, _ := io.ReadAll(io.LimitReader(part, 1024))
				model = strings.TrimSpace(string(value))
				break
			}
		}
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(&seen, r.Body), r.Body}
		return model
	}
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	var probe struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &probe)
	return strings.TrimSpace(probe.Model)
}

// writeGovernanceError answers in the shape the caller's protocol expects.
func writeGovernanceError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	path := providerRouteLegacyPath(r.URL.Path)
	switch {
	case strings.HasPrefix(path, "/anthropic/") || strings.HasPrefix(path, "/v1/messages"):
		writeAnthropicErrorWithType(w, status, code, msg)
	case strings.HasPrefix(path, "/v1/"):
		writeOpenAIError(w, status, code, msg)
	default:
		httpjson.WriteCodedError(w, status, msg, code)
	}
}
