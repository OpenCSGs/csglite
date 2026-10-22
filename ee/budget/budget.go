// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

// Package budget holds the Enterprise part of spend control: budgets for a
// user and for a team, counted across every key and the web UI, and the
// warning at 80% of any budget. Key limits themselves (models, expiry, rate
// limits, a key's own budget) are free and live in internal/keygov.
package budget

import (
	"fmt"
	"math"
	"strings"

	"github.com/opencsgs/csglite/internal/keygov"
	accounts "github.com/opencsgs/csglite/internal/team"
	"github.com/opencsgs/csglite/pkg/api"
)

// OwnerScopes lists the budgets of user and of team, which may be nil, that
// are set.
func OwnerScopes(user accounts.User, team *accounts.Team) []keygov.Scope {
	var scopes []keygov.Scope
	if user.ID != "" && user.Budget != nil {
		scopes = append(scopes, keygov.Scope{Kind: "user", Subject: keygov.UserSubject(user.ID), Budget: user.Budget})
	}
	if team != nil && team.Budget != nil {
		scopes = append(scopes, keygov.Scope{Kind: "team", Subject: keygov.TeamSubject(team.ID), Budget: team.Budget})
	}
	return scopes
}

// WarningHeader is the value of the X-CSGLite-Budget-Warning response header
// for the statuses past 80%, such as "key=85%, team=92%", or empty.
func WarningHeader(statuses []keygov.Status) string {
	var parts []string
	for _, status := range statuses {
		if !status.Warning {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%d%%", status.Scope.Kind, int(math.Floor(100*Used(status.Scope.Budget, status.Spend)))))
	}
	return strings.Join(parts, ", ")
}

// Used is the largest share of a cap that spend has reached.
func Used(budget *api.Budget, spend keygov.Spend) float64 {
	if budget == nil {
		return 0
	}
	used := 0.0
	if budget.Tokens > 0 {
		used = math.Max(used, float64(spend.Tokens)/float64(budget.Tokens))
	}
	if budget.Cost > 0 {
		used = math.Max(used, spend.Cost/budget.Cost)
	}
	return used
}
