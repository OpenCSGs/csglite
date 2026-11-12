package cli

import (
	"path/filepath"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/license"
)

// TestRecordPendingActivationAfterOfflineInstall covers the CLI path that has
// no running server: the license is installed and the device is reported on
// the next online start.
func TestRecordPendingActivationAfterOfflineInstall(t *testing.T) {
	root := t.TempDir()
	manager := license.NewManager(license.Options{
		FilePath:       filepath.Join(root, license.FileName),
		ActivationPath: filepath.Join(root, "license.activation"),
	})
	c := &licenseClient{
		cfg:     &config.Config{ModelDir: filepath.Join(root, config.ModelsDir)},
		manager: manager,
	}

	if !c.recordPendingActivation(license.State{Payload: &license.RSAPayload{Key: "test-license-key"}}) {
		t.Fatal("an offline install with no prior report must owe a report")
	}

	record, ok := manager.Activations().Load()
	if !ok {
		t.Fatal("an offline install must record the device")
	}
	if record.Status != license.ActivationPending || record.LicenseKey != "test-license-key" || record.DeviceID == "" {
		t.Fatalf("activation = %+v, want a pending record for the installed key", record)
	}
}
