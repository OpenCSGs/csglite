package license_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/internal/license/licensetest"
)

// remoteReturning is a FetchFunc that answers with one fixed result and
// records the device ID it was asked about.
func remoteReturning(remote license.RemoteLicense, err error, gotDevice *string) license.FetchFunc {
	return func(_ context.Context, deviceID string) (license.RemoteLicense, error) {
		if gotDevice != nil {
			*gotDevice = deviceID
		}
		return remote, err
	}
}

func activeRemote(data string) license.RemoteLicense {
	return license.RemoteLicense{Status: license.RemoteStatusActive, Product: license.ProductCSGLite, License: data}
}

func TestSyncDecisions(t *testing.T) {
	key := licensetest.NewKey(t)
	encode := func(p license.RSAPayload) string { return licensetest.Encode(t, key, p) }

	current := encode(boundPayload("k1", "user-a"))
	renewedPayload := boundPayload("k1", "user-a")
	renewedPayload.ExpireTime = renewedPayload.ExpireTime.Add(365 * 24 * time.Hour)
	renewed := encode(renewedPayload)
	otherAccount := encode(boundPayload("k2", "user-b"))
	manual := encode(licensetest.Payload(testNow))
	expiredPayload := licensetest.Payload(testNow)
	expiredPayload.StartTime = testNow.Add(-400 * 24 * time.Hour)
	expiredPayload.ExpireTime = testNow.Add(-30 * 24 * time.Hour)
	expired := encode(expiredPayload)

	cases := []struct {
		name string
		// local is installed first; localAuto marks it fetched.
		local      string
		localAuto  bool
		remote     license.RemoteLicense
		fetchErr   error
		wantAction string
		wantErr    bool
		wantKey    string // license key installed afterwards; "" = none
		wantOrigin string
	}{
		{name: "nothing installed takes the issuer's license", remote: activeRemote(current), wantAction: license.SyncInstalled, wantKey: "k1", wantOrigin: license.OriginAuto},
		{name: "same license is left alone", local: current, localAuto: true, remote: activeRemote(current), wantAction: license.SyncUnchanged, wantKey: "k1", wantOrigin: license.OriginAuto},
		{name: "renewal replaces the fetched license", local: current, localAuto: true, remote: activeRemote(renewed), wantAction: license.SyncInstalled, wantKey: "k1", wantOrigin: license.OriginAuto},
		{name: "a downloaded license imported by hand is renewed", local: current, remote: activeRemote(renewed), wantAction: license.SyncInstalled, wantKey: "k1", wantOrigin: license.OriginAuto},
		{name: "a downloaded license imported by hand is removed once revoked", local: current, remote: license.RemoteLicense{Status: license.RemoteStatusRevoked}, wantAction: license.SyncRemoved},
		{name: "manual license is never replaced", local: manual, remote: activeRemote(current), wantAction: license.SyncUnchanged, wantKey: "test-license-key", wantOrigin: license.OriginManual},
		{name: "another account's fetched license is kept", local: current, localAuto: true, remote: activeRemote(otherAccount), wantAction: license.SyncUnchanged, wantKey: "k1", wantOrigin: license.OriginAuto},
		{name: "network failure keeps the local license", local: current, localAuto: true, fetchErr: errors.New("connection refused"), wantAction: license.SyncUnchanged, wantErr: true, wantKey: "k1", wantOrigin: license.OriginAuto},
		{name: "not signed in is a no-op", local: current, localAuto: true, fetchErr: license.ErrNoCredentials, wantAction: license.SyncSkipped, wantKey: "k1", wantOrigin: license.OriginAuto},
		{name: "revoked removes a fetched license", local: current, localAuto: true, remote: license.RemoteLicense{Status: license.RemoteStatusRevoked}, wantAction: license.SyncRemoved},
		{name: "revoked keeps a manual license", local: manual, remote: license.RemoteLicense{Status: license.RemoteStatusRevoked}, wantAction: license.SyncUnchanged, wantKey: "test-license-key", wantOrigin: license.OriginManual},
		{name: "none changes nothing", local: current, localAuto: true, remote: license.RemoteLicense{Status: license.RemoteStatusNone}, wantAction: license.SyncUnchanged, wantKey: "k1", wantOrigin: license.OriginAuto},
		{name: "another product's license is refused", remote: license.RemoteLicense{Status: license.RemoteStatusActive, Product: "CSGHub", License: current}, wantAction: license.SyncUnchanged, wantErr: true},
		{name: "an active answer without a product is refused", remote: license.RemoteLicense{Status: license.RemoteStatusActive, License: current}, wantAction: license.SyncUnchanged, wantErr: true},
		{name: "unknown status is an error", local: current, localAuto: true, remote: license.RemoteLicense{Status: "frozen"}, wantAction: license.SyncUnchanged, wantErr: true, wantKey: "k1", wantOrigin: license.OriginAuto},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newBoundManager(t, key, nil)
			if tc.local != "" {
				install := m.Install
				if tc.localAuto {
					install = m.InstallAuto
				}
				if _, err := install(tc.local); err != nil {
					t.Fatalf("installing local license: %v", err)
				}
			}
			var gotDevice string
			s := license.NewSyncer(m, remoteReturning(tc.remote, tc.fetchErr, &gotDevice))
			result, err := s.Sync(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if result.Action != tc.wantAction {
				t.Fatalf("action = %q (%s), want %q", result.Action, result.Reason, tc.wantAction)
			}
			st := m.State()
			gotKey := ""
			if st.Payload != nil {
				gotKey = st.Payload.Key
			}
			if gotKey != tc.wantKey || st.Origin != tc.wantOrigin {
				t.Fatalf("installed key/origin = %q/%q, want %q/%q", gotKey, st.Origin, tc.wantKey, tc.wantOrigin)
			}
			if tc.fetchErr == nil && gotDevice != testDeviceID {
				t.Fatalf("fetched for device %q, want %q", gotDevice, testDeviceID)
			}
		})
	}

	t.Run("an expired manual license is replaced", func(t *testing.T) {
		m, root := newBoundManager(t, key, nil)
		// Install refuses an expired license, so write the file directly.
		writeLicenseFile(t, root, expired)
		if st := m.Refresh(); st.Status != license.StatusExpired {
			t.Fatalf("local status = %s", st.Status)
		}
		result, err := license.NewSyncer(m, remoteReturning(activeRemote(current), nil, nil)).Sync(context.Background())
		if err != nil || result.Action != license.SyncInstalled {
			t.Fatalf("action %q, err %v", result.Action, err)
		}
	})
}

func TestSyncSkipsEnvironmentLicense(t *testing.T) {
	key := licensetest.NewKey(t)
	data := licensetest.Encode(t, key, licensetest.Payload(testNow))
	m, _ := newBoundManager(t, key, func(o *license.Options) { o.Inline = data })
	called := false
	s := license.NewSyncer(m, func(context.Context, string) (license.RemoteLicense, error) {
		called = true
		return license.RemoteLicense{}, nil
	})
	result, err := s.Sync(context.Background())
	if err != nil || result.Action != license.SyncSkipped || called {
		t.Fatalf("action %q, err %v, fetched %v", result.Action, err, called)
	}

	var nilSyncer *license.Syncer
	if result, err := nilSyncer.Sync(context.Background()); err != nil || result.Action != license.SyncSkipped {
		t.Fatalf("nil syncer: %q, %v", result.Action, err)
	}
}

func TestSyncRemembersDeviceCount(t *testing.T) {
	key := licensetest.NewKey(t)
	m, _ := newBoundManager(t, key, nil)
	remote := activeRemote(licensetest.Encode(t, key, boundPayload("k1", "user-a")))
	remote.DeviceCount = 3
	var fetchErr error
	s := license.NewSyncer(m, func(context.Context, string) (license.RemoteLicense, error) { return remote, fetchErr })

	if s.DeviceCount() != 0 {
		t.Fatalf("count before any sync = %d", s.DeviceCount())
	}
	if _, err := s.Sync(context.Background()); err != nil || s.DeviceCount() != 3 {
		t.Fatalf("count after sync = %d, err %v", s.DeviceCount(), err)
	}
	fetchErr = errors.New("connection refused")
	if _, err := s.Sync(context.Background()); err == nil || s.DeviceCount() != 3 {
		t.Fatalf("a failed sync changed the count to %d", s.DeviceCount())
	}
	fetchErr = license.ErrNoCredentials
	if _, _ = s.Sync(context.Background()); s.DeviceCount() != 0 {
		t.Fatalf("count after sign-out = %d", s.DeviceCount())
	}
	var nilSyncer *license.Syncer
	if nilSyncer.DeviceCount() != 0 {
		t.Fatal("a nil syncer reported devices")
	}
}
