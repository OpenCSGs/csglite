// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

// SchedulingReason is stable protocol data for UI localization. Legacy English
// descriptions remain available for CLI and older API clients.
type SchedulingReason struct {
	Code string `json:"code"`
	Args []any  `json:"args,omitempty"`
}

func (r *Ranked) exclude(code, description string, args ...any) {
	r.Excluded = description
	r.ExcludedReason = &SchedulingReason{Code: code, Args: args}
}

func (r *Ranked) factor(code, description string, args ...any) {
	r.Factors = append(r.Factors, description)
	r.FactorDetails = append(r.FactorDetails, SchedulingReason{Code: code, Args: args})
}
