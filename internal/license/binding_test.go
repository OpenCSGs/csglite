package license_test

import (
	"crypto/rsa"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/internal/license/licensetest"
)

const testDeviceID = "0123456789abcdef0123456789abcdef"

// boundPayload is an Enterprise payload bound to owner, as the CSGHub license
// store issues it.
func boundPayload(key, owner string) license.RSAPayload {
	p := licensetest.Payload(testNow)
	p.Key = key
	p.Extra = `{"binding":{"owner":"` + owner + `"}}`
	return p
}

// newBoundManager is newManager with a device ID and an origin marker.
func newBoundManager(t *testing.T, key *rsa.PrivateKey, mutate func(*license.Options)) (*license.Manager, string) {
	t.Helper()
	return newManager(t, key, func(o *license.Options) {
		o.DeviceID = testDeviceID
		o.OriginPath = filepath.Join(filepath.Dir(o.FilePath), "license.origin")
		if mutate != nil {
			mutate(o)
		}
	})
}

func TestParseExtraReadsBindingWithoutWarning(t *testing.T) {
	extra, warnings, err := license.ParseExtra(`{"binding":{"owner":"u-1"},"other":1}`, license.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	if extra.Binding == nil || extra.Binding.Owner != "u-1" {
		t.Fatalf("binding = %+v", extra.Binding)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"other"`) {
		t.Fatalf("warnings = %v, want only the unknown field", warnings)
	}
	if _, _, err := license.ParseExtra(`{"binding":"d-1"}`, license.Catalog()); err == nil {
		t.Fatal("a non-object binding was accepted")
	}
	if extra, _, err := license.ParseExtra(`{"binding":null}`, license.Catalog()); err != nil || extra.Binding != nil {
		t.Fatalf("null binding: %+v, %v", extra.Binding, err)
	}
}

// Licenses are not tied to an installation, so a license file works offline on
// any CSGLite, even one carrying another device's ID or none at all.
func TestBoundLicenseVerifiesOnAnyInstallation(t *testing.T) {
	key := licensetest.NewKey(t)
	m, _ := newBoundManager(t, key, nil)

	st := m.Verify(licensetest.Encode(t, key, boundPayload("k1", "user-a")))
	if !st.Licensed() || st.Owner != "user-a" || !st.StoreIssued {
		t.Fatalf("bound license: %+v", st)
	}

	legacy := boundPayload("k1", "user-a")
	legacy.Extra = `{"binding":{"owner":"user-a","device_id":"another-device"}}`
	if st := m.Verify(licensetest.Encode(t, key, legacy)); !st.Licensed() || len(st.Warnings) != 0 {
		t.Fatalf("license naming another device: %s %s %v", st.Status, st.Reason, st.Warnings)
	}
	noDevice, _ := newManager(t, key, nil)
	if st := noDevice.Verify(licensetest.Encode(t, key, boundPayload("k1", "user-a"))); !st.Licensed() {
		t.Fatalf("bound license without a local device ID: %s %s", st.Status, st.Reason)
	}

	if st := m.Verify(licensetest.Encode(t, key, licensetest.Payload(testNow))); !st.Licensed() || st.StoreIssued {
		t.Fatalf("unbound license: %s %s, store issued %v", st.Status, st.Reason, st.StoreIssued)
	}
}

func TestInstallRecordsOrigin(t *testing.T) {
	key := licensetest.NewKey(t)
	m, _ := newBoundManager(t, key, nil)
	data := licensetest.Encode(t, key, boundPayload("k1", "user-a"))

	st, err := m.InstallAuto(data)
	if err != nil {
		t.Fatal(err)
	}
	if st.Origin != license.OriginAuto {
		t.Fatalf("origin after InstallAuto = %q", st.Origin)
	}
	if got := m.Refresh().Origin; got != license.OriginAuto {
		t.Fatalf("origin after refresh = %q", got)
	}
	api := st.APIState(m.FilePath(), m.DeviceID())
	if api.Origin != license.OriginAuto || api.Owner != "user-a" || api.DeviceID != testDeviceID {
		t.Fatalf("API state origin/owner/device = %q/%q/%q", api.Origin, api.Owner, api.DeviceID)
	}

	unbound := licensetest.Encode(t, key, licensetest.Payload(testNow))
	st, err = m.Install(unbound)
	if err != nil {
		t.Fatal(err)
	}
	if st.Origin != license.OriginManual {
		t.Fatalf("origin after manual Install of an unbound license = %q", st.Origin)
	}

	if _, err := m.InstallAuto(data); err != nil {
		t.Fatal(err)
	}
	st, err = m.Remove()
	if err != nil {
		t.Fatal(err)
	}
	if st.Origin != "" {
		t.Fatalf("origin after Remove = %q", st.Origin)
	}
	if st, err := m.Install(unbound); err != nil || st.Origin != license.OriginManual {
		t.Fatalf("a stale auto marker survived Remove: %q, %v", st.Origin, err)
	}
}

// A license downloaded from the portal and imported by hand is the license
// store's, so sync must keep managing it.
func TestInstallOfStoreIssuedLicenseIsAutoManaged(t *testing.T) {
	key := licensetest.NewKey(t)
	m, _ := newBoundManager(t, key, nil)

	st, err := m.Install(licensetest.Encode(t, key, boundPayload("k1", "user-a")))
	if err != nil {
		t.Fatal(err)
	}
	if !st.StoreIssued || st.Origin != license.OriginAuto {
		t.Fatalf("store license installed by hand: store issued %v, origin %q", st.StoreIssued, st.Origin)
	}
}
