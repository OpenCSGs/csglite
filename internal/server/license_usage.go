package server

import (
	"context"
	"crypto/sha256"
	"net/http"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/pkg/api"
)

type licenseDeviceUsageCache struct {
	key       [32]byte
	status    api.LicenseDeviceUsage
	fetchedAt time.Time
}

// The signed license proves possession. Reading usage must not need an account
// token, record an activation, or consume a device seat.
func (s *Server) licenseDeviceUsage(ctx context.Context) api.LicenseDeviceUsage {
	state := s.license.State()
	out := api.LicenseDeviceUsage{Limit: license.CSGLiteMaxDevices, CheckedAt: time.Now().UTC()}
	if state.Payload == nil {
		out.Reason = "no_license"
		return out
	}
	out.LicenseKey = state.Payload.Key
	if state.Status == license.StatusInvalid {
		out.Reason = "invalid_license"
		return out
	}
	raw, err := s.license.Raw()
	if err != nil || raw == "" {
		out.Reason = "invalid_license"
		return out
	}
	platform := s.cfg.ServerURL
	key := sha256.Sum256([]byte(platform + "\x00" + raw))
	s.licenseCloudMu.Lock()
	cached := s.licenseDeviceCache
	if cached != nil && cached.key == key && time.Since(cached.fetchedAt) < time.Minute {
		out = cached.status
		s.licenseCloudMu.Unlock()
		return out
	}
	s.licenseCloudMu.Unlock()
	usage, err := csghub.NewClient(platform, "").InspectCsgliteLicense(ctx, raw)
	if err != nil {
		switch classifyLicenseFailure(err) {
		case licenseFailureNotFound:
			out.Reason = "unmanaged_license"
		case licenseFailureUnsupported, licenseFailureBadRequest:
			out.Reason = "unsupported"
		case licenseFailureRateLimited:
			out.Reason = "rate_limited"
		case licenseFailureUnreachable:
			out.Reason = "unreachable"
		default:
			out.Reason = "server_error"
		}
	} else if usage.Used < 0 || usage.Limit <= 0 {
		out.Reason = "server_error"
	} else {
		out.Available = true
		out.Limit = usage.Limit
		used, remaining := usage.Used, max(0, usage.Limit-usage.Used)
		out.Used = &used
		out.Remaining = &remaining
	}
	s.licenseCloudMu.Lock()
	s.licenseDeviceCache = &licenseDeviceUsageCache{key: key, status: out, fetchedAt: time.Now()}
	s.licenseCloudMu.Unlock()
	return out
}

func (s *Server) handleLicenseDeviceUsage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.licenseDeviceUsage(r.Context()))
}
