package license

import (
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
	"time"
)

// csghubGoldenPublicKey and csghubGoldenLicense are copied verbatim from
// starhub-server/builder/rsa/keys_test.go. They pin CSGLite's decoder to the
// exact bytes the CSGHub issuer produces; if this test breaks, the two
// products no longer agree on the license format.
const csghubGoldenPublicKey = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAntSWxxZr+qhshYAdx1II
gpAbXXIzVF8FMIoGU5NEVM4ws7CB4uEj61Mnf9AwwHzsrshmTAFFWy+3IrIPHhcF
gs4cS+UFaZSy9VoeG6jsAPmE3Za4NsFU6WOGKAvB0KixnitEIt5aCtTg4J2OxuSt
8V3McgfI3I4Ef7p4CbLmMg6DqycdCHQ8e09iez3P4q28dgJdTXuHYTb89F0ykOzj
KclUoIk0m8IS3yfLuRaOw6cUaT1rOXKytfzDWsqjm/VsipQGOKQppC8IH/GUBhGc
1E+5lOwW16HjEO5hreY2+baLFqm+dM8J3/1G3LCfU4sTmMoiV3+eq+toIOk2itBF
rwIDAQAB
-----END PUBLIC KEY-----`

const csghubGoldenLicense = `-----BEGIN LICENSE KEY-----
L/+FAwEBB1JTQUluZm8B/4YAAQIBB1BheWxvYWQBCgABCVNpZ25hdHVyZQEKAAAA
/gJo/4YB/gFdLX8DAQEKUlNBUGF5bG9hZAH/gAABAgEDS2V5AQwAAQhEYXRhQm9k
eQH/ggAAAP+F/4EDAQEIRGF0YUJvZHkB/4IAAQkBB0NvbXBhbnkBDAABBUVtYWls
AQwAAQdQcm9kdWN0AQwAAQdFZGl0aW9uAQwAAQdNYXhVc2VyAQQAAQlTdGFydFRp
bWUB/4QAAQpFeHBpcmVUaW1lAf+EAAEFRXh0cmEBDAABB1ZlcnNpb24BDAAAABD/
gwUBAQRUaW1lAf+EAAAA/5X/gAEkZTUxOWZhNWEtNzZmZC00NTA2LWFlOTktODc2
ZjQxZjZiZTQ5AQEHb3BlbmNzZwESd2FuZ2hoMjAwM0AxNjMuY29tAQZDU0dIdWIB
CkVudGVycHJpc2UBZAEPAQAAAA7evWPEAAAAAP//AQ8BAAAADt7k8MQAAAAA//8B
E3sidG9rZW5fbGltaXQiOiAxMH0AAAH+AQCLedVGlnShSBdGWwsX5c8dcKikTB1w
XTScIiOZImcmWihDEH/WfvK0gAUVtCz0Hebuux5MVL4mQwCyBkAYN4E3nz2f3qpV
YxQVI5IXxDOBqO15pmcOfXIeUppYNG4zIoiSLA7T47BYMW1R1Z+SyllSQ+Dy2qjx
JFmoVy0oCzfEZGIi5iOGulppIKAN6EXMaZq9KZ9ns4+0Njjiep6Uuhx0GUm3UIaN
ujqKwWJ0SWImnZmqcuETIw17v3nykrs3j7YwK1HP3vwi1Mrr1xiY7g6QDuj4GrFK
mvbMe42afoH/Wj+s/lLA4T6nAfOrzuxcO6vXBZrLBhKYHp2uqyAqM3wHAA==
-----END LICENSE KEY-----`

func TestDecodeCSGHubGoldenVector(t *testing.T) {
	key, err := ParsePublicKey([]byte(csghubGoldenPublicKey))
	if err != nil {
		t.Fatalf("parse golden public key: %v", err)
	}
	payload, err := Decode(csghubGoldenLicense, []*rsa.PublicKey{key})
	if err != nil {
		t.Fatalf("decode golden license: %v", err)
	}
	want := RSAPayload{
		Key: "e519fa5a-76fd-4506-ae99-876f41f6be49",
		DataBody: DataBody{
			Company:    "opencsg",
			Email:      "wanghh2003@163.com",
			Product:    "CSGHub",
			Edition:    "Enterprise",
			MaxUser:    50,
			StartTime:  time.Date(2024, 11, 6, 13, 19, 0, 0, time.UTC),
			ExpireTime: time.Date(2024, 12, 6, 13, 19, 0, 0, time.UTC),
			Extra:      `{"token_limit": 10}`,
		},
	}
	if payload.Key != want.Key || payload.Company != want.Company || payload.Email != want.Email ||
		payload.Product != want.Product || payload.Edition != want.Edition || payload.MaxUser != want.MaxUser ||
		payload.Extra != want.Extra || payload.Version != want.Version ||
		!payload.StartTime.Equal(want.StartTime) || !payload.ExpireTime.Equal(want.ExpireTime) {
		t.Fatalf("decoded payload mismatch:\n got %+v\nwant %+v", *payload, want)
	}
}

func TestDecodeToleratesWhitespaceAndCRLF(t *testing.T) {
	key, _ := ParsePublicKey([]byte(csghubGoldenPublicKey))
	crlf := "  \n" + strings.ReplaceAll(csghubGoldenLicense, "\n", "\r\n") + "\r\n\n "
	if _, err := Decode(crlf, []*rsa.PublicKey{key}); err != nil {
		t.Fatalf("decode with CRLF and padding: %v", err)
	}
}

func TestDecodeRejectsWrongKeyTamperingAndGarbage(t *testing.T) {
	golden, _ := ParsePublicKey([]byte(csghubGoldenPublicKey))
	other, _ := ParsePublicKey([]byte(csghubEnterprisePublicKeyPEM))

	if _, err := Decode(csghubGoldenLicense, []*rsa.PublicKey{other}); !errors.Is(err, ErrSignature) {
		t.Fatalf("wrong key: got %v, want ErrSignature", err)
	}
	if _, err := Decode(csghubGoldenLicense, []*rsa.PublicKey{other, golden}); err != nil {
		t.Fatalf("second key should verify: %v", err)
	}
	if _, err := Decode(csghubGoldenLicense, nil); !errors.Is(err, ErrNoPublicKey) {
		t.Fatalf("no keys: got %v, want ErrNoPublicKey", err)
	}

	lines := strings.Split(csghubGoldenLicense, "\n")
	// Flip one base64 character deep in the body (the signed payload area).
	mid := lines[len(lines)/2]
	if mid[10] == 'A' {
		mid = mid[:10] + "B" + mid[11:]
	} else {
		mid = mid[:10] + "A" + mid[11:]
	}
	lines[len(lines)/2] = mid
	tampered := strings.Join(lines, "\n")
	if _, err := Decode(tampered, []*rsa.PublicKey{golden}); err == nil {
		t.Fatal("tampered license verified")
	}

	for _, bad := range []string{"", "hello", PEMHeader + "\n" + PEMFooter, PEMHeader + "\n!!!\n" + PEMFooter} {
		if _, err := Decode(bad, []*rsa.PublicKey{golden}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("%q: got %v, want ErrMalformed", bad, err)
		}
	}
}

func TestEmbeddedPublicKeysParse(t *testing.T) {
	keys := EmbeddedPublicKeys()
	if len(keys) == 0 {
		t.Fatal("no embedded public keys")
	}
	for i, key := range keys {
		if key.N.BitLen() < 2048 {
			t.Fatalf("embedded key %d is only %d bits", i, key.N.BitLen())
		}
	}
}
