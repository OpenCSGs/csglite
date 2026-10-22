package keygov

import (
	"testing"
	"time"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestNormalizeLimits(t *testing.T) {
	got, err := NormalizeLimits(&api.KeyLimits{Models: []string{" qwen/* ", "Qwen/*", "", "gpt-4o"}, RPM: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 || got.Models[0] != "qwen/*" || got.Models[1] != "gpt-4o" {
		t.Fatalf("models = %v", got.Models)
	}
	if got, err := NormalizeLimits(&api.KeyLimits{Budget: &api.Budget{}}); err != nil || got != nil {
		t.Fatalf("empty limits = %v, %v; want nil", got, err)
	}
	for _, bad := range []api.KeyLimits{
		{RPM: -1},
		{Models: []string{"a*b"}},
		{Budget: &api.Budget{Cost: 5}},
		{Budget: &api.Budget{Tokens: 1, Period: "week"}},
	} {
		if _, err := NormalizeLimits(&bad); err == nil {
			t.Errorf("NormalizeLimits(%+v) accepted", bad)
		}
	}
	got, err = NormalizeLimits(&api.KeyLimits{Budget: &api.Budget{Cost: 5, Currency: "cny", Period: "Month"}})
	if err != nil || got.Budget.Currency != "CNY" || got.Budget.Period != PeriodMonth {
		t.Fatalf("budget = %+v, %v", got, err)
	}
}

func TestAllowsModel(t *testing.T) {
	limits := &api.KeyLimits{Models: []string{"Qwen/*", "gpt-4o"}}
	for model, want := range map[string]bool{
		"qwen/Qwen3-8B": true,
		"GPT-4o":        true,
		"gpt-4o-mini":   false,
		"deepseek-r1":   false,
	} {
		if got := AllowsModel(limits, model); got != want {
			t.Errorf("AllowsModel(%q) = %v, want %v", model, got, want)
		}
	}
	if !AllowsModel(nil, "anything") || !AllowsModel(&api.KeyLimits{Models: []string{"*"}}, "x") {
		t.Fatal("no allowlist, or *, must allow every model")
	}
}

func TestPeriodStartAndBudgets(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 4, 5, 0, time.Local)
	if got := PeriodStart(PeriodDay, now); !got.Equal(time.Date(2026, 10, 22, 0, 0, 0, 0, time.Local)) {
		t.Errorf("day start = %v", got)
	}
	if got := PeriodStart(PeriodMonth, now); !got.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)) {
		t.Errorf("month start = %v", got)
	}
	if !PeriodStart("", now).IsZero() {
		t.Error("a budget without a period never restarts")
	}
	budget := &api.Budget{Tokens: 100, Cost: 10, Currency: "CNY"}
	if Exhausted(budget, Spend{Tokens: 99, Cost: 9.99}) || !Exhausted(budget, Spend{Tokens: 100}) || !Exhausted(budget, Spend{Cost: 10}) {
		t.Error("Exhausted is wrong at the caps")
	}
	if Warning(budget, Spend{Tokens: 79}) || !Warning(budget, Spend{Tokens: 80}) || !Warning(budget, Spend{Cost: 8}) {
		t.Error("Warning is wrong at 80%")
	}
}

func TestLedger(t *testing.T) {
	ledger := NewLedger(t.TempDir())
	defer ledger.Close()
	today := time.Now()
	yesterday := today.AddDate(0, 0, -1)
	if err := ledger.Record([]string{KeySubject("k"), UserSubject("u")}, yesterday, 100, 1, "cny"); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Record([]string{KeySubject("k")}, today, 50, 2, "USD"); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Record([]string{KeySubject("k")}, today, 7, 0, ""); err != nil {
		t.Fatal(err)
	}
	all, err := ledger.Spend(KeySubject("k"), time.Time{}, "CNY")
	if err != nil || all.Tokens != 157 || all.Cost != 1 {
		t.Fatalf("lifetime spend = %+v, %v", all, err)
	}
	day, err := ledger.Spend(KeySubject("k"), PeriodStart(PeriodDay, today), "USD")
	if err != nil || day.Tokens != 57 || day.Cost != 2 {
		t.Fatalf("today's spend = %+v, %v", day, err)
	}
	user, err := ledger.Spend(UserSubject("u"), time.Time{}, "CNY")
	if err != nil || user.Tokens != 100 {
		t.Fatalf("user spend = %+v, %v", user, err)
	}
}

func TestLimiter(t *testing.T) {
	limiter := NewLimiter()
	now := time.Now()
	for i := 0; i < 2; i++ {
		if ok, _ := limiter.Allow("k", 2, 0, now); !ok {
			t.Fatalf("request %d refused", i)
		}
	}
	ok, wait := limiter.Allow("k", 2, 0, now.Add(10*time.Second))
	if ok || wait != 50*time.Second {
		t.Fatalf("third request = %v, wait %v; want refused for 50s", ok, wait)
	}
	if ok, _ := limiter.Allow("k", 2, 0, now.Add(61*time.Second)); !ok {
		t.Fatal("the window should have moved on")
	}

	if ok, _ := limiter.Allow("t", 0, 100, now); !ok {
		t.Fatal("first request refused")
	}
	limiter.AddTokens("t", 120, now)
	if ok, _ := limiter.Allow("t", 0, 100, now.Add(time.Second)); ok {
		t.Fatal("tokens over the TPM cap should refuse")
	}
	if ok, _ := limiter.Allow("t", 0, 100, now.Add(61*time.Second)); !ok {
		t.Fatal("tokens should leave the window after a minute")
	}
	limiter.AddTokens("untracked", 10, now)
	if len(limiter.windows) != 2 {
		t.Fatal("a subject without a rate limit must not be tracked")
	}
}
