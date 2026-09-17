// Package licensetest signs throwaway licenses for tests. It reproduces
// starhub-server's builder/rsa.GenerateData so tests exercise the exact wire
// format the CSGHub issuer produces. It must never be used to issue licenses.
package licensetest

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/gob"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/license"
)

// NewKey generates a 2048-bit RSA key pair for one test.
func NewKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	return key
}

// Encode signs payload with key and frames it like the CSGHub issuer.
func Encode(t testing.TB, key *rsa.PrivateKey, payload license.RSAPayload) string {
	t.Helper()
	var payloadBuf bytes.Buffer
	if err := gob.NewEncoder(&payloadBuf).Encode(&payload); err != nil {
		t.Fatalf("encoding payload: %v", err)
	}
	hashed := sha256.Sum256(payloadBuf.Bytes())
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("signing payload: %v", err)
	}
	var dataBuf bytes.Buffer
	if err := gob.NewEncoder(&dataBuf).Encode(license.RSAInfo{Payload: payloadBuf.Bytes(), Signature: signature}); err != nil {
		t.Fatalf("encoding envelope: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(dataBuf.Bytes())
	out := license.PEMHeader + "\n"
	for i := 0; i < len(b64); i += 64 {
		end := i + 64
		if end > len(b64) {
			end = len(b64)
		}
		out += b64[i:end] + "\n"
	}
	return out + license.PEMFooter
}

// Payload returns an Enterprise CSGLite payload valid for a year around now.
func Payload(now time.Time) license.RSAPayload {
	return license.RSAPayload{
		Key: "test-license-key",
		DataBody: license.DataBody{
			Company:    "Test Co",
			Email:      "ops@example.com",
			Product:    license.ProductCSGLite,
			Edition:    license.EditionEnterprise,
			MaxUser:    10,
			StartTime:  now.Add(-24 * time.Hour),
			ExpireTime: now.Add(365 * 24 * time.Hour),
		},
	}
}
