package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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

// licenseWriteErrorStatus maps an install or remove failure onto a status the
// caller can act on: a rejected license and an environment-managed license are
// the caller's to fix, everything else is a server fault.
func licenseWriteErrorStatus(state license.State, err error) int {
	switch {
	case errors.Is(err, license.ErrManagedByEnv):
		return http.StatusConflict
	case state.Status == license.StatusInvalid || state.Status == license.StatusExpired:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// isLicenseManagementPath reports whether a path reads or changes the
// installed license. These carry customer identity and allow deleting the
// license, so they are kept same-origin even though the rest of the local API
// answers with a wildcard CORS header.
func isLicenseManagementPath(path string) bool {
	return path == "/api/license" || strings.HasPrefix(path, "/api/license/")
}

// requestIsCrossOrigin reports whether a browser sent this request from
// another origin. Non-browser callers (the CLI, curl) send no Origin at all.
func requestIsCrossOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return true
	}
	return !strings.EqualFold(parsed.Host, r.Host)
}

// licenseOriginGuard refuses license routes to pages served from another
// origin, so a site the user happens to visit cannot read the customer name
// and license ID out of the local API or delete the installed license.
func licenseOriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLicenseManagementPath(r.URL.Path) && requestIsCrossOrigin(r) {
			writeError(w, http.StatusForbidden, "license endpoints are not available to cross-origin callers")
			return
		}
		next.ServeHTTP(w, r)
	})
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
	if def.Type != license.FeatureTypeBoolean {
		// Wrapping happens at route registration, so a misuse fails the
		// process (and every test that builds routes) instead of turning into
		// a route that silently answers 403 forever.
		panic(fmt.Sprintf("requireFeature: %q is a %s limit, not a boolean feature; check it with s.license.Limit", def.Key, def.Type))
	}
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
	resp.License = license.Summary(state.Payload)
	resp.Features = state.EnabledKeys()
	resp.Limits = state.LimitsCopy()
	resp.FeatureCatalog = state.APICatalog(license.Catalog())
	return resp
}

// GET /api/license -- current license status
func (s *Server) handleLicenseGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.license.State().APIState(s.license.FilePath()))
}

// GET /api/license/features -- the catalog with each feature's current state
func (s *Server) handleLicenseFeatures(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.license.State().APICatalog(license.Catalog()))
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
	writeJSON(w, http.StatusOK, s.license.Verify(data).APIVerify())
}

// PUT /api/license -- install (replace) the license file
func (s *Server) handleLicenseImport(w http.ResponseWriter, r *http.Request) {
	data, ok := readLicenseImportRequest(w, r)
	if !ok {
		return
	}
	state, err := s.license.Install(data)
	if err != nil {
		writeError(w, licenseWriteErrorStatus(state, err), err.Error())
		return
	}
	// Install re-reads the source, so a concurrent delete can leave no
	// payload behind even though the write succeeded.
	company := "unknown"
	if state.Payload != nil {
		company = state.Payload.Company
	}
	log.Printf("license: installed %s license for %s (status %s)", state.Edition(), company, state.Status)
	writeJSON(w, http.StatusOK, state.APIState(s.license.FilePath()))
}

// DELETE /api/license -- remove the license file
func (s *Server) handleLicenseDelete(w http.ResponseWriter, r *http.Request) {
	state, err := s.license.Remove()
	if err != nil {
		writeError(w, licenseWriteErrorStatus(state, err), err.Error())
		return
	}
	log.Printf("license: removed; running as %s edition", state.Edition())
	writeJSON(w, http.StatusOK, state.APIState(s.license.FilePath()))
}
