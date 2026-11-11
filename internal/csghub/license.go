package csghub

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// CSGLite license error codes carried in the envelope's "code" field.
const (
	LicenseErrExpired     = "LICENSE-ERR-1"
	LicenseErrNotFound    = "LICENSE-ERR-5"
	LicenseErrDeviceLimit = "LICENSE-ERR-7"
)

// Activation modes accepted by POST /api/v1/licenses/csglite/activations.
const (
	ActivationModeClaim  = "claim"
	ActivationModeReport = "report"
)

// licenseRequestTimeout keeps license calls short enough that an offline
// import does not appear to hang; the shared Client uses 30 seconds.
const licenseRequestTimeout = 8 * time.Second

// responseEnvelope is the CSGHub response shape: {"code":"","msg":"OK","data":{...}}.
type responseEnvelope struct {
	Code string          `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// CsgliteStatus is the CSGLite license state, subscription state and device
// usage of the authenticated customer.
type CsgliteStatus struct {
	HasLicense         bool       `json:"has_license"`
	State              string     `json:"state"`
	LicenseKey         string     `json:"license_key"`
	Product            string     `json:"product"`
	Edition            string     `json:"edition"`
	StartTime          *time.Time `json:"start_time"`
	ExpireTime         *time.Time `json:"expire_time"`
	GraceUntil         string     `json:"grace_until"`
	NotStarted         bool       `json:"not_started"`
	SubscriptionStatus string     `json:"subscription_status"`
	Used               int        `json:"used"`
	Limit              int        `json:"limit"`
}

// CsgliteActivationRequest records one device against a license.
type CsgliteActivationRequest struct {
	License       string `json:"license"`
	DeviceID      string `json:"device_id"`
	Platform      string `json:"platform"`
	Arch          string `json:"arch"`
	ClientVersion string `json:"client_version"`
	Mode          string `json:"mode"`
}

// CsgliteActivation is the server's answer to a claim or report.
type CsgliteActivation struct {
	Recorded  bool `json:"recorded"`
	Used      int  `json:"used"`
	Limit     int  `json:"limit"`
	OverLimit bool `json:"over_limit"`
}

// CsgliteStatus fetches the license status of the logged-in customer.
func (c *Client) CsgliteStatus(ctx context.Context) (*CsgliteStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, licenseRequestTimeout)
	defer cancel()
	var envelope responseEnvelope
	if err := c.getJSON(ctx, "/api/v1/licenses/csglite/status", &envelope); err != nil {
		return nil, err
	}
	var out CsgliteStatus
	if err := decodeEnvelopeData(envelope, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DownloadCsgliteLicense returns the raw license PEM of the logged-in
// customer's current CSGLite license.
func (c *Client) DownloadCsgliteLicense(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, licenseRequestTimeout)
	defer cancel()
	var envelope responseEnvelope
	if err := c.getJSON(ctx, "/api/v1/licenses/download/csglite", &envelope); err != nil {
		return "", err
	}
	var out struct {
		License string `json:"license"`
	}
	if err := decodeEnvelopeData(envelope, &out); err != nil {
		return "", err
	}
	if out.License == "" {
		return "", fmt.Errorf("license download returned no license text")
	}
	return out.License, nil
}

// ActivateCsgliteLicense claims or reports a device. The signed PEM is the
// credential, so no access token is required.
func (c *Client) ActivateCsgliteLicense(ctx context.Context, req CsgliteActivationRequest) (*CsgliteActivation, error) {
	ctx, cancel := context.WithTimeout(ctx, licenseRequestTimeout)
	defer cancel()
	var envelope responseEnvelope
	if err := c.postJSON(ctx, "/api/v1/licenses/csglite/activations", req, &envelope); err != nil {
		return nil, err
	}
	var out CsgliteActivation
	if err := decodeEnvelopeData(envelope, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func decodeEnvelopeData(envelope responseEnvelope, out any) error {
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("API response has no data")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decoding API response: %w", err)
	}
	return nil
}
