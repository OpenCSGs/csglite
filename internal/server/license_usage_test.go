package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/license/licensetest"
)

func TestLicenseDeviceUsageNeedsNoAccountAndDoesNotActivate(t *testing.T) {
	var calls atomic.Int32
	var pem string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/licenses/csglite/activations" || r.Header.Get("Authorization") != "" {
			t.Error("usage query unexpectedly used account credentials")
		}
		var req csghub.CsgliteActivationRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.Mode != csghub.ActivationModeStatus || req.License != pem || req.DeviceID != "" {
			t.Error("usage query must only inspect the signed license")
		}
		writeEnvelope(w, `{"recorded":false,"used":12,"limit":10,"over_limit":true}`)
	}))
	defer hub.Close()
	s, key, _ := newLicenseCloudServer(t, hub.URL, "")
	pem = licensetest.Encode(t, key, licensetest.Payload(time.Now()))
	if _, err := s.license.Install(pem); err != nil {
		t.Fatal(err)
	}
	got := s.licenseDeviceUsage(t.Context())
	if !got.Available || got.Used == nil || *got.Used != 12 || got.Remaining == nil || *got.Remaining != 0 || got.Limit != 10 {
		t.Fatalf("unexpected usage: %+v", got)
	}
	_ = s.licenseDeviceUsage(t.Context())
	if calls.Load() != 1 {
		t.Fatal("usage was not cached")
	}
	if _, ok := s.license.Activations().Load(); ok {
		t.Fatal("query created a local activation record")
	}
	if s.cfg.Token != "" {
		t.Fatal("query changed the account connection")
	}
}

func TestLicenseDeviceUsageUnsupportedDoesNotClaim(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid request body", http.StatusBadRequest)
	}))
	defer hub.Close()
	s, key, _ := newLicenseCloudServer(t, hub.URL, "")
	if _, err := s.license.Install(licensetest.Encode(t, key, licensetest.Payload(time.Now()))); err != nil {
		t.Fatal(err)
	}
	got := s.licenseDeviceUsage(t.Context())
	if got.Available || got.Used != nil || got.Remaining != nil || got.Reason != "unsupported" {
		t.Fatalf("unknown usage was fabricated: %+v", got)
	}
}
