package csghub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPErrorKeepsLegacyMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "")
	err := client.getJSON(context.Background(), "/anything", &struct{}{})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusTeapot || httpErr.Body != "nope" {
		t.Fatalf("HTTPError = %+v", httpErr)
	}
	if got := err.Error(); got != "API error 418: nope" {
		t.Fatalf("message = %q", got)
	}
}

func TestErrorEnvelopeOf(t *testing.T) {
	err := &HTTPError{StatusCode: http.StatusConflict, Body: `{"code":"LICENSE-ERR-7","msg":"limit reached","context":{"used":10,"limit":10}}`}
	envelope, ok := ErrorEnvelopeOf(err)
	if !ok || envelope.Code != LicenseErrDeviceLimit || envelope.Msg != "limit reached" {
		t.Fatalf("envelope = %+v ok=%v", envelope, ok)
	}
	if _, ok := ErrorEnvelopeOf(&HTTPError{StatusCode: http.StatusNotFound, Body: "404 page not found"}); ok {
		t.Fatal("a framework 404 must not parse as a business error")
	}
	if _, ok := ErrorEnvelopeOf(errors.New("network")); ok {
		t.Fatal("a non-HTTP error must not parse")
	}
	httpErr, isHTTP := AsHTTPError(err)
	if !isHTTP || httpErr.StatusCode != http.StatusConflict {
		t.Fatal("HTTP status was not preserved")
	}
}

func TestCsgliteStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/licenses/csglite/status" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Errorf("authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"code":"","msg":"OK","data":{"has_license":true,"state":"active","license_key":"subscription:4:1","expire_time":"2027-01-01T00:00:00Z","used":3,"limit":10}}`))
	}))
	defer srv.Close()

	status, err := NewClient(srv.URL, "token-1").CsgliteStatus(context.Background())
	if err != nil {
		t.Fatalf("CsgliteStatus: %v", err)
	}
	if !status.HasLicense || status.State != "active" || status.LicenseKey != "subscription:4:1" || status.Used != 3 || status.Limit != 10 {
		t.Fatalf("status = %+v", status)
	}
	if status.ExpireTime == nil || status.ExpireTime.Year() != 2027 {
		t.Fatalf("expire time = %v", status.ExpireTime)
	}
}

func TestDownloadCsgliteLicense(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"","msg":"OK","data":{"data":{"id":1},"license":"-----BEGIN LICENSE KEY-----\nabc\n-----END LICENSE KEY-----"}}`))
	}))
	defer srv.Close()

	text, err := NewClient(srv.URL, "").DownloadCsgliteLicense(context.Background())
	if err != nil {
		t.Fatalf("DownloadCsgliteLicense: %v", err)
	}
	if !strings.Contains(text, "BEGIN LICENSE KEY") {
		t.Fatalf("license = %q", text)
	}
}

func TestActivateCsgliteLicense(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req CsgliteActivationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Mode != ActivationModeClaim || req.DeviceID != "abc" || req.License != "pem" {
			t.Errorf("request = %+v", req)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("activation must not need a token, got %q", got)
		}
		_, _ = w.Write([]byte(`{"code":"","msg":"OK","data":{"recorded":true,"used":4,"limit":10,"over_limit":false}}`))
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL, "").ActivateCsgliteLicense(context.Background(), CsgliteActivationRequest{
		License: "pem", DeviceID: "abc", Mode: ActivationModeClaim,
	})
	if err != nil {
		t.Fatalf("ActivateCsgliteLicense: %v", err)
	}
	if !result.Recorded || result.Used != 4 || result.Limit != 10 || result.OverLimit {
		t.Fatalf("result = %+v", result)
	}
}
