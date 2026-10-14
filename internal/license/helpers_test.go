package license_test

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencsgs/csglite/internal/license"
)

func marshalPublicKey(key *rsa.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

// writeLicenseFile writes data as the license file under root, bypassing
// Install's checks.
func writeLicenseFile(t *testing.T, root, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, license.FileName), []byte(data), 0o600); err != nil {
		t.Fatalf("writing license file: %v", err)
	}
}
