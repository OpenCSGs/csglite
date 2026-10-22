// Package keygov enforces what an API key may do: which models it calls,
// until when, how fast, and how much it spends. Key limits are free, as in
// LiteLLM's open-source proxy; budgets for users and teams are in ee/budget.
package keygov

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/opencsgs/csglite/pkg/api"
)

// Budget periods. An empty period never restarts.
const (
	PeriodDay   = "day"
	PeriodMonth = "month"
)

// WarnFraction is the share of a cap at which a budget warns.
const WarnFraction = 0.8

const maxModels = 200

var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// NormalizeBudget trims a budget and checks it. A budget that caps nothing
// comes back nil.
func NormalizeBudget(b *api.Budget) (*api.Budget, error) {
	if b == nil {
		return nil, nil
	}
	out := *b
	out.Currency = strings.ToUpper(strings.TrimSpace(out.Currency))
	out.Period = strings.ToLower(strings.TrimSpace(out.Period))
	if out.Tokens < 0 {
		return nil, errors.New("token budget must not be negative")
	}
	if out.Cost < 0 || math.IsNaN(out.Cost) || math.IsInf(out.Cost, 0) {
		return nil, errors.New("cost budget must be a positive number")
	}
	switch out.Period {
	case "", PeriodDay, PeriodMonth:
	default:
		return nil, fmt.Errorf("budget period must be %q, %q or empty", PeriodDay, PeriodMonth)
	}
	if out.Cost > 0 && !currencyPattern.MatchString(out.Currency) {
		return nil, errors.New("a cost budget needs a three-letter currency such as CNY or USD")
	}
	if out.Cost == 0 {
		out.Currency = ""
	}
	if out.Tokens == 0 && out.Cost == 0 {
		return nil, nil
	}
	return &out, nil
}

// NormalizeLimits trims key limits and checks them. Limits that restrict
// nothing come back nil.
func NormalizeLimits(l *api.KeyLimits) (*api.KeyLimits, error) {
	if l == nil {
		return nil, nil
	}
	out := api.KeyLimits{RPM: l.RPM, TPM: l.TPM, ExpiresAt: l.ExpiresAt}
	if out.RPM < 0 || out.TPM < 0 {
		return nil, errors.New("rate limits must not be negative")
	}
	seen := map[string]bool{}
	for _, model := range l.Models {
		model = strings.TrimSpace(model)
		if model == "" || seen[strings.ToLower(model)] {
			continue
		}
		if model != "*" && strings.Contains(strings.TrimSuffix(model, "*"), "*") {
			return nil, fmt.Errorf("model %q may only end in *", model)
		}
		seen[strings.ToLower(model)] = true
		out.Models = append(out.Models, model)
	}
	if len(out.Models) > maxModels {
		return nil, fmt.Errorf("a key may list at most %d models", maxModels)
	}
	if out.ExpiresAt != nil {
		if out.ExpiresAt.IsZero() {
			out.ExpiresAt = nil
		} else {
			at := out.ExpiresAt.UTC()
			out.ExpiresAt = &at
		}
	}
	budget, err := NormalizeBudget(l.Budget)
	if err != nil {
		return nil, err
	}
	out.Budget = budget
	if len(out.Models) == 0 && out.ExpiresAt == nil && out.RPM == 0 && out.TPM == 0 && out.Budget == nil {
		return nil, nil
	}
	return &out, nil
}

// AllowsModel reports whether limits let a key call model. Names compare
// without case; an entry ending in "*" matches by prefix.
func AllowsModel(l *api.KeyLimits, model string) bool {
	if l == nil || len(l.Models) == 0 {
		return true
	}
	model = strings.ToLower(strings.TrimSpace(model))
	for _, entry := range l.Models {
		entry = strings.ToLower(entry)
		if prefix, ok := strings.CutSuffix(entry, "*"); ok {
			if strings.HasPrefix(model, prefix) {
				return true
			}
			continue
		}
		if entry == model {
			return true
		}
	}
	return false
}

// Expired reports whether limits have run out at now.
func Expired(l *api.KeyLimits, now time.Time) bool {
	return l != nil && l.ExpiresAt != nil && !now.Before(*l.ExpiresAt)
}

// PeriodStart is when the budget period containing now began, in now's
// location. It is the zero time for a budget that never restarts.
func PeriodStart(period string, now time.Time) time.Time {
	switch period {
	case PeriodDay:
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case PeriodMonth:
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	default:
		return time.Time{}
	}
}

// Exhausted reports whether spend has reached a cap of b.
func Exhausted(b *api.Budget, spend Spend) bool {
	if b == nil {
		return false
	}
	return (b.Tokens > 0 && spend.Tokens >= b.Tokens) || (b.Cost > 0 && spend.Cost >= b.Cost)
}

// Warning reports whether spend has reached WarnFraction of a cap of b.
func Warning(b *api.Budget, spend Spend) bool {
	if b == nil {
		return false
	}
	return (b.Tokens > 0 && float64(spend.Tokens) >= WarnFraction*float64(b.Tokens)) ||
		(b.Cost > 0 && spend.Cost >= WarnFraction*b.Cost)
}

// Scope is one budget a request counts against: a key's, or with an
// Enterprise license a user's or a team's.
type Scope struct {
	// Kind is "key", "user" or "team".
	Kind    string
	Subject string
	Budget  *api.Budget
}

// Status is where one scope stands in its current period.
type Status struct {
	Scope     Scope
	Spend     Spend
	Since     time.Time
	Exhausted bool
	Warning   bool
}

// Check reads each scope's spend in its current period.
func Check(ledger *Ledger, scopes []Scope, now time.Time) ([]Status, error) {
	out := make([]Status, 0, len(scopes))
	for _, scope := range scopes {
		spend, since, err := ledger.Current(scope.Subject, scope.Budget, now)
		if err != nil {
			return nil, err
		}
		out = append(out, Status{
			Scope:     scope,
			Spend:     spend,
			Since:     since,
			Exhausted: Exhausted(scope.Budget, spend),
			Warning:   Warning(scope.Budget, spend),
		})
	}
	return out, nil
}
