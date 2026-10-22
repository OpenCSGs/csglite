// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package budget

import (
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/keygov"
	accounts "github.com/opencsgs/csglite/internal/team"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestCheckUserAndTeamBudgets(t *testing.T) {
	ledger := keygov.NewLedger(t.TempDir())
	defer ledger.Close()
	now := time.Now()
	user := accounts.User{ID: "u1", Budget: &api.Budget{Tokens: 1000, Period: keygov.PeriodDay}}
	team := &accounts.Team{ID: "t1", Budget: &api.Budget{Cost: 10, Currency: "CNY", Period: keygov.PeriodMonth}}
	scopes := OwnerScopes(user, team)
	if len(scopes) != 2 {
		t.Fatalf("scopes = %+v", scopes)
	}
	if err := ledger.Record([]string{keygov.UserSubject("u1"), keygov.TeamSubject("t1")}, now, 850, 10, "CNY"); err != nil {
		t.Fatal(err)
	}
	statuses, err := keygov.Check(ledger, scopes, now)
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].Exhausted || !statuses[0].Warning {
		t.Errorf("user at 85%% = %+v; want a warning only", statuses[0])
	}
	if !statuses[1].Exhausted {
		t.Errorf("team at 100%% = %+v; want exhausted", statuses[1])
	}
	if got := WarningHeader(statuses); got != "user=85%, team=100%" {
		t.Errorf("header = %q", got)
	}
}

func TestOwnerScopesSkipsUnsetBudgets(t *testing.T) {
	if scopes := OwnerScopes(accounts.User{ID: "u1"}, &accounts.Team{ID: "t1"}); len(scopes) != 0 {
		t.Fatalf("scopes = %+v; want none", scopes)
	}
	if scopes := OwnerScopes(accounts.User{Budget: &api.Budget{Tokens: 1}}, nil); len(scopes) != 0 {
		t.Fatal("a key that predates accounts has no user to budget")
	}
}
