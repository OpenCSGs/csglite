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
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
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

// isClusterManagementPath reports whether a path administers the cluster:
// forming or leaving one, admitting or removing a node, or changing how work
// is placed. None of it belongs to a page from another origin.
func isClusterManagementPath(path string) bool {
	return path == "/api/cluster" || strings.HasPrefix(path, "/api/cluster/")
}

// isClusterSecretPath reports whether a path hands out the credential that
// lets a machine join this cluster. The join token and the admission code are
// the whole of the admission check, so reading one is equivalent to being
// admitted.
func isClusterSecretPath(path string) bool {
	switch path {
	case "/api/cluster/token", "/api/cluster/token/rotate", "/api/cluster/code":
		return true
	}
	return false
}

// isSensitiveLocalPath reports whether a path must never be readable by a page
// the user happens to have open. Those pages reach the local API through the
// browser, which is why the wildcard CORS header is withheld here and the
// origin is checked.
func isSensitiveLocalPath(path string) bool {
	return isLicenseManagementPath(path) || isClusterManagementPath(path)
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

// sensitiveOriginGuard refuses the license and cluster routes to pages served
// from another origin, so a site the user happens to visit cannot read the
// customer name and license ID out of the local API, delete the installed
// license, or read the token that admits a machine to the cluster.
func sensitiveOriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestIsCrossOrigin(r) {
			switch {
			case isLicenseManagementPath(r.URL.Path):
				writeError(w, http.StatusForbidden, "license endpoints are not available to cross-origin callers")
				return
			case isClusterManagementPath(r.URL.Path):
				writeError(w, http.StatusForbidden, "cluster endpoints are not available to cross-origin callers")
				return
			}
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

// PUT /api/license -- install (replace) the license file.
//
// A valid license is claimed against the customer's device quota before it is
// written. The validity rules are unchanged: only invalid and expired licenses
// are rejected locally. When the hub cannot be reached the license is installed
// anyway and the device is reported later, because an offline instance is
// allowed to work without the quota.
func (s *Server) handleLicenseImport(w http.ResponseWriter, r *http.Request) {
	data, ok := readLicenseImportRequest(w, r)
	if !ok {
		return
	}
	if s.licenseManagedByEnv() {
		writeError(w, http.StatusConflict, fmt.Sprintf("%v: unset %s before installing a file", license.ErrManagedByEnv, license.EnvLicense))
		return
	}
	// Keep the previous rejection path for unusable licenses: Install produces
	// the same message and status as before.
	verified := s.license.Verify(data)
	if verified.Status == license.StatusInvalid || verified.Status == license.StatusExpired {
		state, err := s.license.Install(data)
		writeError(w, licenseWriteErrorStatus(state, err), err.Error())
		return
	}
	licenseKey := "unknown"
	if verified.Payload != nil {
		licenseKey = verified.Payload.Key
	}

	deviceID, err := s.licenseDevice()
	if err != nil {
		// The license is usable, so install it; an unidentifiable device simply
		// cannot be written off.
		log.Printf("license: cannot determine the device id: %v", err)
		state, installed := s.installLicense(w, data)
		if !installed {
			return
		}
		writeJSON(w, http.StatusOK, state.APIState(s.license.FilePath()))
		return
	}

	result, err := s.activateLicense(r.Context(), data, deviceID, csghub.ActivationModeClaim)
	if err != nil {
		s.handleClaimFailure(w, data, deviceID, licenseKey, err)
		return
	}

	state, installed := s.installAndMark(w, data, deviceID, license.ActivationReported, time.Time{}, false)
	if !installed {
		return
	}
	company := "unknown"
	if state.Payload != nil {
		company = state.Payload.Company
	}
	log.Printf("license: installed %s license for %s (status %s, devices %d/%d)",
		state.Edition(), company, state.Status, result.Used, result.Limit)
}

// handleClaimFailure decides what to do when the activation call did not
// succeed. Only a definitive refusal keeps the license out; a broken or
// unreachable hub falls back to offline behaviour, while the automatic import
// never does.
func (s *Server) handleClaimFailure(w http.ResponseWriter, data, deviceID, licenseKey string, err error) {
	switch classifyLicenseFailure(err) {
	case licenseFailureDeviceLimit:
		writeDeviceLimitError(w, err)
	case licenseFailureExpired:
		writeExpiredError(w, err)
	case licenseFailureRateLimited:
		writeLicenseError(w, http.StatusTooManyRequests, "too many requests", licenseCodeRateLimited, 0, 0)
	case licenseFailureNotFound:
		// The ledger does not know this license (admin-issued or historical).
		s.installAndMark(w, data, deviceID, license.ActivationUnmanaged, time.Time{}, false)
	case licenseFailureUnsupported:
		// An older server has no activation route; report once it is upgraded.
		s.installAndMark(w, data, deviceID, license.ActivationPending, time.Now().Add(licenseActivationRetryDelay), false)
	case licenseFailureUnreachable, licenseFailureServer:
		log.Printf("license: device write-off for key %s failed, will retry later: %v", licenseKeyLogPrefix(licenseKey), err)
		s.installAndMark(w, data, deviceID, license.ActivationPending, time.Time{}, true)
	default:
		writeLicenseError(w, http.StatusBadGateway, err.Error(), licenseCodeBadRequest, 0, 0)
	}
}

// installAndMark writes the license and records the write-off state. A state
// write failure does not fail the request: the license is already installed
// and the startup scan reports it again. offline marks a response whose device
// was not confirmed by the server, so the UI can say the device is reported
// later.
func (s *Server) installAndMark(w http.ResponseWriter, data, deviceID string, status license.ActivationStatus, retryAt time.Time, offline bool) (license.State, bool) {
	state, ok := s.installLicense(w, data)
	if !ok {
		return state, false
	}
	pendingReport := false
	// Install re-reads the file, so a concurrent delete can leave no payload
	// even though the write succeeded. The license is installed; there is just
	// no key to record, and the startup scan handles the next import.
	if state.Payload != nil {
		store := s.license.Activations()
		key := state.Payload.Key
		var err error
		switch status {
		case license.ActivationReported:
			err = store.MarkReported(key, deviceID)
		case license.ActivationUnmanaged:
			err = store.MarkUnmanaged(key, deviceID)
		default:
			// A report is still owed unless the record is already reported or
			// unmanaged; EnsurePending never downgrades those terminal states.
			// A failed write leaves pendingReport false: the response must not
			// promise a report this process could not record.
			var record license.Activation
			record, err = store.EnsurePending(key, deviceID)
			if err == nil {
				pendingReport = record.Status == license.ActivationPending
				if pendingReport && !retryAt.IsZero() {
					err = store.MarkRetry(key, deviceID, retryAt)
				}
			}
		}
		if err != nil {
			log.Printf("license: recording activation: %v", err)
		}
	}
	apiState := state.APIState(s.license.FilePath())
	apiState.OfflineImport = offline && pendingReport
	writeJSON(w, http.StatusOK, apiState)
	return state, true
}

// DELETE /api/license -- remove the license file
func (s *Server) handleLicenseDelete(w http.ResponseWriter, r *http.Request) {
	state, err := s.license.Remove()
	if err != nil {
		writeError(w, licenseWriteErrorStatus(state, err), err.Error())
		return
	}
	log.Printf("license: removed; running as %s edition", state.Edition())
	s.invalidateLicenseCloudCache()
	writeJSON(w, http.StatusOK, state.APIState(s.license.FilePath()))
}
