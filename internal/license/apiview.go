package license

import "github.com/opencsgs/csglite/pkg/api"

// This file is the single place that maps license state onto the public API
// types, shared by the HTTP handlers and the CLI so the two cannot drift.

// Summary converts the signed payload into its API form, or nil.
func Summary(p *RSAPayload) *api.LicenseSummary {
	if p == nil {
		return nil
	}
	return &api.LicenseSummary{
		Key:        p.Key,
		Company:    p.Company,
		Email:      p.Email,
		Product:    p.Product,
		Edition:    p.Edition,
		MaxUser:    p.MaxUser,
		StartTime:  p.StartTime,
		ExpireTime: p.ExpireTime,
		Version:    p.Version,
	}
}

// LimitsCopy returns the effective limits as a fresh, never-nil map.
func (s State) LimitsCopy() map[string]int {
	out := make(map[string]int, len(s.Limits))
	for k, v := range s.Limits {
		out[k] = v
	}
	return out
}

// APIState is the GET /api/license representation. filePath is where the
// license file lives, or empty.
func (s State) APIState(filePath string) api.LicenseState {
	resp := api.LicenseState{
		Status:    string(s.Status),
		Edition:   s.Edition(),
		License:   Summary(s.Payload),
		Features:  s.EnabledKeys(),
		Limits:    s.LimitsCopy(),
		Reason:    s.Reason,
		Warnings:  s.Warnings,
		Source:    s.Source,
		FilePath:  filePath,
		CheckedAt: s.CheckedAt,
	}
	if !s.GraceUntil.IsZero() {
		grace := s.GraceUntil
		resp.GraceUntil = &grace
	}
	return resp
}

// APIVerify is the POST /api/license/verify representation.
func (s State) APIVerify() api.LicenseVerifyResponse {
	return api.LicenseVerifyResponse{
		Valid:    s.Licensed(),
		Status:   string(s.Status),
		License:  Summary(s.Payload),
		Features: s.EnabledKeys(),
		Limits:   s.LimitsCopy(),
		Reason:   s.Reason,
		Warnings: s.Warnings,
	}
}

// APICatalog lists defs with whether each is in effect under s.
func (s State) APICatalog(defs []FeatureDefinition) []api.LicenseFeatureDefinition {
	out := make([]api.LicenseFeatureDefinition, 0, len(defs))
	for _, def := range defs {
		entry := api.LicenseFeatureDefinition{
			Key:          def.Key,
			Type:         string(def.Type),
			Gated:        def.Gated,
			DefaultValue: def.DefaultValue,
			NavItem:      def.NavItem,
			Since:        def.Since,
		}
		switch def.Type {
		case FeatureTypeBoolean:
			entry.Enabled = s.Enabled(def)
		case FeatureTypeInt:
			entry.Enabled = !def.Gated || s.Licensed()
		}
		out = append(out, entry)
	}
	return out
}
