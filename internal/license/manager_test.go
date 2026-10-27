package license_test

import (
	"crypto/rsa"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/internal/license/licensetest"
)

var testNow = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

// gated returns def marked enterprise-only, so tests exercise the licensed
// path even while the shipped catalog has nothing gated.
func gated(def license.FeatureDefinition) license.FeatureDefinition {
	def.Gated = true
	return def
}

// gatedCatalog is the shipped catalog with every entry gated.
func gatedCatalog() []license.FeatureDefinition {
	defs := license.Catalog()
	for i := range defs {
		defs[i].Gated = true
	}
	return defs
}

func newManager(t *testing.T, key *rsa.PrivateKey, mutate func(*license.Options)) (*license.Manager, string) {
	t.Helper()
	root := t.TempDir()
	opts := license.Options{
		FilePath:      filepath.Join(root, license.FileName),
		PublicKeys:    []*rsa.PublicKey{&key.PublicKey},
		Version:       "0.10.3",
		Catalog:       gatedCatalog(),
		Now:           func() time.Time { return testNow },
		LastCheckPath: filepath.Join(root, "license.lastcheck"),
	}
	if mutate != nil {
		mutate(&opts)
	}
	return license.NewManager(opts), root
}

func TestCatalogIsValid(t *testing.T) {
	if err := license.ValidateCatalog(license.Catalog()); err != nil {
		t.Fatal(err)
	}
	if _, ok := license.Lookup(license.FeatureObservability.Key); !ok {
		t.Fatal("Lookup did not find a catalog entry")
	}
}

func TestValidateCatalogRejectsBadDefinitions(t *testing.T) {
	cases := map[string]license.FeatureDefinition{
		"empty key":        {Type: license.FeatureTypeBoolean, DefaultValue: true, Since: "1"},
		"wrong namespace":  {Key: "feature.audit_log", Type: license.FeatureTypeBoolean, DefaultValue: true, Since: "1"},
		"quota as feature": {Key: "quota.lite.x", Type: license.FeatureTypeBoolean, DefaultValue: true, Since: "1"},
		"bad default":      {Key: "feature.lite.x", Type: license.FeatureTypeBoolean, DefaultValue: 1, Since: "1"},
		"int default":      {Key: "quota.lite.x", Type: license.FeatureTypeInt, DefaultValue: "0", Since: "1"},
		"missing since":    {Key: "feature.lite.x", Type: license.FeatureTypeBoolean, DefaultValue: true},
		"bad type":         {Key: "feature.lite.x", Type: "string", DefaultValue: "", Since: "1"},
	}
	for name, def := range cases {
		if err := license.ValidateCatalog([]license.FeatureDefinition{def}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	dup := license.FeatureObservability
	if err := license.ValidateCatalog([]license.FeatureDefinition{dup, dup}); err == nil {
		t.Error("duplicate key accepted")
	}
}

func TestRefreshWithoutFileIsCommunity(t *testing.T) {
	m, _ := newManager(t, licensetest.NewKey(t), nil)
	st := m.Refresh()
	if st.Status != license.StatusNone || st.Licensed() || st.Edition() != license.EditionCommunity {
		t.Fatalf("unexpected state %+v", st)
	}
	if m.Enabled(gated(license.FeatureObservability)) {
		t.Fatal("gated feature enabled without a license")
	}
	if !m.Enabled(license.FeatureObservability) {
		t.Fatal("ungated feature must be enabled without a license")
	}
	var nilManager *license.Manager
	if nilManager.State().Status != license.StatusNone || nilManager.Enabled(gated(license.FeatureObservability)) {
		t.Fatal("nil manager must behave as Community")
	}
	if !nilManager.Enabled(license.FeatureObservability) || nilManager.Limit(license.QuotaMaxProviderPools) != 0 {
		t.Fatal("nil manager must keep ungated features open")
	}
}

func TestUngatedFeaturesIgnoreTheLicense(t *testing.T) {
	key := licensetest.NewKey(t)
	// Shipped catalog: the cluster node cap, and splitting one model across
	// machines. The cluster itself is open to everyone; only the number of
	// nodes and the ability to run a model that fits on none of them are sold.
	m, _ := newManager(t, key, func(o *license.Options) { o.Catalog = license.Catalog() })
	gated := map[string]bool{}
	for _, def := range license.GatedCatalog() {
		gated[def.Key] = true
	}
	want := map[string]bool{
		license.QuotaMaxClusterNodes.Key:    true,
		license.FeatureClusterModelSpan.Key: true,
	}
	if len(gated) != len(want) {
		t.Fatalf("shipped catalog gates %v; %v is expected. Update this test and the docs deliberately", gated, want)
	}
	for key := range want {
		if !gated[key] {
			t.Fatalf("%s is no longer gated. Update this test and the docs deliberately", key)
		}
	}
	st := m.Refresh()
	if st.Status != license.StatusNone {
		t.Fatalf("status %s", st.Status)
	}
	for _, def := range license.Catalog() {
		switch def.Type {
		case license.FeatureTypeBoolean:
			// A gated boolean is exactly the thing a licence buys, so it is
			// off without one; every other feature stays open.
			if st.Enabled(def) == def.Gated {
				t.Errorf("%s: enabled=%v without a licence, gated=%v", def.Key, st.Enabled(def), def.Gated)
			}
		case license.FeatureTypeInt:
			want := 0
			if def.Gated {
				want = def.CommunityValue
			}
			if st.Limit(def) != want {
				t.Errorf("%s = %d without a license, want %d", def.Key, st.Limit(def), want)
			}
		}
	}
	if got := len(st.EnabledKeys()); got != 7 {
		t.Fatalf("EnabledKeys = %d, want all 7 ungated booleans", got)
	}
	// The Community edition may cluster two machines; an Enterprise license
	// with no explicit tier lifts the cap entirely.
	if st.Limit(license.QuotaMaxClusterNodes) != 2 {
		t.Fatalf("community cluster cap = %d, want 2", st.Limit(license.QuotaMaxClusterNodes))
	}
	if ent := m.Verify(licensetest.Encode(t, key, licensetest.Payload(testNow))); ent.Status != license.StatusValid || ent.Limit(license.QuotaMaxClusterNodes) != 0 {
		t.Fatalf("enterprise cluster cap = %d (%s), want unlimited", ent.Limit(license.QuotaMaxClusterNodes), ent.Status)
	}

	// A license that explicitly disables an ungated feature has no effect on it.
	p := licensetest.Payload(testNow)
	p.Extra = `{"features": {"feature.lite.observability": false}}`
	st = m.Verify(licensetest.Encode(t, key, p))
	if st.Status != license.StatusValid || !st.Enabled(license.FeatureObservability) {
		t.Fatalf("ungated feature must stay enabled: %+v", st)
	}
}

func TestInstallEnterpriseUnlocksCatalogDefaults(t *testing.T) {
	key := licensetest.NewKey(t)
	m, root := newManager(t, key, nil)
	data := licensetest.Encode(t, key, licensetest.Payload(testNow))

	st, err := m.Install(data)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if st.Status != license.StatusValid || st.Edition() != license.EditionEnterprise || st.Source != "file" {
		t.Fatalf("unexpected state %+v", st)
	}
	for _, def := range gatedCatalog() {
		switch def.Type {
		case license.FeatureTypeBoolean:
			if !st.Enabled(def) {
				t.Errorf("%s should default on for Enterprise", def.Key)
			}
		case license.FeatureTypeInt:
			if st.Limit(def) != def.DefaultValue.(int) {
				t.Errorf("%s = %d, want default %v", def.Key, st.Limit(def), def.DefaultValue)
			}
		}
	}
	if len(st.EnabledKeys()) == 0 {
		t.Fatal("EnabledKeys empty")
	}
	if _, err := os.Stat(filepath.Join(root, license.FileName)); err != nil {
		t.Fatalf("license file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "license.lastcheck")); err != nil {
		t.Fatalf("last-check marker not written: %v", err)
	}

	// A fresh manager over the same root picks the file up.
	again := license.NewManager(license.Options{
		FilePath:   filepath.Join(root, license.FileName),
		PublicKeys: []*rsa.PublicKey{&key.PublicKey},
		Catalog:    gatedCatalog(),
		Now:        func() time.Time { return testNow },
	})
	if got := again.Refresh().Status; got != license.StatusValid {
		t.Fatalf("reload status = %s", got)
	}

	if st, err := m.Remove(); err != nil || st.Status != license.StatusNone {
		t.Fatalf("remove: %v, %+v", err, st)
	}
	if st, err := m.Remove(); err != nil || st.Status != license.StatusNone {
		t.Fatalf("second remove must be idempotent: %v, %+v", err, st)
	}
}

func TestExtraOverridesAndWarnings(t *testing.T) {
	key := licensetest.NewKey(t)
	m, _ := newManager(t, key, nil)
	p := licensetest.Payload(testNow)
	p.Extra = `{"features": {"feature.lite.observability": false, "feature.lite.future": true},
	            "limits": {"quota.lite.max_provider_pools": 3, "quota.lite.unknown": 1},
	            "vendor": {}}`
	st := m.Verify(licensetest.Encode(t, key, p))
	if st.Status != license.StatusValid {
		t.Fatalf("status %s: %s", st.Status, st.Reason)
	}
	if st.Enabled(gated(license.FeatureObservability)) {
		t.Error("explicit false must disable observability")
	}
	if !st.Enabled(gated(license.FeatureAIApps)) {
		t.Error("unmentioned feature must keep its default")
	}
	if st.Limit(gated(license.QuotaMaxProviderPools)) != 3 {
		t.Errorf("limit = %d, want 3", st.Limit(gated(license.QuotaMaxProviderPools)))
	}
	if len(st.Warnings) != 3 {
		t.Errorf("warnings = %v, want 3 (unknown field, feature, limit)", st.Warnings)
	}

	p.Extra = `{"features": {"feature.lite.observability": "yes"}}`
	if st := m.Verify(licensetest.Encode(t, key, p)); st.Status != license.StatusInvalid {
		t.Errorf("wrong-typed known key must be invalid, got %s", st.Status)
	}
	p.Extra = `{"limits": {"feature.lite.observability": 1}}`
	if st := m.Verify(licensetest.Encode(t, key, p)); st.Status != license.StatusInvalid {
		t.Errorf("boolean key under limits must be invalid, got %s", st.Status)
	}
	p.Extra = `not json`
	if st := m.Verify(licensetest.Encode(t, key, p)); st.Status != license.StatusInvalid {
		t.Errorf("malformed extra must be invalid, got %s", st.Status)
	}
}

func TestNonEnterpriseEditionOnlyGetsExplicitFeatures(t *testing.T) {
	key := licensetest.NewKey(t)
	m, _ := newManager(t, key, nil)
	p := licensetest.Payload(testNow)
	p.Edition = "Trial"
	p.Extra = `{"features": {"feature.lite.observability": true}}`
	st := m.Verify(licensetest.Encode(t, key, p))
	if st.Status != license.StatusValid || st.Edition() != "Trial" {
		t.Fatalf("unexpected %+v", st)
	}
	if !st.Enabled(gated(license.FeatureObservability)) || st.Enabled(gated(license.FeatureAIApps)) {
		t.Fatalf("features = %v", st.Features)
	}
}

func TestLifecycleStatuses(t *testing.T) {
	key := licensetest.NewKey(t)
	m, _ := newManager(t, key, nil)
	base := licensetest.Payload(testNow)

	cases := []struct {
		name       string
		start, end time.Time
		want       license.Status
		licensed   bool
	}{
		{"not started", testNow.Add(time.Hour), testNow.Add(48 * time.Hour), license.StatusNotStarted, false},
		{"valid", testNow.Add(-time.Hour), testNow.Add(time.Hour), license.StatusValid, true},
		{"valid at expiry instant", testNow.Add(-time.Hour), testNow, license.StatusValid, true},
		{"grace", testNow.Add(-48 * time.Hour), testNow.Add(-time.Hour), license.StatusGrace, true},
		{"grace last day", testNow.Add(-400 * 24 * time.Hour), testNow.Add(-license.DefaultGracePeriod), license.StatusGrace, true},
		{"expired", testNow.Add(-400 * 24 * time.Hour), testNow.Add(-license.DefaultGracePeriod - time.Second), license.StatusExpired, false},
		{"ends before start", testNow.Add(time.Hour), testNow.Add(-time.Hour), license.StatusInvalid, false},
	}
	for _, tc := range cases {
		p := base
		p.StartTime, p.ExpireTime = tc.start, tc.end
		st := m.Verify(licensetest.Encode(t, key, p))
		if st.Status != tc.want || st.Licensed() != tc.licensed {
			t.Errorf("%s: status %s licensed %v (%s), want %s/%v", tc.name, st.Status, st.Licensed(), st.Reason, tc.want, tc.licensed)
		}
	}

	expired := base
	expired.StartTime, expired.ExpireTime = testNow.Add(-400*24*time.Hour), testNow.Add(-30*24*time.Hour)
	if _, err := m.Install(licensetest.Encode(t, key, expired)); err == nil {
		t.Fatal("installing an expired license must fail")
	}
	future := base
	future.StartTime, future.ExpireTime = testNow.Add(24*time.Hour), testNow.Add(48*time.Hour)
	if st, err := m.Install(licensetest.Encode(t, key, future)); err != nil || st.Status != license.StatusNotStarted {
		t.Fatalf("not-yet-started license should install: %v %+v", err, st)
	}
}

func TestProductVersionAndSignatureChecks(t *testing.T) {
	key := licensetest.NewKey(t)
	m, _ := newManager(t, key, nil)

	p := licensetest.Payload(testNow)
	p.Product = "CSGHub"
	if st := m.Verify(licensetest.Encode(t, key, p)); st.Status != license.StatusInvalid || !strings.Contains(st.Reason, "CSGHub") {
		t.Fatalf("other product accepted: %+v", st)
	}

	p = licensetest.Payload(testNow)
	p.Version = "0.11.0"
	if st := m.Verify(licensetest.Encode(t, key, p)); st.Status != license.StatusInvalid {
		t.Fatalf("newer minimum version accepted: %+v", st)
	}
	p.Version = "v0.10.3"
	if st := m.Verify(licensetest.Encode(t, key, p)); st.Status != license.StatusValid {
		t.Fatalf("equal minimum version rejected: %s", st.Reason)
	}
	dev, _ := newManager(t, key, func(o *license.Options) { o.Version = "dev" })
	p.Version = "9.9.9"
	if st := dev.Verify(licensetest.Encode(t, key, p)); st.Status != license.StatusValid {
		t.Fatalf("dev build must ignore the version floor: %s", st.Reason)
	}

	other := licensetest.NewKey(t)
	if st := m.Verify(licensetest.Encode(t, other, licensetest.Payload(testNow))); st.Status != license.StatusInvalid {
		t.Fatalf("foreign signature accepted: %+v", st)
	}
	if _, err := m.Install("garbage"); err == nil {
		t.Fatal("installing garbage must fail")
	}
}

func TestInlineEnvOverridesFileAndBlocksInstall(t *testing.T) {
	key := licensetest.NewKey(t)
	inline := licensetest.Encode(t, key, licensetest.Payload(testNow))
	m, _ := newManager(t, key, func(o *license.Options) { o.Inline = inline })
	if st := m.Refresh(); st.Status != license.StatusValid || st.Source != "env" {
		t.Fatalf("inline license not used: %+v", st)
	}
	if _, err := m.Install(inline); err == nil {
		t.Fatal("install must be refused while the env var is set")
	}
	if _, err := m.Remove(); err == nil {
		t.Fatal("remove must be refused while the env var is set")
	}
}

func TestClockRollbackDowngradesToGrace(t *testing.T) {
	key := licensetest.NewKey(t)
	root := t.TempDir()
	path := filepath.Join(root, license.FileName)
	data := licensetest.Encode(t, key, licensetest.Payload(testNow))
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "license.lastcheck")
	if err := os.WriteFile(marker, []byte(testNow.Add(72*time.Hour).Format(time.RFC3339)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := license.NewManager(license.Options{
		FilePath:      path,
		PublicKeys:    []*rsa.PublicKey{&key.PublicKey},
		Now:           func() time.Time { return testNow },
		LastCheckPath: marker,
	})
	st := m.Refresh()
	if st.Status != license.StatusGrace || !strings.Contains(st.Reason, "clock") {
		t.Fatalf("expected grace due to clock rollback, got %+v", st)
	}
	raw, _ := os.ReadFile(marker)
	if !strings.HasPrefix(string(raw), testNow.Add(72*time.Hour).Format(time.RFC3339)) {
		t.Fatalf("marker must not move backwards, got %q", raw)
	}
}

func TestResolvePublicKeysEnvOverride(t *testing.T) {
	key := licensetest.NewKey(t)
	root := t.TempDir()
	pemPath := filepath.Join(root, "pub.pem")
	der, err := marshalPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pemPath, der, 0o600); err != nil {
		t.Fatal(err)
	}
	keys, err := license.ResolvePublicKeys(func(name string) string {
		if name == license.EnvPublicKeyFile {
			return pemPath
		}
		return ""
	})
	if err != nil || len(keys) != 1 || keys[0].N.Cmp(key.N) != 0 {
		t.Fatalf("override not applied: %v %d", err, len(keys))
	}
	if _, err := license.ResolvePublicKeys(func(string) string { return filepath.Join(root, "missing.pem") }); err == nil {
		t.Fatal("missing override file must error")
	}
	if keys, err := license.ResolvePublicKeys(func(string) string { return "" }); err != nil || len(keys) == 0 {
		t.Fatalf("embedded keys expected: %v", err)
	}
}

func TestAPIViewsMirrorState(t *testing.T) {
	key := licensetest.NewKey(t)
	m, _ := newManager(t, key, nil)

	none := m.Refresh().APIState(m.FilePath())
	if none.Status != "none" || none.Edition != license.EditionCommunity || none.License != nil || none.Limits == nil || none.FilePath != m.FilePath() {
		t.Fatalf("unlicensed view %+v", none)
	}

	p := licensetest.Payload(testNow)
	p.Extra = `{"features": {"feature.lite.observability": false}, "limits": {"quota.lite.max_provider_pools": 2}}`
	st := m.Verify(licensetest.Encode(t, key, p))
	verify := st.APIVerify()
	if !verify.Valid || verify.License == nil || verify.License.Company != p.Company || verify.Limits["quota.lite.max_provider_pools"] != 2 {
		t.Fatalf("verify view %+v", verify)
	}
	catalog := st.APICatalog(gatedCatalog())
	if len(catalog) != len(license.Catalog()) {
		t.Fatalf("catalog view has %d entries", len(catalog))
	}
	for _, entry := range catalog {
		want := entry.Key != license.FeatureObservability.Key
		if entry.Enabled != want || !entry.Gated {
			t.Errorf("%s: enabled=%v gated=%v", entry.Key, entry.Enabled, entry.Gated)
		}
	}
	full := st.APIState("")
	if full.GraceUntil == nil || len(full.Features) != 7 {
		t.Fatalf("state view %+v", full)
	}
	verify.Limits["quota.lite.max_provider_pools"] = 99
	if st.Limits["quota.lite.max_provider_pools"] != 2 {
		t.Fatal("API view must copy the limits map, not alias it")
	}
}

func TestGatedLimitFallsBackToTheCommunityCap(t *testing.T) {
	key := licensetest.NewKey(t)
	quota := license.QuotaMaxProviderPools
	quota.Gated = true
	quota.DefaultValue = 0 // unlimited for licensed customers
	quota.CommunityValue = 1
	m, _ := newManager(t, key, func(o *license.Options) {
		o.Catalog = []license.FeatureDefinition{quota}
	})

	// No license: the Community cap applies, not 0 (which means unlimited).
	if got := m.Refresh().Limit(quota); got != 1 {
		t.Fatalf("unlicensed limit = %d, want the Community cap 1", got)
	}
	// Enterprise with no override: the catalog default, here unlimited.
	if got := m.Verify(licensetest.Encode(t, key, licensetest.Payload(testNow))).Limit(quota); got != 0 {
		t.Fatalf("licensed limit = %d, want 0 (unlimited)", got)
	}
	// Enterprise with an explicit cap.
	p := licensetest.Payload(testNow)
	p.Extra = `{"limits": {"quota.lite.max_provider_pools": 10}}`
	if got := m.Verify(licensetest.Encode(t, key, p)).Limit(quota); got != 10 {
		t.Fatalf("tailored limit = %d, want 10", got)
	}
}

func TestValidateCatalogRejectsGatedLimitWithoutCommunityCap(t *testing.T) {
	quota := license.QuotaMaxProviderPools
	quota.Gated = true
	if err := license.ValidateCatalog([]license.FeatureDefinition{quota}); err == nil {
		t.Fatal("a gated limit with CommunityValue 0 would leave Community users unlimited; it must be rejected")
	}
	quota.CommunityValue = 2
	if err := license.ValidateCatalog([]license.FeatureDefinition{quota}); err != nil {
		t.Fatalf("gated limit with a cap rejected: %v", err)
	}
	ungated := license.FeatureObservability
	ungated.CommunityValue = 5
	if err := license.ValidateCatalog([]license.FeatureDefinition{ungated}); err == nil {
		t.Fatal("CommunityValue on an ungated feature is never used and must be rejected")
	}
}

func TestInstallAndRemoveReportEnvManagement(t *testing.T) {
	key := licensetest.NewKey(t)
	data := licensetest.Encode(t, key, licensetest.Payload(testNow))
	m, _ := newManager(t, key, func(o *license.Options) { o.Inline = data })
	if _, err := m.Install(data); !errors.Is(err, license.ErrManagedByEnv) {
		t.Fatalf("Install error = %v, want ErrManagedByEnv", err)
	}
	if _, err := m.Remove(); !errors.Is(err, license.ErrManagedByEnv) {
		t.Fatalf("Remove error = %v, want ErrManagedByEnv", err)
	}

	noPath, _ := newManager(t, key, func(o *license.Options) { o.FilePath = "" })
	if _, err := noPath.Install(data); !errors.Is(err, license.ErrNoFilePath) {
		t.Fatalf("Install error = %v, want ErrNoFilePath", err)
	}
}
