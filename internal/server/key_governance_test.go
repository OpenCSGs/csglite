package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/keygov"
	"github.com/opencsgs/csglite/internal/team"
	"github.com/opencsgs/csglite/pkg/api"
)

// expectOpenAIError checks the error type an OpenAI-compatible route returns.
func expectOpenAIError(t *testing.T, rec *httptest.ResponseRecorder, status int, errType, what string) {
	t.Helper()
	expectStatus(t, rec, status, what)
	var body struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Type != errType {
		t.Fatalf("%s: error type %q, want %q: %s", what, body.Error.Type, errType, rec.Body.String())
	}
}

func chatRequest(model string) api.OpenAIChatRequest {
	return api.OpenAIChatRequest{Model: model, Messages: []api.Message{{Role: "user", Content: "hi"}}}
}

// newKeyClient enables API key authentication, has the local operator
// create a key with limits, and returns a remote client using it.
func newKeyClient(t *testing.T, s *Server, limits *api.KeyLimits) (*teamClient, api.APIKeyInfo) {
	t.Helper()
	local := newTeamClient(t, s)
	local.remote = "127.0.0.1:50000"
	expectStatus(t, local.do(http.MethodPost, "/api/api-keys/settings", api.APIKeySettingsUpdateRequest{AuthEnabled: true}), http.StatusOK, "enable API keys")
	rec := local.do(http.MethodPost, "/api/api-keys", api.APIKeyCreateRequest{Name: "client", Limits: limits})
	expectStatus(t, rec, http.StatusCreated, "create a key with limits")
	var created api.APIKeyCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	client := newTeamClient(t, s)
	client.apiKey = created.APIKey
	return client, created.Key
}

func TestKeyLimitsWorkWithoutALicense(t *testing.T) {
	s := newTestServer(t)
	client, key := newKeyClient(t, s, &api.KeyLimits{Models: []string{"qwen/*"}, RPM: 1})
	if key.Limits == nil || key.Limits.RPM != 1 || key.Status != apiKeyStatusActive {
		t.Fatalf("created key = %+v", key)
	}

	expectOpenAIError(t, client.do(http.MethodPost, "/v1/chat/completions", chatRequest("gpt-4o")),
		http.StatusForbidden, modelNotAllowedCode, "a model outside the allowlist")
	rec := client.do(http.MethodPost, "/v1/chat/completions", chatRequest("qwen/Qwen3-8B"))
	if rec.Code == http.StatusForbidden || rec.Code == http.StatusTooManyRequests {
		t.Fatalf("an allowed model was refused: %d %s", rec.Code, rec.Body.String())
	}
	rec = client.do(http.MethodPost, "/v1/chat/completions", chatRequest("qwen/Qwen3-8B"))
	expectOpenAIError(t, rec, http.StatusTooManyRequests, rateLimitedCode, "a second request within the minute")
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("a rate-limited response must say when to retry")
	}
}

func TestAnExpiredKeyIsRefused(t *testing.T) {
	s := newTestServer(t)
	past := time.Now().Add(-time.Hour)
	client, key := newKeyClient(t, s, &api.KeyLimits{ExpiresAt: &past})
	if key.Status != apiKeyStatusExpired {
		t.Fatalf("status = %q, want expired", key.Status)
	}
	expectCode(t, client.do(http.MethodPost, "/api/chat", chatRequest("any")), http.StatusUnauthorized, apiKeyExpiredCode, "an expired key")
}

func TestAKeyStopsAtItsBudget(t *testing.T) {
	s := newTestServer(t)
	client, key := newKeyClient(t, s, &api.KeyLimits{Budget: &api.Budget{Tokens: 100, Period: keygov.PeriodDay}})
	if err := s.spend.Record([]string{keygov.KeySubject(key.ID)}, time.Now(), 100, 0, ""); err != nil {
		t.Fatal(err)
	}
	rec := client.do(http.MethodPost, "/anthropic/v1/messages", api.AnthropicMessageRequest{Model: "any", MaxTokens: 10})
	expectStatus(t, rec, http.StatusTooManyRequests, "a key past its budget")
	if !bytes.Contains(rec.Body.Bytes(), []byte(budgetExceededCode)) {
		t.Fatalf("body = %s, want the %s type", rec.Body.String(), budgetExceededCode)
	}

	local := newTeamClient(t, s)
	local.remote = "127.0.0.1:50000"
	var list api.APIKeysResponse
	_ = json.Unmarshal(local.do(http.MethodGet, "/api/api-keys", nil).Body.Bytes(), &list)
	got := list.Keys[0]
	if got.Status != apiKeyStatusExhausted || got.Spend == nil || got.Spend.Tokens != 100 || got.Spend.Since == nil {
		t.Fatalf("listed key = %+v", got)
	}
	if got.Spend.Warning {
		t.Fatal("budget warnings are Enterprise")
	}
}

func TestOnlyAnAdministratorChangesKeyLimits(t *testing.T) {
	s, admin := newAccountsServer(t)
	_, member := inviteUser(t, s, admin, "bob", "member")
	rec := member.do(http.MethodPost, "/api/api-keys", api.APIKeyCreateRequest{Name: "mine", Limits: &api.KeyLimits{RPM: 5}})
	expectStatus(t, rec, http.StatusCreated, "a member restricts their own new key")
	var created api.APIKeyCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	path := "/api/api-keys/" + created.Key.ID

	expectCode(t, member.do(http.MethodPut, path, api.APIKeyUpdateRequest{Limits: &api.KeyLimits{}}),
		http.StatusForbidden, permissionDeniedCode, "a member lifting their own limits")
	expectStatus(t, member.do(http.MethodPut, path, api.APIKeyUpdateRequest{Name: ptr("renamed")}), http.StatusOK, "a member renaming their key")

	rec = admin.do(http.MethodPut, path, api.APIKeyUpdateRequest{Limits: &api.KeyLimits{TPM: 1000}})
	expectStatus(t, rec, http.StatusOK, "an administrator changing limits")
	var updated api.APIKeyInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.Name != "renamed" || updated.Limits == nil || updated.Limits.TPM != 1000 || updated.Limits.RPM != 0 {
		t.Fatalf("updated key = %+v", updated)
	}
	expectStatus(t, admin.do(http.MethodPut, path, api.APIKeyUpdateRequest{Limits: &api.KeyLimits{RPM: -1}}), http.StatusBadRequest, "a negative limit")
}

func TestUserAndTeamBudgetsNeedALicense(t *testing.T) {
	s := newTestServer(t)
	local := newTeamClient(t, s)
	local.remote = "127.0.0.1:50000"
	admin := createTeamUser(t, local, api.TeamUserCreateRequest{Username: "admin", Password: "admin-password"})
	expectCode(t, local.do(http.MethodPut, "/api/team/users/"+admin.ID+"/budget", api.BudgetRequest{Budget: &api.Budget{Tokens: 10}}),
		http.StatusForbidden, licenseErrorCode, "a user budget without a license")
}

func TestUserAndTeamBudgetsWarnAndStop(t *testing.T) {
	s, admin := newAccountsServer(t)
	rec := admin.do(http.MethodPost, "/api/team/teams", api.TeamGroupRequest{Name: ptr("research")})
	var group api.TeamGroup
	_ = json.Unmarshal(rec.Body.Bytes(), &group)
	bob, member := inviteUser(t, s, admin, "bob", "member")
	expectStatus(t, admin.do(http.MethodPut, "/api/team/users/"+bob.ID, api.TeamUserUpdateRequest{TeamID: &group.ID}), http.StatusOK, "join the team")

	rec = admin.do(http.MethodPut, "/api/team/users/"+bob.ID+"/budget", api.BudgetRequest{Budget: &api.Budget{Tokens: 1000, Period: "month"}})
	expectStatus(t, rec, http.StatusOK, "set a user budget")
	expectStatus(t, admin.do(http.MethodPut, "/api/team/teams/"+group.ID+"/budget", api.BudgetRequest{Budget: &api.Budget{Cost: 5, Currency: "cny"}}),
		http.StatusOK, "set a team budget")
	expectStatus(t, admin.do(http.MethodPut, "/api/team/teams/"+group.ID+"/budget", api.BudgetRequest{Budget: &api.Budget{Cost: 5}}),
		http.StatusBadRequest, "a cost budget without a currency")

	if err := s.spend.Record([]string{keygov.UserSubject(bob.ID)}, time.Now(), 850, 0, ""); err != nil {
		t.Fatal(err)
	}
	rec = member.do(http.MethodPost, "/v1/chat/completions", chatRequest("any"))
	if rec.Code == http.StatusTooManyRequests {
		t.Fatalf("refused at 85%%: %s", rec.Body.String())
	}
	if got := rec.Header().Get(budgetWarningHeader); got != "user=85%" {
		t.Fatalf("warning header = %q", got)
	}
	var users api.TeamUsersResponse
	_ = json.Unmarshal(admin.do(http.MethodGet, "/api/team/users", nil).Body.Bytes(), &users)
	for _, user := range users.Users {
		if user.ID == bob.ID && (user.Spend == nil || !user.Spend.Warning || user.Spend.Exhausted) {
			t.Fatalf("listed user = %+v", user)
		}
	}

	if err := s.spend.Record([]string{keygov.TeamSubject(group.ID)}, time.Now(), 0, 5, "CNY"); err != nil {
		t.Fatal(err)
	}
	expectOpenAIError(t, member.do(http.MethodPost, "/v1/chat/completions", chatRequest("any")),
		http.StatusTooManyRequests, budgetExceededCode, "a member of a team past its budget")

	expectStatus(t, admin.do(http.MethodPut, "/api/team/teams/"+group.ID+"/budget", api.BudgetRequest{}), http.StatusOK, "remove the team budget")
	if rec := member.do(http.MethodPost, "/v1/chat/completions", chatRequest("any")); rec.Code == http.StatusTooManyRequests {
		t.Fatalf("still refused after the budget was removed: %s", rec.Body.String())
	}
}

func TestSpendIsCountedAgainstKeyUserAndTeam(t *testing.T) {
	s := newTestServer(t)
	key := config.APIKeyRecord{ID: "k1", Limits: &api.KeyLimits{TPM: 10}}
	ctx := context.WithValue(context.Background(), apiKeyContextKey{}, key)
	ctx = context.WithValue(ctx, requestIdentityKey{}, requestIdentity{User: team.User{ID: "u1", TeamID: "t1"}, APIKey: &key})
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)

	s.rateLimits.Allow(keygov.KeySubject("k1"), 0, 10, time.Now())
	s.recordSpend(r, 42, 0.5, "USD")
	for _, subject := range []string{keygov.KeySubject("k1"), keygov.UserSubject("u1"), keygov.TeamSubject("t1")} {
		spend, err := s.spend.Spend(subject, time.Time{}, "USD")
		if err != nil || spend.Tokens != 42 || spend.Cost != 0.5 {
			t.Fatalf("%s spend = %+v, %v", subject, spend, err)
		}
	}
	if ok, _ := s.rateLimits.Allow(keygov.KeySubject("k1"), 0, 10, time.Now()); ok {
		t.Fatal("the tokens should count against the key's TPM")
	}
}

func TestModelListFollowsTheKeyAllowlist(t *testing.T) {
	s := newTestServer(t)
	key := config.APIKeyRecord{ID: "k1", Limits: &api.KeyLimits{Models: []string{"qwen/*"}}}
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r = r.WithContext(context.WithValue(r.Context(), apiKeyContextKey{}, key))
	got := s.filterModelsForKey(r, []api.ModelInfo{{Model: "Qwen/Qwen3-8B"}, {Model: "gpt-4o"}})
	if len(got) != 1 || got[0].Model != "Qwen/Qwen3-8B" {
		t.Fatalf("models = %+v", got)
	}
}

func TestPeekRequestModelLeavesTheBodyIntact(t *testing.T) {
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	file, _ := writer.CreateFormFile("file", "a.wav")
	_, _ = file.Write(bytes.Repeat([]byte("x"), 10_000))
	_ = writer.WriteField("model", "whisper-large")
	_ = writer.Close()
	original := form.Bytes()
	r := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(append([]byte(nil), original...)))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	if got := peekRequestModel(r); got != "whisper-large" {
		t.Fatalf("multipart model = %q", got)
	}
	if body, _ := io.ReadAll(r.Body); !bytes.Equal(body, original) {
		t.Fatal("the multipart body changed")
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{"model":" gpt-4o ","messages":[]}`)))
	r.Header.Set("Content-Type", "application/json")
	if got := peekRequestModel(r); got != "gpt-4o" {
		t.Fatalf("JSON model = %q", got)
	}
	if body, _ := io.ReadAll(r.Body); string(body) != `{"model":" gpt-4o ","messages":[]}` {
		t.Fatalf("the JSON body changed: %s", body)
	}
}
