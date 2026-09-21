package server

import (
	"bytes"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/internal/license/licensetest"
	"github.com/opencsgs/csglite/pkg/api"
)

// installTestLicenseKeys swaps the server's manager for one that trusts a
// throwaway key pair and stores the license under the test storage root.
func installTestLicenseKeys(t *testing.T, s *Server) *rsa.PrivateKey {
	t.Helper()
	key := licensetest.NewKey(t)
	root := t.TempDir()
	s.license = license.NewManager(license.Options{
		FilePath:      filepath.Join(root, license.FileName),
		PublicKeys:    []*rsa.PublicKey{&key.PublicKey},
		Version:       s.version,
		Catalog:       gatedTestCatalog(),
		LastCheckPath: filepath.Join(root, "license.lastcheck"),
	})
	s.license.Refresh()
	return key
}

// gatedTestCatalog gates every shipped entry so the tests exercise the
// licensed path; the shipped catalog itself gates nothing yet.
func gatedTestCatalog() []license.FeatureDefinition {
	defs := license.Catalog()
	for i := range defs {
		defs[i].Gated = true
	}
	return defs
}

func gatedDef(def license.FeatureDefinition) license.FeatureDefinition {
	def.Gated = true
	return def
}

func licenseJSONRequest(method, target, data string) *http.Request {
	body, _ := json.Marshal(api.LicenseImportRequest{Data: data})
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestRequireFeatureWithoutLicenseReturnsForbidden(t *testing.T) {
	s := newTestServer(t)
	installTestLicenseKeys(t, s)

	called := false
	handler := s.requireFeature(gatedDef(license.FeatureObservability))(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/observability/requests", nil))

	if called {
		t.Fatal("gated handler ran without a license")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var body licenseErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != licenseErrorCode || body.Feature != license.FeatureObservability.Key || body.EditionRequired != license.EditionEnterprise || body.ErrorCode != 403 {
		t.Fatalf("unexpected body %+v", body)
	}
}

func TestRequireFeatureUngatedAlwaysPasses(t *testing.T) {
	s := newTestServer(t)
	installTestLicenseKeys(t, s)
	for _, srv := range []*Server{s, {version: "test"}} { // with and without a manager
		rec := httptest.NewRecorder()
		srv.requireFeature(license.FeatureObservability)(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("ungated feature refused without a license: %d", rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/license/features", nil))
	var catalog []api.LicenseFeatureDefinition
	if err := json.Unmarshal(rec.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	for _, entry := range catalog {
		if entry.Key == license.QuotaMaxClusterNodes.Key {
			// The only shipped gated entry: an integer cap, never "enabled".
			if !entry.Gated {
				t.Fatalf("cluster node cap should be gated: %+v", entry)
			}
			continue
		}
		if entry.Gated || !entry.Enabled {
			t.Fatalf("shipped catalog entry %s should be ungated and enabled: %+v", entry.Key, entry)
		}
	}
}

func TestRequireFeaturePanicsOnNonBooleanDefinition(t *testing.T) {
	s := newTestServer(t)
	defer func() {
		if recover() == nil {
			t.Fatal("requireFeature accepted an integer limit; it must panic at registration time")
		}
	}()
	s.requireFeature(license.QuotaMaxProviderPools)
}

func TestRequireFeatureNilManagerIsCommunity(t *testing.T) {
	s := newTestServer(t)
	s.license = nil
	rec := httptest.NewRecorder()
	s.requireFeature(gatedDef(license.FeatureAIApps))(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run")
	})(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if got := s.settingsResponse(); got.Edition != license.EditionCommunity || got.LicenseStatus != string(license.StatusNone) {
		t.Fatalf("settings with nil manager: %+v", got)
	}
}

func TestLicenseLifecycleOverHTTP(t *testing.T) {
	s := newTestServer(t)
	key := installTestLicenseKeys(t, s)
	handler := s.routes()
	now := time.Now()
	data := licensetest.Encode(t, key, licensetest.Payload(now))

	// Community by default.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/license", nil))
	var state api.LicenseState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET /api/license: %d %v", rec.Code, err)
	}
	if state.Status != string(license.StatusNone) || state.Edition != license.EditionCommunity || state.License != nil {
		t.Fatalf("initial state %+v", state)
	}

	// Verify does not install.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, licenseJSONRequest(http.MethodPost, "/api/license/verify", data))
	var verify api.LicenseVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &verify); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("verify: %d %v %s", rec.Code, err, rec.Body.String())
	}
	if !verify.Valid || verify.License == nil || verify.License.Company != "Test Co" || len(verify.Features) == 0 {
		t.Fatalf("verify response %+v", verify)
	}
	if s.license.State().Status != license.StatusNone {
		t.Fatal("verify must not change the installed state")
	}

	// Import.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, licenseJSONRequest(http.MethodPut, "/api/license", data))
	if rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != string(license.StatusValid) || state.Edition != license.EditionEnterprise || state.GraceUntil == nil || state.FilePath == "" {
		t.Fatalf("imported state %+v", state)
	}

	// Gated handler now passes, settings reflect the license.
	rec = httptest.NewRecorder()
	s.requireFeature(gatedDef(license.FeatureObservability))(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("gated handler after import: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	var settings api.SettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Edition != license.EditionEnterprise || settings.LicenseStatus != "valid" || settings.License == nil ||
		len(settings.Features) != 7 || len(settings.FeatureCatalog) != len(license.Catalog()) {
		t.Fatalf("settings after import: edition=%s status=%s features=%v catalog=%d",
			settings.Edition, settings.LicenseStatus, settings.Features, len(settings.FeatureCatalog))
	}
	for _, entry := range settings.FeatureCatalog {
		if !entry.Enabled {
			t.Errorf("catalog entry %s not enabled under Enterprise", entry.Key)
		}
	}

	// Features endpoint.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/license/features", nil))
	var catalog []api.LicenseFeatureDefinition
	if err := json.Unmarshal(rec.Body.Bytes(), &catalog); err != nil || len(catalog) != len(license.Catalog()) {
		t.Fatalf("features: %d %v", rec.Code, err)
	}

	// Delete.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/license", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil || state.Status != string(license.StatusNone) {
		t.Fatalf("state after delete: %+v %v", state, err)
	}
}

func TestLicenseImportRejectsBadInput(t *testing.T) {
	s := newTestServer(t)
	key := installTestLicenseKeys(t, s)
	handler := s.routes()

	cases := []struct {
		name string
		req  *http.Request
		code int
		want string
	}{
		{"empty body", httptest.NewRequest(http.MethodPut, "/api/license", strings.NewReader("")), http.StatusBadRequest, "invalid request body"},
		{"empty data", licenseJSONRequest(http.MethodPut, "/api/license", "   "), http.StatusBadRequest, "data cannot be empty"},
		{"garbage", licenseJSONRequest(http.MethodPut, "/api/license", "not a license"), http.StatusBadRequest, "license rejected"},
		{"too large", licenseJSONRequest(http.MethodPut, "/api/license", strings.Repeat("A", licenseImportMaxBytes+10)), http.StatusRequestEntityTooLarge, "too large"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, tc.req)
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: %d %s", tc.name, rec.Code, rec.Body.String())
		}
	}

	other := licensetest.NewKey(t)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, licenseJSONRequest(http.MethodPut, "/api/license", licensetest.Encode(t, other, licensetest.Payload(time.Now()))))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "signature") {
		t.Fatalf("foreign signature: %d %s", rec.Code, rec.Body.String())
	}

	p := licensetest.Payload(time.Now())
	p.Product = "CSGHub"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, licenseJSONRequest(http.MethodPut, "/api/license", licensetest.Encode(t, key, p)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "CSGHub") {
		t.Fatalf("other product: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, licenseJSONRequest(http.MethodPost, "/api/license/verify", "garbage"))
	var verify api.LicenseVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &verify); err != nil || rec.Code != http.StatusOK || verify.Valid || verify.Status != "invalid" {
		t.Fatalf("verify garbage: %d %+v %v", rec.Code, verify, err)
	}
	if s.license.State().Status != license.StatusNone {
		t.Fatal("rejected imports must leave the state untouched")
	}
}

func TestLicenseRoutesAreLocalOnly(t *testing.T) {
	s := newTestServer(t)
	installTestLicenseKeys(t, s)
	rec := httptest.NewRecorder()
	s.externalAPIRoutes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/license", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("external API exposes /api/license: %d", rec.Code)
	}
}

func TestLicenseRoutesRefuseCrossOriginBrowsers(t *testing.T) {
	s := newTestServer(t)
	key := installTestLicenseKeys(t, s)
	handler := s.routes()
	if _, err := s.license.Install(licensetest.Encode(t, key, licensetest.Payload(time.Now()))); err != nil {
		t.Fatal(err)
	}

	// A page on another origin must not be able to read the customer name and
	// license ID, nor delete the license.
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/license", nil)
		req.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s from a foreign origin: %d, want 403", method, rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("license route advertises CORS origin %q", got)
		}
	}
	if s.license.State().Status != license.StatusValid {
		t.Fatal("a refused cross-origin DELETE must not touch the installed license")
	}

	// The same-origin web UI and non-browser callers still work.
	sameOrigin := httptest.NewRequest(http.MethodGet, "/api/license", nil)
	sameOrigin.Header.Set("Origin", "http://"+sameOrigin.Host)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, sameOrigin)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-origin GET: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/license", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("no-Origin GET: %d", rec.Code)
	}

	// Other local API routes keep their existing wildcard CORS behaviour.
	other := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	other.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, other)
	if rec.Code != http.StatusOK || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("unrelated route changed: %d %q", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestLicenseImportFromEnvIsAConflictNotAServerError(t *testing.T) {
	s := newTestServer(t)
	key := licensetest.NewKey(t)
	data := licensetest.Encode(t, key, licensetest.Payload(time.Now()))
	root := t.TempDir()
	s.license = license.NewManager(license.Options{
		FilePath:   filepath.Join(root, license.FileName),
		PublicKeys: []*rsa.PublicKey{&key.PublicKey},
		Inline:     data,
		Catalog:    gatedTestCatalog(),
	})
	s.license.Refresh()
	handler := s.routes()

	for _, req := range []*http.Request{
		licenseJSONRequest(http.MethodPut, "/api/license", data),
		httptest.NewRequest(http.MethodDelete, "/api/license", nil),
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s /api/license = %d, want 409: %s", req.Method, rec.Code, rec.Body.String())
		}
	}
}
