package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/deviceid"
	"github.com/opencsgs/csglite/internal/httpjson"
	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	// licenseCloudCacheTTL keeps Settings from calling the hub on every poll.
	licenseCloudCacheTTL = 10 * time.Second
	// licenseCloudCacheKey is fixed because a license applies to the whole
	// instance, not to a user profile.
	licenseCloudCacheKey = "cloud-license-status"
	// licenseActivationRetryDelay backs off after a server that has no
	// activation route (framework 404), so an older deployment is retried once
	// a day rather than on every start.
	licenseActivationRetryDelay = 24 * time.Hour
)

// Local error codes for POST /api/license/cloud/import.
const (
	licenseCodeLoginRequired   = "login_required"
	licenseCodeTokenInvalid    = "token_invalid"
	licenseCodeNoLicense       = "no_license"
	licenseCodeAlreadyCurrent  = "already_current"
	licenseCodeCloudNotStarted = "cloud_not_started"
	licenseCodeEnvManaged      = "env_managed"
	licenseCodeRateLimited     = "rate_limited"
	licenseCodeBadRequest      = "bad_request"
	licenseCodeUnreachable     = "unreachable"
	licenseCodeUnsupported     = "unsupported"
)

type licenseCloudCacheEntry struct {
	status    api.LicenseCloudStatus
	fetchedAt time.Time
}

// licenseFailure classifies a hub call so the import and report paths can
// share one mapping.
type licenseFailure int

const (
	licenseFailureUnreachable licenseFailure = iota
	licenseFailureUnauthorized
	licenseFailureUnsupported
	licenseFailureNotFound
	licenseFailureDeviceLimit
	licenseFailureExpired
	licenseFailureRateLimited
	licenseFailureServer
	licenseFailureBadRequest
)

func classifyLicenseFailure(err error) licenseFailure {
	httpErr, ok := csghub.AsHTTPError(err)
	if !ok {
		// No HTTP response: DNS failure, connection refused, timeout.
		return licenseFailureUnreachable
	}
	switch {
	case httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden:
		return licenseFailureUnauthorized
	case httpErr.StatusCode == http.StatusNotFound:
		// Only a business 404 means the ledger does not know the license.
		// A framework 404 is an older server without the route.
		if envelope, ok := csghub.ErrorEnvelopeOf(err); ok && envelope.Code == csghub.LicenseErrNotFound {
			return licenseFailureNotFound
		}
		return licenseFailureUnsupported
	case httpErr.StatusCode == http.StatusConflict:
		envelope, _ := csghub.ErrorEnvelopeOf(err)
		switch envelope.Code {
		case csghub.LicenseErrDeviceLimit:
			return licenseFailureDeviceLimit
		case csghub.LicenseErrExpired:
			return licenseFailureExpired
		default:
			return licenseFailureBadRequest
		}
	case httpErr.StatusCode == http.StatusTooManyRequests:
		return licenseFailureRateLimited
	case httpErr.StatusCode >= http.StatusInternalServerError:
		return licenseFailureServer
	default:
		return licenseFailureBadRequest
	}
}

func writeLicenseError(w http.ResponseWriter, status int, msg, code string, used, limit int) {
	httpjson.Write(w, status, httpjson.Error{
		Error:     msg,
		ErrorCode: status,
		Code:      code,
		Used:      used,
		Limit:     limit,
	})
}

// licenseManagedByEnv reports whether CSGHUB_LITE_LICENSE supplies the license,
// which makes it read-only for both install and remove.
func (s *Server) licenseManagedByEnv() bool {
	return s.license != nil && s.license.ManagedByEnv()
}

func (s *Server) licenseDevice() (string, error) {
	return deviceid.Resolve(s.storageRoot)
}

// activateLicense calls the hub activation endpoint. The signed PEM is the
// credential, so the client carries no access token.
func (s *Server) activateLicense(ctx context.Context, text, deviceID, mode string) (*csghub.CsgliteActivation, error) {
	client := csghub.NewClient(s.cfg.ServerURL, "")
	return client.ActivateCsgliteLicense(ctx, csghub.CsgliteActivationRequest{
		License:       text,
		DeviceID:      deviceID,
		Platform:      runtime.GOOS,
		Arch:          runtime.GOARCH,
		ClientVersion: s.version,
		Mode:          mode,
	})
}

// licenseKeyLogPrefix identifies a license in logs without printing it.
func licenseKeyLogPrefix(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:8]
}

func (s *Server) invalidateLicenseCloudCache() {
	s.licenseCloudMu.Lock()
	s.licenseCloudCache = nil
	s.licenseCloudMu.Unlock()
}

// licenseCloudStatus returns the cached view of the customer's cloud license.
func (s *Server) licenseCloudStatus(ctx context.Context) api.LicenseCloudStatus {
	s.licenseCloudMu.Lock()
	if entry, ok := s.licenseCloudCache[licenseCloudCacheKey]; ok && time.Since(entry.fetchedAt) < licenseCloudCacheTTL {
		s.licenseCloudMu.Unlock()
		return entry.status
	}
	s.licenseCloudMu.Unlock()

	status := s.fetchLicenseCloudStatus(ctx)

	s.licenseCloudMu.Lock()
	if s.licenseCloudCache == nil {
		s.licenseCloudCache = make(map[string]licenseCloudCacheEntry)
	}
	s.licenseCloudCache[licenseCloudCacheKey] = licenseCloudCacheEntry{status: status, fetchedAt: time.Now()}
	s.licenseCloudMu.Unlock()
	return status
}

func (s *Server) fetchLicenseCloudStatus(ctx context.Context) api.LicenseCloudStatus {
	out := api.LicenseCloudStatus{Online: true}
	if state := s.license.State(); state.Source == "env" {
		out.EnvManaged = true
	}
	token := strings.TrimSpace(s.cfg.Token)
	if token == "" {
		out.Reason = licenseCodeLoginRequired
		return out
	}
	out.LoggedIn = true

	status, err := csghub.NewClient(s.cfg.ServerURL, token).CsgliteStatus(ctx)
	if err != nil {
		switch classifyLicenseFailure(err) {
		case licenseFailureUnauthorized:
			out.LoggedIn = false
			out.Reason = licenseCodeTokenInvalid
		case licenseFailureUnsupported:
			out.Reason = licenseCodeUnsupported
		case licenseFailureUnreachable:
			// The server may still support the API; we just cannot tell now.
			out.Supported = true
			out.Online = false
			out.Reason = licenseCodeUnreachable
		case licenseFailureNotFound:
			// A business 404 means the account has no CSGLite license, not
			// that the service is broken.
			out.Supported = true
			out.State = "none"
			out.HasLicense = false
		default:
			out.Supported = true
			out.Reason = "server_error"
		}
		return out
	}

	out.Supported = true
	out.State = status.State
	if out.State == "" {
		out.State = "none"
	}
	out.HasLicense = status.HasLicense
	out.NotStarted = status.NotStarted
	out.LicenseKey = status.LicenseKey
	out.ExpireTime = status.ExpireTime
	out.GraceUntil = status.GraceUntil
	out.SubscriptionStatus = status.SubscriptionStatus
	out.Used = status.Used
	out.Limit = status.Limit
	return out
}

// GET /api/license/cloud -- cloud license state for the Settings page.
func (s *Server) handleLicenseCloudGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.licenseCloudStatus(r.Context()))
}

// POST /api/license/cloud/import -- download and install the customer's
// license after claiming a device seat.
func (s *Server) handleLicenseCloudImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.licenseManagedByEnv() {
		writeLicenseError(w, http.StatusConflict, "license is managed by environment", licenseCodeEnvManaged, 0, 0)
		return
	}
	token := strings.TrimSpace(s.cfg.Token)
	if token == "" {
		writeLicenseError(w, http.StatusUnauthorized, "login required", licenseCodeLoginRequired, 0, 0)
		return
	}

	client := csghub.NewClient(s.cfg.ServerURL, token)
	status, err := client.CsgliteStatus(ctx)
	if err != nil {
		s.writeCloudFailure(w, err, "cloud license status")
		return
	}
	if !status.HasLicense {
		writeLicenseError(w, http.StatusConflict, "no active subscription", licenseCodeNoLicense, 0, 0)
		return
	}

	local := s.license.State()
	if status.NotStarted && local.Licensed() {
		writeLicenseError(w, http.StatusConflict, "cloud license not started", licenseCodeCloudNotStarted, 0, 0)
		return
	}
	// A different key means a re-issued license or another account, which the
	// user asked to import even when the expiry date did not move forward.
	if local.Licensed() && local.Payload != nil && status.ExpireTime != nil &&
		status.LicenseKey == local.Payload.Key &&
		!status.ExpireTime.After(local.Payload.ExpireTime) {
		writeLicenseError(w, http.StatusConflict, "license is up to date", licenseCodeAlreadyCurrent, 0, 0)
		return
	}

	text, err := client.DownloadCsgliteLicense(ctx)
	if err != nil {
		s.writeCloudFailure(w, err, "cloud license download")
		return
	}
	verified := s.license.Verify(text)
	if verified.Status == license.StatusInvalid || verified.Status == license.StatusExpired {
		writeLicenseError(w, http.StatusBadGateway, "cloud license is not usable: "+verified.Reason, licenseCodeBadRequest, 0, 0)
		return
	}

	deviceID, err := s.licenseDevice()
	if err != nil {
		log.Printf("license: cannot determine the device id: %v", err)
		writeLicenseError(w, http.StatusInternalServerError, "cannot register this device", "", 0, 0)
		return
	}
	result, err := s.activateLicense(ctx, text, deviceID, csghub.ActivationModeClaim)
	if err != nil {
		switch classifyLicenseFailure(err) {
		case licenseFailureNotFound:
			// The ledger lost the license between the download and the claim,
			// typically a concurrent delete. The downloaded file is still
			// signed and usable, so install it without a seat, exactly like the
			// manual import path.
			s.installAndMark(w, text, deviceID, license.ActivationUnmanaged, time.Time{}, false)
			return
		case licenseFailureUnsupported:
			// status answered but the activation route does not exist: a
			// partial deployment or a proxy in front. Install and report once
			// the route is reachable (4.9.1).
			s.installAndMark(w, text, deviceID, license.ActivationPending, time.Now().Add(licenseActivationRetryDelay), false)
			return
		}
		s.writeCloudFailure(w, err, "cloud license claim")
		return
	}

	state, ok := s.installLicense(w, text)
	if !ok {
		return
	}
	// A concurrent delete can leave the install without a payload; the write
	// succeeded, so the caller still gets the state.
	if state.Payload != nil {
		if err := s.license.Activations().MarkReported(state.Payload.Key, deviceID); err != nil {
			log.Printf("license: recording activation: %v", err)
		}
		log.Printf("license: imported from the cloud for %s (devices %d/%d)", state.Payload.Company, result.Used, result.Limit)
	}
	s.invalidateLicenseCloudCache()
	writeJSON(w, http.StatusOK, state.APIState(s.license.FilePath()))
}

// writeCloudFailure maps a hub failure onto the local import response.
func (s *Server) writeCloudFailure(w http.ResponseWriter, err error, action string) {
	switch classifyLicenseFailure(err) {
	case licenseFailureUnauthorized:
		writeLicenseError(w, http.StatusUnauthorized, "login required", licenseCodeTokenInvalid, 0, 0)
	case licenseFailureUnsupported:
		writeLicenseError(w, http.StatusConflict, "server does not support license import", licenseCodeUnsupported, 0, 0)
	case licenseFailureNotFound:
		writeLicenseError(w, http.StatusConflict, "no active subscription", licenseCodeNoLicense, 0, 0)
	case licenseFailureDeviceLimit:
		writeDeviceLimitError(w, err)
	case licenseFailureExpired:
		writeExpiredError(w, err)
	case licenseFailureRateLimited:
		writeLicenseError(w, http.StatusTooManyRequests, "too many requests", licenseCodeRateLimited, 0, 0)
	case licenseFailureUnreachable, licenseFailureServer:
		log.Printf("license: %s failed: %v", action, err)
		writeLicenseError(w, http.StatusServiceUnavailable, "server unreachable", licenseCodeUnreachable, 0, 0)
	default:
		writeLicenseError(w, http.StatusBadGateway, err.Error(), licenseCodeBadRequest, 0, 0)
	}
}

// writeDeviceLimitError answers an import the hub refused because the license
// has no free device seat. The counts come from the error context so the UI can
// show "used/limit".
func writeDeviceLimitError(w http.ResponseWriter, err error) {
	envelope, _ := csghub.ErrorEnvelopeOf(err)
	used, limit := licenseErrorCounts(envelope)
	msg := "license device limit reached"
	if envelope.Msg != "" {
		msg = envelope.Msg
	}
	writeLicenseError(w, http.StatusConflict, msg, csghub.LicenseErrDeviceLimit, used, limit)
}

// writeExpiredError answers an import the hub considers expired, which is a
// definitive refusal rather than an offline fallback.
func writeExpiredError(w http.ResponseWriter, err error) {
	envelope, _ := csghub.ErrorEnvelopeOf(err)
	msg := "license expired"
	if envelope.Msg != "" {
		msg = envelope.Msg
	}
	writeLicenseError(w, http.StatusConflict, msg, csghub.LicenseErrExpired, 0, 0)
}

func licenseErrorCounts(envelope csghub.ErrorEnvelope) (used, limit int) {
	if value, ok := envelope.Context["used"].(float64); ok {
		used = int(value)
	}
	if value, ok := envelope.Context["limit"].(float64); ok {
		limit = int(value)
	}
	return used, limit
}

// installLicense writes the license and answers the caller on failure.
func (s *Server) installLicense(w http.ResponseWriter, data string) (license.State, bool) {
	state, err := s.license.Install(data)
	if err != nil {
		writeError(w, licenseWriteErrorStatus(state, err), err.Error())
		return state, false
	}
	s.invalidateLicenseCloudCache()
	return state, true
}

// reportLicenseActivation writes off this device when the license is known and
// the record still says pending. It runs on startup and after login, never on
// a timer: an offline instance is allowed to keep working.
func (s *Server) reportLicenseActivation(ctx context.Context) {
	store := s.license.Activations()
	if store == nil || store.Path() == "" {
		return
	}
	if s.licenseManagedByEnv() {
		// CSGHUB_LITE_LICENSE is read-only: the deployment supplies it, so it
		// is never written off to the device ledger.
		return
	}
	state := s.license.State()
	if state.Status == license.StatusInvalid {
		// Keep the record: it still says this device was written off, and a
		// replaced license decides afresh by key and device id.
		return
	}
	if state.Payload == nil || state.Payload.Product != license.ProductCSGLite {
		if _, ok := store.Load(); ok {
			if err := store.Delete(); err != nil {
				log.Printf("license: removing orphaned activation state: %v", err)
			}
		}
		return
	}

	deviceID, err := s.licenseDevice()
	if err != nil {
		log.Printf("license: cannot determine the device id: %v", err)
		return
	}
	key := state.Payload.Key
	record, err := store.EnsurePending(key, deviceID)
	if err != nil {
		log.Printf("license: recording activation: %v", err)
		return
	}
	if record.Status != license.ActivationPending || !store.Due(record) {
		return
	}

	text, err := s.license.Raw()
	if err != nil || strings.TrimSpace(text) == "" {
		return
	}
	result, err := s.activateLicense(ctx, text, deviceID, csghub.ActivationModeReport)
	// A concurrent import can replace the license while the report is in
	// flight. Its record describes the new license, so an outcome for the
	// previous key must not overwrite it; the next startup reports the new key.
	if current := s.license.State(); current.Payload == nil || current.Payload.Key != key {
		log.Printf("license: dropping a stale write-off outcome for key %s; the installed license changed", licenseKeyLogPrefix(key))
		return
	}
	if err != nil {
		switch classifyLicenseFailure(err) {
		case licenseFailureNotFound:
			if markErr := store.MarkUnmanaged(key, deviceID); markErr != nil {
				log.Printf("license: recording activation: %v", markErr)
			}
		case licenseFailureUnsupported:
			if markErr := store.MarkRetry(key, deviceID, time.Now().Add(licenseActivationRetryDelay)); markErr != nil {
				log.Printf("license: recording activation: %v", markErr)
			}
		default:
			log.Printf("license: device write-off for key %s failed, will retry later: %v", licenseKeyLogPrefix(key), err)
		}
		return
	}
	if !result.Recorded {
		return
	}
	if err := store.MarkReported(key, deviceID); err != nil {
		log.Printf("license: recording activation: %v", err)
	}
	if result.OverLimit {
		log.Printf("license: device write-off for key %s is over the limit (%d/%d)", licenseKeyLogPrefix(key), result.Used, result.Limit)
	}
	s.invalidateLicenseCloudCache()
}
