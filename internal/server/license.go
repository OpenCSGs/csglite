package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/pkg/api"
)

// licenseImportMaxBytes bounds the PUT/POST bodies; a real license is a few KB.
const licenseImportMaxBytes = 64 << 10

// licenseErrorCode is the machine-readable code the UI switches on when a
// gated route is refused.
const licenseErrorCode = "feature_not_licensed"

// licenseErrorResponse extends the standard error shape so callers learn which
// feature and edition the route needs.
type licenseErrorResponse struct {
	Error           string `json:"error"`
	ErrorCode       int    `json:"errorCode"`
	Code            string `json:"code"`
	Feature         string `json:"feature"`
	EditionRequired string `json:"edition_required"`
}

func newLicenseManager(storageRoot, version string) *license.Manager {
	opts, err := license.DefaultOptions(storageRoot, version)
	if err != nil {
		// A broken public-key override must not take the server down; the
		// manager runs with no keys and reports every license as invalid.
		log.Printf("license: %v; enterprise features stay disabled", err)
		opts = license.Options{Version: version}
	}
	m := license.NewManager(opts)
	state := m.Refresh()
	switch state.Status {
	case license.StatusNone:
	case license.StatusValid:
		log.Printf("license: %s edition for %s (expires %s)", state.Edition(), state.Payload.Company, state.Payload.ExpireTime.UTC().Format("2006-01-02"))
	default:
		log.Printf("license: status %s: %s", state.Status, state.Reason)
	}
	return m
}

// requireFeature refuses the wrapped handler with 403 unless the boolean
// feature is in effect. A definition that is not Gated always passes, so
// wrapping a route is harmless until the catalog entry is flipped to EE.
// Wrap routes in routes.go only; background jobs check s.license.Enabled.
func (s *Server) requireFeature(def license.FeatureDefinition) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.license.Enabled(def) {
				next(w, r)
				return
			}
			writeLicenseRequired(w, def)
		}
	}
}

func writeLicenseRequired(w http.ResponseWriter, def license.FeatureDefinition) {
	writeJSON(w, http.StatusForbidden, licenseErrorResponse{
		Error:           "This feature requires a CSGLite " + license.EditionEnterprise + " license.",
		ErrorCode:       http.StatusForbidden,
		Code:            licenseErrorCode,
		Feature:         def.Key,
		EditionRequired: license.EditionEnterprise,
	})
}

// settingsResponse is currentSettingsResponse plus the license fields.
func (s *Server) settingsResponse() api.SettingsResponse {
	resp := currentSettingsResponse(s.cfg, s.version)
	state := s.license.State()
	resp.Edition = state.Edition()
	resp.LicenseStatus = string(state.Status)
	resp.License = licenseSummary(state)
	resp.Features = state.EnabledKeys()
	resp.Limits = licenseLimits(state)
	resp.FeatureCatalog = licenseFeatureCatalog(state)
	return resp
}

func licenseSummary(state license.State) *api.LicenseSummary {
	p := state.Payload
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

func licenseLimits(state license.State) map[string]int {
	out := make(map[string]int, len(state.Limits))
	for k, v := range state.Limits {
		out[k] = v
	}
	return out
}

func licenseFeatureCatalog(state license.State) []api.LicenseFeatureDefinition {
	defs := license.Catalog()
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
		case license.FeatureTypeBoolean:
			entry.Enabled = state.Enabled(def)
		case license.FeatureTypeInt:
			entry.Enabled = !def.Gated || state.Licensed()
		}
		out = append(out, entry)
	}
	return out
}

func licenseStateResponse(m *license.Manager, state license.State) api.LicenseState {
	resp := api.LicenseState{
		Status:    string(state.Status),
		Edition:   state.Edition(),
		License:   licenseSummary(state),
		Features:  state.EnabledKeys(),
		Limits:    licenseLimits(state),
		Reason:    state.Reason,
		Warnings:  state.Warnings,
		Source:    state.Source,
		FilePath:  m.FilePath(),
		CheckedAt: state.CheckedAt,
	}
	if !state.GraceUntil.IsZero() {
		grace := state.GraceUntil
		resp.GraceUntil = &grace
	}
	return resp
}

// GET /api/license -- current license status
func (s *Server) handleLicenseGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, licenseStateResponse(s.license, s.license.State()))
}

// GET /api/license/features -- the catalog with each feature's current state
func (s *Server) handleLicenseFeatures(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, licenseFeatureCatalog(s.license.State()))
}

func readLicenseImportRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, licenseImportMaxBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "reading request body")
		return "", false
	}
	if len(body) > licenseImportMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "license data is too large")
		return "", false
	}
	var req api.LicenseImportRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return "", false
	}
	data := strings.TrimSpace(req.Data)
	if data == "" {
		writeError(w, http.StatusBadRequest, "data cannot be empty")
		return "", false
	}
	return data, true
}

// POST /api/license/verify -- check a license without installing it
func (s *Server) handleLicenseVerify(w http.ResponseWriter, r *http.Request) {
	data, ok := readLicenseImportRequest(w, r)
	if !ok {
		return
	}
	state := s.license.Verify(data)
	writeJSON(w, http.StatusOK, api.LicenseVerifyResponse{
		Valid:    state.Licensed(),
		Status:   string(state.Status),
		License:  licenseSummary(state),
		Features: state.EnabledKeys(),
		Limits:   licenseLimits(state),
		Reason:   state.Reason,
		Warnings: state.Warnings,
	})
}

// PUT /api/license -- install (replace) the license file
func (s *Server) handleLicenseImport(w http.ResponseWriter, r *http.Request) {
	data, ok := readLicenseImportRequest(w, r)
	if !ok {
		return
	}
	state, err := s.license.Install(data)
	if err != nil {
		status := http.StatusBadRequest
		if state.Status != license.StatusInvalid && state.Status != license.StatusExpired {
			status = http.StatusInternalServerError
		}
		writeError(w, status, err.Error())
		return
	}
	log.Printf("license: installed %s license for %s (status %s)", state.Edition(), state.Payload.Company, state.Status)
	writeJSON(w, http.StatusOK, licenseStateResponse(s.license, state))
}

// DELETE /api/license -- remove the license file
func (s *Server) handleLicenseDelete(w http.ResponseWriter, r *http.Request) {
	state, err := s.license.Remove()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("license: removed; running as %s edition", state.Edition())
	writeJSON(w, http.StatusOK, licenseStateResponse(s.license, state))
}
