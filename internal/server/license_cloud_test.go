package server

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/internal/license/licensetest"
	"github.com/opencsgs/csglite/pkg/api"
)

// newLicenseCloudServer builds a server whose license manager trusts a
// throwaway key and keeps its activation state under root.
func newLicenseCloudServer(t *testing.T, hubURL, token string) (*Server, *rsa.PrivateKey, string) {
	t.Helper()
	s := newTestServer(t)
	key := licensetest.NewKey(t)
	root := t.TempDir()
	s.license = license.NewManager(license.Options{
		FilePath:       filepath.Join(root, license.FileName),
		PublicKeys:     []*rsa.PublicKey{&key.PublicKey},
		Version:        s.version,
		Catalog:        gatedTestCatalog(),
		LastCheckPath:  filepath.Join(root, "license.lastcheck"),
		ActivationPath: filepath.Join(root, "license.activation"),
	})
	s.license.Refresh()
	s.cfg.ServerURL = hubURL
	s.cfg.Token = token
	return s, key, root
}

// trustLicenseKey rebuilds the manager so it trusts key, which is what a test
// needs when the hub stub signs the license it serves.
func trustLicenseKey(t *testing.T, s *Server, key *rsa.PrivateKey, root string) {
	t.Helper()
	s.license = license.NewManager(license.Options{
		FilePath:       filepath.Join(root, license.FileName),
		PublicKeys:     []*rsa.PublicKey{&key.PublicKey},
		Version:        s.version,
		Catalog:        gatedTestCatalog(),
		ActivationPath: filepath.Join(root, "license.activation"),
	})
	s.license.Refresh()
}

func writeEnvelope(w http.ResponseWriter, data string) {
	_, _ = w.Write([]byte(`{"code":"","msg":"OK","data":` + data + `}`))
}

func licenseDownloadJSON(t *testing.T, key *rsa.PrivateKey, now time.Time) (string, string) {
	t.Helper()
	pem := licensetest.Encode(t, key, licensetest.Payload(now))
	body, err := json.Marshal(map[string]any{
		"code": "", "msg": "OK",
		"data": map[string]any{"data": map[string]any{"id": 1}, "license": pem},
	})
	if err != nil {
		t.Fatal(err)
	}
	return pem, string(body)
}

func TestLicenseCloudStatusMapping(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		handler    http.HandlerFunc
		want       api.LicenseCloudStatus
		wantExpire bool
	}{
		{
			name:  "no token",
			token: "",
			want:  api.LicenseCloudStatus{Online: true, Reason: licenseCodeLoginRequired},
		},
		{
			name:  "invalid token",
			token: "token",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			want: api.LicenseCloudStatus{Online: true, Reason: licenseCodeTokenInvalid},
		},
		{
			name:  "old server",
			token: "token",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.NotFound(w, r)
			},
			want: api.LicenseCloudStatus{LoggedIn: true, Online: true, Reason: licenseCodeUnsupported},
		},
		{
			name:  "no license on the account",
			token: "token",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"LICENSE-ERR-5","msg":"no current customer license found"}`))
			},
			want: api.LicenseCloudStatus{LoggedIn: true, Online: true, Supported: true, State: "none"},
		},
		{
			name:  "server error",
			token: "token",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
			},
			want: api.LicenseCloudStatus{LoggedIn: true, Online: true, Supported: true, Reason: "server_error"},
		},
		{
			name:  "ok",
			token: "token",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeEnvelope(w, `{"has_license":true,"state":"active","license_key":"subscription:4:1","expire_time":"2027-01-01T00:00:00Z","used":3,"limit":10,"subscription_status":"active"}`)
			},
			want: api.LicenseCloudStatus{
				LoggedIn: true, Online: true, Supported: true, HasLicense: true,
				State: "active", LicenseKey: "subscription:4:1", Used: 3, Limit: 10,
				SubscriptionStatus: "active",
			},
			wantExpire: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hubURL string
			if tc.handler != nil {
				srv := httptest.NewServer(tc.handler)
				t.Cleanup(srv.Close)
				hubURL = srv.URL
			}
			s, _, _ := newLicenseCloudServer(t, hubURL, tc.token)
			got := s.licenseCloudStatus(t.Context())
			// Reason drives the Settings hints, so a wrong mapping is a visible
			// regression even when the rest of the state looks right.
			if got.LoggedIn != tc.want.LoggedIn || got.Online != tc.want.Online || got.Supported != tc.want.Supported ||
				got.HasLicense != tc.want.HasLicense || got.State != tc.want.State || got.LicenseKey != tc.want.LicenseKey ||
				got.Used != tc.want.Used || got.Limit != tc.want.Limit || got.Reason != tc.want.Reason ||
				got.NotStarted != tc.want.NotStarted || (got.ExpireTime != nil) != tc.wantExpire {
				t.Fatalf("status = %+v, want %+v (expire set: %v)", got, tc.want, tc.wantExpire)
			}
		})
	}
}

func TestLicenseCloudStatusUnreachable(t *testing.T) {
	s, _, _ := newLicenseCloudServer(t, "http://127.0.0.1:1", "token")
	got := s.licenseCloudStatus(t.Context())
	if got.Online || got.Reason != licenseCodeUnreachable {
		t.Fatalf("status = %+v, want offline/unreachable", got)
	}
}

func TestLicenseCloudStatusIsCached(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeEnvelope(w, `{"has_license":true,"state":"active","license_key":"k","used":1,"limit":10}`)
	}))
	defer srv.Close()

	s, _, _ := newLicenseCloudServer(t, srv.URL, "token")
	s.licenseCloudStatus(t.Context())
	s.licenseCloudStatus(t.Context())
	if got := calls.Load(); got != 1 {
		t.Fatalf("hub calls = %d, want 1 within the cache window", got)
	}
	s.invalidateLicenseCloudCache()
	s.licenseCloudStatus(t.Context())
	if got := calls.Load(); got != 2 {
		t.Fatalf("hub calls after invalidation = %d, want 2", got)
	}
}

func TestLicenseCloudStatusReportsEnvManaged(t *testing.T) {
	s, key, root := newLicenseCloudServer(t, "", "")
	s.license = license.NewManager(license.Options{
		FilePath:   filepath.Join(root, license.FileName),
		PublicKeys: []*rsa.PublicKey{&key.PublicKey},
		Inline:     licensetest.Encode(t, key, licensetest.Payload(time.Now())),
		Version:    s.version,
		Catalog:    gatedTestCatalog(),
	})
	s.license.Refresh()
	if !s.licenseCloudStatus(t.Context()).EnvManaged {
		t.Fatal("env-managed license must be reported as env_managed")
	}
}

func TestLicenseCloudImportSuccess(t *testing.T) {
	now := time.Now()
	key := licensetest.NewKey(t)
	_, download := licenseDownloadJSON(t, key, now)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/licenses/csglite/status":
			writeEnvelope(w, `{"has_license":true,"state":"active","license_key":"test-license-key","expire_time":"`+now.Add(365*24*time.Hour).UTC().Format(time.RFC3339)+`","used":2,"limit":10}`)
		case "/api/v1/licenses/download/csglite":
			_, _ = w.Write([]byte(download))
		case "/api/v1/licenses/csglite/activations":
			writeEnvelope(w, `{"recorded":true,"used":3,"limit":10}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s, _, _ := newLicenseCloudServer(t, srv.URL, "token")
	s.license = license.NewManager(license.Options{
		FilePath:       filepath.Join(t.TempDir(), license.FileName),
		PublicKeys:     []*rsa.PublicKey{&key.PublicKey},
		Version:        s.version,
		Catalog:        gatedTestCatalog(),
		ActivationPath: filepath.Join(t.TempDir(), "license.activation"),
	})
	s.license.Refresh()

	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/cloud/import", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	if s.license.State().Status != license.StatusValid {
		t.Fatalf("license state = %s", s.license.State().Status)
	}
	record, ok := s.license.Activations().Load()
	if !ok || record.Status != license.ActivationReported {
		t.Fatalf("activation = %+v ok=%v, want reported", record, ok)
	}
	if !strings.Contains(rec.Body.String(), `"status":"valid"`) {
		t.Fatalf("response = %s", rec.Body.String())
	}
}

func TestLicenseCloudImportAcceptsNewKeyWithSameExpiry(t *testing.T) {
	now := time.Now()
	s, key, _ := newLicenseCloudServer(t, "", "token")
	// A re-issued license or another account: the key changed while the expiry
	// date stayed the same, so already_current must not short-circuit.
	payload := licensetest.Payload(now)
	payload.Key = "subscription:9:9"
	pem := licensetest.Encode(t, key, payload)
	body, err := json.Marshal(map[string]any{
		"code": "", "msg": "OK",
		"data": map[string]any{"data": map[string]any{"id": 1}, "license": pem},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/licenses/csglite/status":
			writeEnvelope(w, `{"has_license":true,"state":"active","license_key":"subscription:9:9","expire_time":"`+payload.ExpireTime.UTC().Format(time.RFC3339)+`","used":1,"limit":10}`)
		case "/api/v1/licenses/download/csglite":
			_, _ = w.Write([]byte(body))
		case "/api/v1/licenses/csglite/activations":
			writeEnvelope(w, `{"recorded":true,"used":2,"limit":10}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s.cfg.ServerURL = srv.URL

	if _, err := s.license.Install(licensetest.Encode(t, key, licensetest.Payload(now))); err != nil {
		t.Fatal(err)
	}
	if got := s.license.State().Payload.Key; got != "test-license-key" {
		t.Fatalf("installed key = %q", got)
	}

	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/cloud/import", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	if got := s.license.State().Payload.Key; got != "subscription:9:9" {
		t.Fatalf("installed key = %q, want the cloud key", got)
	}
}

func TestLicenseCloudImportRejectsOverLimit(t *testing.T) {
	now := time.Now()
	key := licensetest.NewKey(t)
	_, download := licenseDownloadJSON(t, key, now)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/licenses/csglite/status":
			writeEnvelope(w, `{"has_license":true,"state":"active","license_key":"test-license-key","used":10,"limit":10}`)
		case "/api/v1/licenses/download/csglite":
			_, _ = w.Write([]byte(download))
		case "/api/v1/licenses/csglite/activations":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"LICENSE-ERR-7","msg":"license device limit reached (10/10)","context":{"used":10,"limit":10}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s, _, _ := newLicenseCloudServer(t, srv.URL, "token")
	s.license = license.NewManager(license.Options{
		FilePath:       filepath.Join(t.TempDir(), license.FileName),
		PublicKeys:     []*rsa.PublicKey{&key.PublicKey},
		Version:        s.version,
		Catalog:        gatedTestCatalog(),
		ActivationPath: filepath.Join(t.TempDir(), "license.activation"),
	})
	s.license.Refresh()

	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/cloud/import", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	var body apiErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "LICENSE-ERR-7" || body.Used != 10 || body.Limit != 10 {
		t.Fatalf("body = %+v", body)
	}
	if s.license.State().Status != license.StatusNone {
		t.Fatal("a rejected claim must not install the license")
	}
}

func TestLicenseCloudImportGuards(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		status    string
		install   bool
		wantCode  int
		wantError string
	}{
		{
			name:      "no license",
			status:    `{"has_license":false,"state":"none","used":0,"limit":10}`,
			wantCode:  http.StatusConflict,
			wantError: licenseCodeNoLicense,
		},
		{
			name:      "not started with a valid local license",
			status:    `{"has_license":true,"state":"not_started","not_started":true,"expire_time":"2027-01-01T00:00:00Z"}`,
			install:   true,
			wantCode:  http.StatusConflict,
			wantError: licenseCodeCloudNotStarted,
		},
		{
			name:      "already current",
			status:    `{"has_license":true,"state":"active","license_key":"test-license-key","expire_time":"` + now.Add(365*24*time.Hour).UTC().Format(time.RFC3339) + `"}`,
			install:   true,
			wantCode:  http.StatusConflict,
			wantError: licenseCodeAlreadyCurrent,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/status") {
					writeEnvelope(w, tc.status)
					return
				}
				t.Errorf("unexpected hub call %s", r.URL.Path)
			}))
			defer srv.Close()

			s, key, _ := newLicenseCloudServer(t, srv.URL, "token")
			if tc.install {
				if _, err := s.license.Install(licensetest.Encode(t, key, licensetest.Payload(now))); err != nil {
					t.Fatal(err)
				}
			}
			rec := httptest.NewRecorder()
			s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/cloud/import", nil))
			if rec.Code != tc.wantCode {
				t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
			}
			var body apiErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tc.wantError {
				t.Fatalf("code = %q, want %q", body.Code, tc.wantError)
			}
		})
	}
}

func TestLicenseCloudImportOldServer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	s, _, _ := newLicenseCloudServer(t, srv.URL, "token")

	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/cloud/import", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	var body apiErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != licenseCodeUnsupported {
		t.Fatalf("body = %+v err=%v", body, err)
	}
}

func TestLicenseCloudImportRequiresLogin(t *testing.T) {
	s, _, _ := newLicenseCloudServer(t, "", "")
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/cloud/import", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
}

// An environment-managed license is read-only, so the import must refuse
// before it calls the hub. The unreachable hub URL proves no request is made:
// a call would turn the answer into 503.
func TestLicenseCloudImportRefusesEnvManagedLicense(t *testing.T) {
	s := newTestServer(t)
	key := licensetest.NewKey(t)
	root := t.TempDir()
	s.license = license.NewManager(license.Options{
		FilePath:       filepath.Join(root, license.FileName),
		PublicKeys:     []*rsa.PublicKey{&key.PublicKey},
		Inline:         licensetest.Encode(t, key, licensetest.Payload(time.Now())),
		Catalog:        gatedTestCatalog(),
		ActivationPath: filepath.Join(root, "license.activation"),
	})
	s.license.Refresh()
	s.cfg.ServerURL = "http://127.0.0.1:1"
	s.cfg.Token = "token"

	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/cloud/import", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	var body apiErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != licenseCodeEnvManaged {
		t.Fatalf("body = %+v err=%v", body, err)
	}
}

func TestLicenseImportFallsBackToOfflineWhenHubIsDown(t *testing.T) {
	s, key, _ := newLicenseCloudServer(t, "http://127.0.0.1:1", "")
	data := licensetest.Encode(t, key, licensetest.Payload(time.Now()))

	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, licenseJSONRequest(http.MethodPut, "/api/license", data))
	if rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	if s.license.State().Status != license.StatusValid {
		t.Fatalf("license state = %s", s.license.State().Status)
	}
	record, ok := s.license.Activations().Load()
	if !ok || record.Status != license.ActivationPending {
		t.Fatalf("activation = %+v ok=%v, want pending", record, ok)
	}
}

// TestLicenseImportOfflineDoesNotPromiseAReportForReportedDevice: re-importing
// the same license offline must not tell the user a report is pending when the
// device was already reported.
func TestLicenseImportOfflineDoesNotPromiseAReportForReportedDevice(t *testing.T) {
	s, key, _ := newLicenseCloudServer(t, newLicenseHubStub(t), "")
	data := licensetest.Encode(t, key, licensetest.Payload(time.Now()))

	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, licenseJSONRequest(http.MethodPut, "/api/license", data))
	if rec.Code != http.StatusOK {
		t.Fatalf("online import: %d %s", rec.Code, rec.Body.String())
	}
	record, ok := s.license.Activations().Load()
	if !ok || record.Status != license.ActivationReported {
		t.Fatalf("activation = %+v ok=%v, want reported", record, ok)
	}

	// The second import cannot reach the hub, but the device needs no report.
	s.cfg.ServerURL = "http://127.0.0.1:1"
	rec = httptest.NewRecorder()
	s.routes().ServeHTTP(rec, licenseJSONRequest(http.MethodPut, "/api/license", data))
	if rec.Code != http.StatusOK {
		t.Fatalf("offline import: %d %s", rec.Code, rec.Body.String())
	}
	var body api.LicenseState
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.OfflineImport {
		t.Fatal("a reported device must not be announced as a pending report")
	}
}

func TestReportLicenseActivationTransitions(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantStatus license.ActivationStatus
		wantRetry  bool
	}{
		{
			name: "recorded",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeEnvelope(w, `{"recorded":true,"used":11,"limit":10,"over_limit":true}`)
			},
			wantStatus: license.ActivationReported,
		},
		{
			name: "server did not record the device",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeEnvelope(w, `{"recorded":false,"used":10,"limit":10}`)
			},
			wantStatus: license.ActivationPending,
		},
		{
			name: "ledger does not know the license",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"LICENSE-ERR-5","msg":"no current customer license found"}`))
			},
			wantStatus: license.ActivationUnmanaged,
		},
		{
			name: "old server without the route",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.NotFound(w, r)
			},
			wantStatus: license.ActivationPending,
			wantRetry:  true,
		},
		{
			name: "unreachable stays pending",
			handler: func(w http.ResponseWriter, r *http.Request) {
				panic("should not be called")
			},
			wantStatus: license.ActivationPending,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hubURL string
			if tc.name == "unreachable stays pending" {
				hubURL = "http://127.0.0.1:1"
			} else {
				srv := httptest.NewServer(tc.handler)
				t.Cleanup(srv.Close)
				hubURL = srv.URL
			}
			s, key, _ := newLicenseCloudServer(t, hubURL, "")
			if _, err := s.license.Install(licensetest.Encode(t, key, licensetest.Payload(now))); err != nil {
				t.Fatal(err)
			}
			s.reportLicenseActivation(t.Context())

			record, ok := s.license.Activations().Load()
			if !ok || record.Status != tc.wantStatus {
				t.Fatalf("activation = %+v ok=%v, want %s", record, ok, tc.wantStatus)
			}
			if tc.wantRetry != (record.NextRetryAt != "") {
				t.Fatalf("next_retry_at = %q, want set=%v", record.NextRetryAt, tc.wantRetry)
			}
		})
	}
}

// TestReportLicenseActivationDropsStaleOutcome: an import that lands while the
// report is in flight must keep its own record; the outcome for the previous
// license is stale and must not overwrite it.
func TestReportLicenseActivationDropsStaleOutcome(t *testing.T) {
	s, key, _ := newLicenseCloudServer(t, "", "")
	oldKey := "test-license-key"
	if _, err := s.license.Install(licensetest.Encode(t, key, licensetest.Payload(time.Now()))); err != nil {
		t.Fatal(err)
	}
	replacement := licensetest.Payload(time.Now())
	replacement.Key = "replacement-license-key"
	replacementData := licensetest.Encode(t, key, replacement)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A concurrent import replaces the license before the hub answers.
		if _, err := s.license.Install(replacementData); err != nil {
			t.Errorf("installing the replacement license: %v", err)
		}
		writeEnvelope(w, `{"recorded":true,"used":2,"limit":10}`)
	}))
	defer srv.Close()
	s.cfg.ServerURL = srv.URL

	s.reportLicenseActivation(t.Context())

	record, ok := s.license.Activations().Load()
	if !ok {
		t.Fatal("the pending record must survive a stale outcome")
	}
	if record.LicenseKey != oldKey || record.Status != license.ActivationPending {
		t.Fatalf("activation = %+v, want the pending record for %q", record, oldKey)
	}
}

func TestReportLicenseActivationSkipsReportedDevices(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeEnvelope(w, `{"recorded":true,"used":1,"limit":10}`)
	}))
	defer srv.Close()

	s, key, _ := newLicenseCloudServer(t, srv.URL, "")
	if _, err := s.license.Install(licensetest.Encode(t, key, licensetest.Payload(time.Now()))); err != nil {
		t.Fatal(err)
	}
	deviceID, err := s.licenseDevice()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.license.Activations().MarkReported("test-license-key", deviceID); err != nil {
		t.Fatal(err)
	}
	s.reportLicenseActivation(t.Context())
	if got := calls.Load(); got != 0 {
		t.Fatalf("hub calls = %d, want 0 for an already reported device", got)
	}
}

func TestReportLicenseActivationRemovesOrphanedState(t *testing.T) {
	s, _, _ := newLicenseCloudServer(t, "http://127.0.0.1:1", "")
	if _, err := s.license.Activations().EnsurePending("key", "device"); err != nil {
		t.Fatal(err)
	}
	s.reportLicenseActivation(t.Context())
	if _, ok := s.license.Activations().Load(); ok {
		t.Fatal("activation state without a license must be removed")
	}
}

func TestLicenseRemoveClearsActivation(t *testing.T) {
	s, key, _ := newLicenseCloudServer(t, "", "")
	if _, err := s.license.Install(licensetest.Encode(t, key, licensetest.Payload(time.Now()))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.license.Activations().EnsurePending("test-license-key", "device"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/license", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := s.license.Activations().Load(); ok {
		t.Fatal("removing the license must remove the activation state")
	}
}

// TestLicenseImportClaimBranches covers every answer the hub can give to the
// claim that a manual import makes.
func TestLicenseImportClaimBranches(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name        string
		status      int
		body        string
		wantCode    int
		wantErr     string
		wantUsed    int
		wantLimit   int
		wantInstall bool
		wantState   license.ActivationStatus
		wantRetry   bool
	}{
		{
			name: "device limit", status: http.StatusConflict,
			body:     `{"code":"LICENSE-ERR-7","msg":"license device limit reached (10/10)","context":{"used":10,"limit":10}}`,
			wantCode: http.StatusConflict, wantErr: "LICENSE-ERR-7", wantUsed: 10, wantLimit: 10,
		},
		{
			name: "expired", status: http.StatusConflict,
			body:     `{"code":"LICENSE-ERR-1","msg":"license expired"}`,
			wantCode: http.StatusConflict, wantErr: "LICENSE-ERR-1",
		},
		{
			name: "rate limited", status: http.StatusTooManyRequests,
			body:     `{"error":"too many requests","code":"RATE_LIMITED"}`,
			wantCode: http.StatusTooManyRequests, wantErr: "rate_limited",
		},
		{
			name: "ledger does not know the license", status: http.StatusNotFound,
			body:        `{"code":"LICENSE-ERR-5","msg":"no current customer license found"}`,
			wantCode:    http.StatusOK,
			wantInstall: true, wantState: license.ActivationUnmanaged,
		},
		{
			name: "old server without the route", status: http.StatusNotFound,
			body:        "404 page not found",
			wantCode:    http.StatusOK,
			wantInstall: true, wantState: license.ActivationPending, wantRetry: true,
		},
		{
			name: "unexpected client error", status: http.StatusBadRequest,
			body:     `{"error":"bad request"}`,
			wantCode: http.StatusBadGateway, wantErr: "bad_request",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			s, key, _ := newLicenseCloudServer(t, srv.URL, "")
			data := licensetest.Encode(t, key, licensetest.Payload(now))
			rec := httptest.NewRecorder()
			s.routes().ServeHTTP(rec, licenseJSONRequest(http.MethodPut, "/api/license", data))
			if rec.Code != tc.wantCode {
				t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
			}
			if tc.wantErr != "" {
				var body apiErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Code != tc.wantErr {
					t.Fatalf("code = %q, want %q", body.Code, tc.wantErr)
				}
				if body.Used != tc.wantUsed || body.Limit != tc.wantLimit {
					t.Fatalf("used/limit = %d/%d, want %d/%d", body.Used, body.Limit, tc.wantUsed, tc.wantLimit)
				}
			}
			installed := s.license.State().Status == license.StatusValid
			if installed != tc.wantInstall {
				t.Fatalf("installed = %v, want %v", installed, tc.wantInstall)
			}
			record, ok := s.license.Activations().Load()
			if !tc.wantInstall {
				if ok {
					t.Fatalf("activation = %+v, want none", record)
				}
				return
			}
			if !ok || record.Status != tc.wantState {
				t.Fatalf("activation = %+v ok=%v, want %s", record, ok, tc.wantState)
			}
			if tc.wantRetry != (record.NextRetryAt != "") {
				t.Fatalf("next_retry_at = %q, want set=%v", record.NextRetryAt, tc.wantRetry)
			}
		})
	}
}

// TestLicenseImportClaimTimeoutFallsBackToOffline: a claim that times out is a
// manual import the hub did not confirm, so the license is installed and the
// device is reported later.
func TestLicenseImportClaimTimeoutFallsBackToOffline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	s, key, _ := newLicenseCloudServer(t, srv.URL, "")
	// Register after the temp dir so this cleanup runs first (LIFO): the
	// blocked handler must be released and the server closed before t.TempDir
	// removes the directory.
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	data := licensetest.Encode(t, key, licensetest.Payload(time.Now()))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, licenseJSONRequest(http.MethodPut, "/api/license", data).WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	record, ok := s.license.Activations().Load()
	if !ok || record.Status != license.ActivationPending {
		t.Fatalf("activation = %+v ok=%v, want pending", record, ok)
	}
	var body api.LicenseState
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OfflineImport {
		t.Fatal("a timed-out claim must mark the response as an offline import")
	}
}

// TestLicenseCloudImportClaimTimeoutFails: the automatic import never degrades
// to offline, so a timed-out claim installs nothing.
func TestLicenseCloudImportClaimTimeoutFails(t *testing.T) {
	now := time.Now()
	key := licensetest.NewKey(t)
	_, download := licenseDownloadJSON(t, key, now)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/licenses/csglite/status":
			writeEnvelope(w, `{"has_license":true,"state":"active","expire_time":"`+now.Add(365*24*time.Hour).UTC().Format(time.RFC3339)+`","used":1,"limit":10}`)
		case "/api/v1/licenses/download/csglite":
			_, _ = w.Write([]byte(download))
		default:
			<-release
		}
	}))
	s, _, root := newLicenseCloudServer(t, srv.URL, "token")
	trustLicenseKey(t, s, key, root)
	// Register after the temp dir so this cleanup runs first (LIFO): the
	// blocked handler must be released and the server closed before t.TempDir
	// removes the directory.
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/cloud/import", nil).WithContext(ctx))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	if s.license.State().Status != license.StatusNone {
		t.Fatal("the automatic import must not install a license whose claim timed out")
	}
}

func TestLicenseCloudImportDownloadFailures(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		status   int
		body     string
		wantCode int
		wantErr  string
	}{
		{"ledger lost the license", http.StatusNotFound, `{"code":"LICENSE-ERR-5","msg":"not found"}`, http.StatusConflict, licenseCodeNoLicense},
		{"token rejected", http.StatusUnauthorized, `{"error":"unauthorized"}`, http.StatusUnauthorized, licenseCodeTokenInvalid},
		{"server error", http.StatusBadGateway, ``, http.StatusServiceUnavailable, licenseCodeUnreachable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/status") {
					writeEnvelope(w, `{"has_license":true,"state":"active","expire_time":"`+now.Add(365*24*time.Hour).UTC().Format(time.RFC3339)+`","used":1,"limit":10}`)
					return
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			s, _, _ := newLicenseCloudServer(t, srv.URL, "token")
			rec := httptest.NewRecorder()
			s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/cloud/import", nil))
			if rec.Code != tc.wantCode {
				t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
			}
			var body apiErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tc.wantErr {
				t.Fatalf("code = %q, want %q", body.Code, tc.wantErr)
			}
			if s.license.State().Status != license.StatusNone {
				t.Fatal("a failed download must not install a license")
			}
		})
	}
}

// TestLicenseCloudImportClaimNotFound follows 4.6.1: a 404 at claim time still
// installs the downloaded license, because the file is signed and usable.
func TestLicenseCloudImportClaimNotFound(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		body      string
		wantState license.ActivationStatus
		wantRetry bool
	}{
		{"business 404", `{"code":"LICENSE-ERR-5","msg":"not found"}`, license.ActivationUnmanaged, false},
		{"framework 404", "404 page not found", license.ActivationPending, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key := licensetest.NewKey(t)
			_, download := licenseDownloadJSON(t, key, now)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/status"):
					writeEnvelope(w, `{"has_license":true,"state":"active","expire_time":"`+now.Add(365*24*time.Hour).UTC().Format(time.RFC3339)+`","used":1,"limit":10}`)
				case strings.HasSuffix(r.URL.Path, "/download/csglite"):
					_, _ = w.Write([]byte(download))
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(tc.body))
				}
			}))
			defer srv.Close()

			s, _, root := newLicenseCloudServer(t, srv.URL, "token")
			trustLicenseKey(t, s, key, root)
			rec := httptest.NewRecorder()
			s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/cloud/import", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
			}
			if s.license.State().Status != license.StatusValid {
				t.Fatalf("license state = %s, want valid", s.license.State().Status)
			}
			record, ok := s.license.Activations().Load()
			if !ok || record.Status != tc.wantState {
				t.Fatalf("activation = %+v ok=%v, want %s", record, ok, tc.wantState)
			}
			if tc.wantRetry != (record.NextRetryAt != "") {
				t.Fatalf("next_retry_at = %q, want set=%v", record.NextRetryAt, tc.wantRetry)
			}
		})
	}
}

// TestReportLicenseActivationSendsRawPEM covers the env-managed path: the
// payload alone is not enough, the hub verifies the signature over the PEM.
func TestReportLicenseActivationSendsRawPEM(t *testing.T) {
	type activationRequest struct {
		License string `json:"license"`
		Mode    string `json:"mode"`
	}
	received := make(chan activationRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req activationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding activation request: %v", err)
		}
		received <- req
		writeEnvelope(w, `{"recorded":true,"used":1,"limit":10}`)
	}))
	defer srv.Close()

	s := newTestServer(t)
	key := licensetest.NewKey(t)
	data := licensetest.Encode(t, key, licensetest.Payload(time.Now()))
	root := t.TempDir()
	s.license = license.NewManager(license.Options{
		Inline:         data,
		PublicKeys:     []*rsa.PublicKey{&key.PublicKey},
		Version:        s.version,
		Catalog:        gatedTestCatalog(),
		ActivationPath: filepath.Join(root, "license.activation"),
	})
	s.license.Refresh()
	s.cfg.ServerURL = srv.URL

	s.reportLicenseActivation(t.Context())

	select {
	case req := <-received:
		if strings.TrimSpace(req.License) != strings.TrimSpace(data) {
			t.Fatal("the report must carry the original PEM")
		}
		if req.Mode != "report" {
			t.Fatalf("mode = %q, want report", req.Mode)
		}
	default:
		t.Fatal("no activation request was sent")
	}
	record, ok := s.license.Activations().Load()
	if !ok || record.Status != license.ActivationReported {
		t.Fatalf("activation = %+v ok=%v, want reported", record, ok)
	}
	if _, err := os.Stat(filepath.Join(root, license.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an env-managed license must not be written to disk: %v", err)
	}
}

func TestLicenseCloudCacheInvalidatedByLicenseRemoval(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeEnvelope(w, `{"has_license":true,"state":"active","license_key":"test-license-key","used":1,"limit":10}`)
	}))
	defer srv.Close()

	s, key, _ := newLicenseCloudServer(t, srv.URL, "token")
	if _, err := s.license.Install(licensetest.Encode(t, key, licensetest.Payload(time.Now()))); err != nil {
		t.Fatal(err)
	}
	s.licenseCloudStatus(t.Context())
	if got := calls.Load(); got != 1 {
		t.Fatalf("hub calls = %d, want 1", got)
	}
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/license", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	s.licenseCloudStatus(t.Context())
	if got := calls.Load(); got != 2 {
		t.Fatalf("hub calls after removal = %d, want 2 (the cache must be invalidated)", got)
	}
}

func TestLicenseImportAcceptsGracePeriodLicense(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, `{"recorded":true,"used":1,"limit":10}`)
	}))
	defer srv.Close()

	s, key, _ := newLicenseCloudServer(t, srv.URL, "")
	now := time.Now()
	payload := licensetest.Payload(now)
	payload.StartTime = now.Add(-30 * 24 * time.Hour)
	payload.ExpireTime = now.Add(-7 * 24 * time.Hour)
	data := licensetest.Encode(t, key, payload)

	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, licenseJSONRequest(http.MethodPut, "/api/license", data))
	if rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	if state := s.license.State().Status; state != license.StatusGrace {
		t.Fatalf("license state = %s, want grace", state)
	}
}
