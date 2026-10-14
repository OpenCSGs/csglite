package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/pkg/api"
)

// licenseSyncTimeout bounds one background sync started after a sign-in.
const licenseSyncTimeout = 30 * time.Second

// cloudLoginRequiredCode tells the UI to send the user through CSGHub sign-in
// before an upgrade can start.
const cloudLoginRequiredCode = "cloud_login_required"

// cloudLoginRequiredResponse is the 401 for license routes that need a
// signed-in CSGHub account.
type cloudLoginRequiredResponse struct {
	Error     string `json:"error"`
	ErrorCode int    `json:"errorCode"`
	Code      string `json:"code"`
	LoginURL  string `json:"login_url"`
}

func (s *Server) writeCloudLoginRequired(w http.ResponseWriter, r *http.Request, msg string) {
	writeJSON(w, http.StatusUnauthorized, cloudLoginRequiredResponse{
		Error:     msg,
		ErrorCode: http.StatusUnauthorized,
		Code:      cloudLoginRequiredCode,
		LoginURL:  s.cloudAuthStatus(r.Context()).LoginURL,
	})
}

// licenseStoreClient returns a CSGHub client for the signed-in account, or nil
// when nobody is signed in.
func (s *Server) licenseStoreClient() *csghub.Client {
	token := strings.TrimSpace(s.cfg.Token)
	if token == "" {
		return nil
	}
	return csghub.NewClient(s.cfg.ServerURL, token)
}

// fetchStoreLicense is the Syncer's FetchFunc: it asks the issuer for the
// signed-in account's license, reporting this installation as deviceID.
func (s *Server) fetchStoreLicense(ctx context.Context, deviceID string) (license.RemoteLicense, error) {
	client := s.licenseStoreClient()
	if client == nil {
		return license.RemoteLicense{}, license.ErrNoCredentials
	}
	current, err := client.GetCurrentLicense(ctx, license.ProductCSGLite, deviceID)
	if err != nil {
		return license.RemoteLicense{}, err
	}
	return license.RemoteLicense{Status: current.Status, Product: current.Product, License: current.License, DeviceCount: current.DeviceCount}, nil
}

// triggerLicenseSync syncs in the background, as after a sign-in. It runs on
// the server's lifetime context, so nothing starts before Run or outlives it.
func (s *Server) triggerLicenseSync() {
	s.mu.Lock()
	parent := s.loadCtx
	s.mu.Unlock()
	if parent == nil || s.licenseSync == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(parent, licenseSyncTimeout)
		defer cancel()
		if _, err := s.licenseSync.Sync(ctx); err != nil && ctx.Err() == nil {
			log.Printf("license: sync after sign-in failed: %v", err)
		}
	}()
}

// licenseStoreErrorStatus maps an issuer failure onto the local answer: an
// expired sign-in is the user's to fix, everything else is a bad gateway.
func licenseStoreErrorStatus(err error) int {
	var apiErr *csghub.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusUnauthorized:
			return http.StatusUnauthorized
		case http.StatusForbidden:
			return http.StatusForbidden
		case http.StatusNotFound:
			return http.StatusNotFound
		}
	}
	return http.StatusBadGateway
}

// licenseStoreErrorMessage prefers the issuer's own message over the raw body.
func licenseStoreErrorMessage(err error) string {
	var apiErr *csghub.APIError
	if errors.As(err, &apiErr) {
		if msg := apiErr.Message(); msg != "" {
			return msg
		}
	}
	return err.Error()
}

func (s *Server) writeLicenseStoreError(w http.ResponseWriter, r *http.Request, err error) {
	status := licenseStoreErrorStatus(err)
	if status == http.StatusUnauthorized {
		s.writeCloudLoginRequired(w, r, "your CSGHub sign-in has expired; sign in again")
		return
	}
	writeError(w, status, licenseStoreErrorMessage(err))
}

// POST /api/license/upgrade -- start an Enterprise purchase on CSGHub
func (s *Server) handleLicenseUpgradeStart(w http.ResponseWriter, r *http.Request) {
	client := s.licenseStoreClient()
	if client == nil {
		s.writeCloudLoginRequired(w, r, "sign in to CSGHub to upgrade")
		return
	}
	session, err := client.CreateCheckoutSession(r.Context(), license.ProductCSGLite, s.license.DeviceID(), s.version)
	if err != nil {
		s.writeLicenseStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.LicenseCheckoutResponse{
		SID:         session.SID,
		CheckoutURL: session.CheckoutURL,
		ExpireAt:    session.ExpireAt,
	})
}

// GET /api/license/upgrade/{sid} -- poll a purchase; installs the license once paid
func (s *Server) handleLicenseUpgradeStatus(w http.ResponseWriter, r *http.Request) {
	client := s.licenseStoreClient()
	if client == nil {
		s.writeCloudLoginRequired(w, r, "sign in to CSGHub to upgrade")
		return
	}
	session, err := client.GetCheckoutSession(r.Context(), r.PathValue("sid"))
	if err != nil {
		s.writeLicenseStoreError(w, r, err)
		return
	}
	resp := api.LicenseCheckoutStatusResponse{
		SID:         session.SID,
		Status:      session.Status,
		OwnerName:   session.OwnerName,
		OrderNo:     session.OrderNo,
		ExpireAt:    session.ExpireAt,
		PaidByOther: session.PaidByOther,
		BuyerName:   session.BuyerName,
	}
	if session.Status == csghub.CheckoutCompleted {
		result, err := s.licenseSync.Sync(r.Context())
		if err != nil {
			resp.SyncError = licenseStoreErrorMessage(err)
		}
		state := s.licenseAPIState(result.State)
		resp.License = &state
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/license/sync -- fetch the signed-in account's license now
func (s *Server) handleLicenseSync(w http.ResponseWriter, r *http.Request) {
	if s.licenseStoreClient() == nil {
		s.writeCloudLoginRequired(w, r, "sign in to CSGHub to sync the license")
		return
	}
	result, err := s.licenseSync.Sync(r.Context())
	if err != nil {
		s.writeLicenseStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.LicenseSyncResponse{
		Action:  result.Action,
		Reason:  result.Reason,
		License: s.licenseAPIState(result.State),
	})
}
