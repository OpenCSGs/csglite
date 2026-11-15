package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	clusterpublic "github.com/opencsgs/csglite/ee/cluster/public"
	"github.com/opencsgs/csglite/internal/config"
)

func TestManagementTokenCLIAddsListsAndRevokesIndependently(t *testing.T) {
	setupCLIConfigHome(t)
	run := func(args ...string) (string, error) {
		cmd := newClusterLoginTokenCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return strings.TrimSpace(out.String()), err
	}
	first, err := run("--name", "administrator A", "--ttl", "168h")
	if err != nil {
		t.Fatal(err)
	}
	second, err := run("--name", "administrator B")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "csgadmin_") || !strings.HasPrefix(second, "csgadmin_") || first == second || strings.ContainsAny(first, " \n") {
		t.Fatal("token generation changed its one-line output contract")
	}
	listed, err := run("list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var tokens []clusterpublic.AdminTokenInfo
	if err := json.Unmarshal([]byte(listed), &tokens); err != nil || len(tokens) != 2 {
		t.Fatal("invalid token listing", err)
	}
	if strings.Contains(listed, first) || strings.Contains(listed, second) || strings.Contains(listed, "token_hash") {
		t.Fatal("CLI list revealed secrets")
	}
	if tokens[0].Name != "administrator A" || tokens[0].ExpiresAt == nil || tokens[0].ExpiresAt.Sub(tokens[0].CreatedAt) != 168*time.Hour || tokens[1].ExpiresAt != nil {
		t.Fatal("token lifetime or name lost")
	}
	table, err := run("list")
	if err != nil || !strings.Contains(table, tokens[0].ID) || !strings.Contains(table, "active") || !strings.Contains(table, "never") {
		t.Fatal("unusable token listing", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	auth := clusterpublic.NewAdminAuth(clusterpublic.AdminTokenPath(cfg.StorageDir()))
	login := func(token string) int {
		r := httptest.NewRequest(http.MethodPost, "https://gateway.example/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
		r.Header.Set("Origin", "https://gateway.example")
		w := httptest.NewRecorder()
		auth.Login(w, r)
		return w.Code
	}
	if login(first) != 200 || login(second) != 200 {
		t.Fatal("both CLI credentials must work")
	}
	if _, err := run("revoke", tokens[0].ID); err != nil {
		t.Fatal(err)
	}
	if login(first) != 401 || login(second) != 200 {
		t.Fatal("CLI revocation affected the wrong token")
	}
	if _, err := run("revoke", tokens[0].ID); err == nil {
		t.Fatal("revoking an unknown token reported success")
	}
	if _, err := run("--ttl", "-1s"); err == nil {
		t.Fatal("negative token TTL accepted")
	}
	if login(second) != 200 {
		t.Fatal("failed CLI invocation invalidated existing token")
	}
}
