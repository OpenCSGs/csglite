package csghub

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// licenseStorePath is the CSGHub license store: purchases and license
// delivery for any product that verifies CSGHub-issued licenses.
const licenseStorePath = "/api/v1/license-store"

// CheckoutSession is a license purchase started from a product: the issuer
// records which account, product and device started it, and the portal page
// at CheckoutURL completes the payment. The license goes to the account that
// pays, which may not be the one that started the session.
type CheckoutSession struct {
	SID         string    `json:"sid"`
	Product     string    `json:"product,omitempty"`
	CheckoutURL string    `json:"checkout_url,omitempty"`
	Status      string    `json:"status,omitempty"`
	OwnerName   string    `json:"owner_name,omitempty"`
	OrderNo     string    `json:"order_no,omitempty"`
	ExpireAt    time.Time `json:"expire_at"`
	// PaidByOther reports that another CSGHub account paid the session's
	// order, so the license belongs to that account (BuyerName, masked).
	PaidByOther bool   `json:"paid_by_other,omitempty"`
	BuyerName   string `json:"buyer_name,omitempty"`
}

// Checkout session statuses.
const (
	CheckoutPending   = "pending"
	CheckoutPaying    = "paying"
	CheckoutCompleted = "completed"
	CheckoutExpired   = "expired"
)

// StoreLicense is the issuer's current license of one product for the
// signed-in account. License holds the LICENSE KEY text when Status is
// "active"; DeviceCount is how many devices the account has used it on.
type StoreLicense struct {
	Status      string    `json:"status"`
	Product     string    `json:"product,omitempty"`
	License     string    `json:"license,omitempty"`
	Edition     string    `json:"edition,omitempty"`
	ExpireTime  time.Time `json:"expire_time"`
	DeviceCount int       `json:"device_count"`
}

type createCheckoutSessionRequest struct {
	Product       string `json:"product"`
	DeviceID      string `json:"device_id,omitempty"`
	ClientVersion string `json:"client_version"`
}

// CreateCheckoutSession starts a purchase of product for the signed-in
// account on deviceID.
func (c *Client) CreateCheckoutSession(ctx context.Context, product, deviceID, clientVersion string) (*CheckoutSession, error) {
	var resp APIResponse[CheckoutSession]
	body := createCheckoutSessionRequest{Product: product, DeviceID: deviceID, ClientVersion: clientVersion}
	if err := c.postJSON(ctx, licenseStorePath+"/checkout-sessions", body, &resp); err != nil {
		return nil, fmt.Errorf("creating checkout session: %w", err)
	}
	if strings.TrimSpace(resp.Data.SID) == "" || strings.TrimSpace(resp.Data.CheckoutURL) == "" {
		return nil, fmt.Errorf("creating checkout session: the response has no session ID or checkout URL")
	}
	return &resp.Data, nil
}

// GetCheckoutSession returns the status of a purchase.
func (c *Client) GetCheckoutSession(ctx context.Context, sid string) (*CheckoutSession, error) {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return nil, fmt.Errorf("session ID is empty")
	}
	var resp APIResponse[CheckoutSession]
	if err := c.getJSON(ctx, licenseStorePath+"/checkout-sessions/"+url.PathEscape(sid), &resp); err != nil {
		return nil, fmt.Errorf("getting checkout session: %w", err)
	}
	return &resp.Data, nil
}

// GetCurrentLicense returns the signed-in account's license of product,
// reporting this installation as deviceID so the issuer counts it as a device.
// An empty deviceID is left out of the request.
func (c *Client) GetCurrentLicense(ctx context.Context, product, deviceID string) (*StoreLicense, error) {
	query := url.Values{"product": {product}}
	if deviceID != "" {
		query.Set("device_id", deviceID)
	}
	path := licenseStorePath + "/current?" + query.Encode()
	var resp APIResponse[StoreLicense]
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("getting current license: %w", err)
	}
	return &resp.Data, nil
}
