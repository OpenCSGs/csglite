package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/internal/license/licensetest"
	"github.com/opencsgs/csglite/pkg/api"
)

// fakeLicenseStore stands in for the CSGHub license store API.
type fakeLicenseStore struct {
	mu            sync.Mutex
	sessionStatus string
	sessionExtra  string // extra JSON fields of GET /checkout-sessions/s-1, each with a leading comma
	current       string // JSON body for GET /current
	currentCode   int
	gotDevice     string
	currentCalls  int
}

func (f *fakeLicenseStore) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"msg":"invalid token"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/license-store/checkout-sessions":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["product"] != license.ProductCSGLite {
				t.Errorf("checkout product = %q", body["product"])
			}
			f.gotDevice = body["device_id"]
			_, _ = w.Write([]byte(`{"msg":"OK","data":{"sid":"s-1","checkout_url":"https://hub.example/licenses/checkout?sid=s-1","expire_at":"2026-10-10T12:30:00Z"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/license-store/checkout-sessions/s-1":
			_, _ = w.Write([]byte(`{"msg":"OK","data":{"sid":"s-1","product":"CSGLite","status":"` + f.sessionStatus + `","owner_name":"alice","order_no":"o-1","expire_at":"2026-10-10T12:30:00Z"` + f.sessionExtra + `}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/license-store/current":
			f.currentCalls++
			f.gotDevice = r.URL.Query().Get("device_id")
			if r.URL.Query().Get("product") != license.ProductCSGLite {
				t.Errorf("current product = %q", r.URL.Query().Get("product"))
			}
			if f.currentCode != 0 {
				w.WriteHeader(f.currentCode)
			}
			_, _ = w.Write([]byte(f.current))
		default:
			t.Errorf("unexpected issuer call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// activeLicenseBody is a GET /current answer carrying data.
func activeLicenseBody(data string) string {
	body, _ := json.Marshal(map[string]any{"msg": "OK", "data": map[string]any{"status": "active", "product": "CSGLite", "license": data, "edition": "Enterprise", "device_count": 2}})
	return string(body)
}

func newLicenseCheckoutServer(t *testing.T, issuer *fakeLicenseStore, token string) (*Server, http.Handler, func(license.RSAPayload) string) {
	t.Helper()
	s := newTestServer(t)
	key := installTestLicenseKeys(t, s)
	issuerServer := httptest.NewServer(issuer.handler(t))
	t.Cleanup(issuerServer.Close)
	s.cfg.ServerURL = issuerServer.URL
	s.cfg.Token = token
	sign := func(p license.RSAPayload) string { return licensetest.Encode(t, key, p) }
	return s, s.routes(), sign
}

func boundTestPayload() license.RSAPayload {
	p := licensetest.Payload(time.Now())
	p.Key = "bought-1"
	p.Extra = `{"binding":{"owner":"user-a"}}`
	return p
}

func TestLicenseUpgradeRequiresCloudLogin(t *testing.T) {
	s, handler, _ := newLicenseCheckoutServer(t, &fakeLicenseStore{}, "")
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/license/upgrade", nil),
		httptest.NewRequest(http.MethodGet, "/api/license/upgrade/s-1", nil),
		httptest.NewRequest(http.MethodPost, "/api/license/sync", nil),
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: status %d", req.Method, req.URL.Path, rec.Code)
		}
		var resp cloudLoginRequiredResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Code != cloudLoginRequiredCode || resp.LoginURL == "" {
			t.Fatalf("%s %s: %+v", req.Method, req.URL.Path, resp)
		}
	}
	if s.license.State().Status != license.StatusNone {
		t.Fatal("license state changed without a sign-in")
	}
}

func TestLicenseUpgradeStartAndCompletion(t *testing.T) {
	issuer := &fakeLicenseStore{sessionStatus: "pending"}
	s, handler, sign := newLicenseCheckoutServer(t, issuer, "test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/upgrade", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var started api.LicenseCheckoutResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.SID != "s-1" || started.CheckoutURL != "https://hub.example/licenses/checkout?sid=s-1" || started.ExpireAt.IsZero() {
		t.Fatalf("start response %+v", started)
	}
	if issuer.gotDevice != testLicenseDeviceID {
		t.Fatalf("session created for device %q, want %q", issuer.gotDevice, testLicenseDeviceID)
	}

	// Still waiting for payment: no license fetched.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/license/upgrade/s-1", nil))
	var pending api.LicenseCheckoutStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pending); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("pending poll: %d %v", rec.Code, err)
	}
	if pending.Status != "pending" || pending.License != nil || pending.OwnerName != "alice" {
		t.Fatalf("pending poll %+v", pending)
	}

	// Paid: the poll fetches and installs the license.
	issuer.mu.Lock()
	issuer.sessionStatus = "completed"
	issuer.current = activeLicenseBody(sign(boundTestPayload()))
	issuer.mu.Unlock()
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/license/upgrade/s-1", nil))
	var done api.LicenseCheckoutStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &done); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("completed poll: %d %v", rec.Code, err)
	}
	if done.SyncError != "" || done.License == nil {
		t.Fatalf("completed poll %+v", done)
	}
	if done.License.Status != string(license.StatusValid) || done.License.Origin != license.OriginAuto || done.License.Owner != "user-a" {
		t.Fatalf("installed state %+v", done.License)
	}
	if st := s.license.State(); st.Edition() != license.EditionEnterprise {
		t.Fatalf("edition after purchase = %s", st.Edition())
	}
}

func TestLicenseCompletedPollReportsSyncError(t *testing.T) {
	issuer := &fakeLicenseStore{sessionStatus: "completed"}
	s, handler, sign := newLicenseCheckoutServer(t, issuer, "test-token")
	wrongProduct := boundTestPayload()
	wrongProduct.Product = "CSGHub"
	issuer.current = activeLicenseBody(sign(wrongProduct))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/license/upgrade/s-1", nil))
	var resp api.LicenseCheckoutStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("poll: %d %v", rec.Code, err)
	}
	if !strings.Contains(resp.SyncError, "does not verify here") {
		t.Fatalf("sync_error = %q", resp.SyncError)
	}
	if s.license.State().Status != license.StatusNone {
		t.Fatal("a license for another product was installed")
	}
}

func TestLicenseCompletedPollPaidByOtherAccount(t *testing.T) {
	issuer := &fakeLicenseStore{
		sessionStatus: "completed",
		sessionExtra:  `,"paid_by_other":true,"buyer_name":"b***b"`,
		current:       `{"msg":"OK","data":{"status":"none","product":"CSGLite"}}`,
	}
	s, handler, _ := newLicenseCheckoutServer(t, issuer, "test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/license/upgrade/s-1", nil))
	var resp api.LicenseCheckoutStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("poll: %d %v", rec.Code, err)
	}
	if !resp.PaidByOther || resp.BuyerName != "b***b" || resp.OwnerName != "alice" {
		t.Fatalf("poll response %+v", resp)
	}
	if issuer.currentCalls != 1 {
		t.Fatalf("completed poll synced %d times, want 1", issuer.currentCalls)
	}
	if s.license.State().Edition() == license.EditionEnterprise {
		t.Fatal("a license was installed for a purchase paid by another account")
	}
}

func TestLicenseSyncRoute(t *testing.T) {
	issuer := &fakeLicenseStore{}
	s, handler, sign := newLicenseCheckoutServer(t, issuer, "test-token")

	issuer.current = activeLicenseBody(sign(boundTestPayload()))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/sync", nil))
	var resp api.LicenseSyncResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("sync: %d %v %s", rec.Code, err, rec.Body.String())
	}
	if resp.Action != license.SyncInstalled || resp.License.Origin != license.OriginAuto ||
		resp.License.DeviceID != testLicenseDeviceID || resp.License.DeviceCount != 2 {
		t.Fatalf("sync response %+v", resp)
	}
	if issuer.gotDevice != testLicenseDeviceID {
		t.Fatalf("synced for device %q, want %q", issuer.gotDevice, testLicenseDeviceID)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/license", nil))
	var state api.LicenseState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil || state.DeviceCount != 2 {
		t.Fatalf("GET /api/license: %v %s", err, rec.Body.String())
	}

	// A refusal: the issuer's message reaches the caller.
	issuer.mu.Lock()
	issuer.currentCode = http.StatusForbidden
	issuer.current = `{"msg":"license store is closed"}`
	issuer.mu.Unlock()
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/sync", nil))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "license store is closed") {
		t.Fatalf("refusal: %d %s", rec.Code, rec.Body.String())
	}
	if s.license.State().Edition() != license.EditionEnterprise {
		t.Fatal("a failed sync removed the installed license")
	}

	// An expired sign-in asks for a new one.
	s.cfg.Token = "stale-token"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/license/sync", nil))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), cloudLoginRequiredCode) {
		t.Fatalf("stale token: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTriggerLicenseSyncWaitsForRun(t *testing.T) {
	issuer := &fakeLicenseStore{}
	s, _, _ := newLicenseCheckoutServer(t, issuer, "test-token")
	// Before Run there is no lifetime context, so a sign-in must not start
	// a background request.
	s.triggerLicenseSync()
	time.Sleep(50 * time.Millisecond)
	issuer.mu.Lock()
	defer issuer.mu.Unlock()
	if issuer.currentCalls != 0 {
		t.Fatal("a sync ran before the server started")
	}
}
