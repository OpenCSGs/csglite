package license

import (
	"crypto/rsa"
	"fmt"
	"os"
	"strings"
)

// csghubEnterprisePublicKeyPEM is the CSGHub enterprise license public key
// shipped as enterprise/public_key_ee.pem in starhub-server. Licenses signed by
// the CSGHub SaaS issuer verify against it. Add a second entry (never replace)
// when the issuer rotates keys so licenses signed with the old key stay valid.
const csghubEnterprisePublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAsv9a9Zy+/VQuRLq1UYcP
JgvfES+F9zDfWThEbp7sRXVtcF0gPCFmg1lXzzAfQxtprIkPzXxrCc1YoqzGT7Eg
tMd3Z5ZRbJ2o+NWs+w/kZfA64TkmJWf9lykdPQbbPz6ZfKgI7XLfLC3h5Xl8BGQh
LvubJP5P8LnUUtHuVriAuXz/reNCvi5gh9QLNICKKkSbYXZSLmun1xAY1Jt4q+bg
ipHuPA6kTw3E3LeYVYjflsTAb+0YLJS0U9Xq0lW3mkEFiDJmUxaEllNoV0n7aahh
nB+5l0rDj5XQRMTDciug6m8hzNv8BUn4LPKbzpVfERryufStP2h/1CJVSXb5Dnul
MQIDAQAB
-----END PUBLIC KEY-----`

var embeddedPublicKeyPEMs = []string{csghubEnterprisePublicKeyPEM}

// EmbeddedPublicKeys returns the compiled-in issuer public keys.
func EmbeddedPublicKeys() []*rsa.PublicKey {
	keys := make([]*rsa.PublicKey, 0, len(embeddedPublicKeyPEMs))
	for _, pemText := range embeddedPublicKeyPEMs {
		key, err := ParsePublicKey([]byte(pemText))
		if err != nil {
			// The constants are fixed at build time; a parse failure is a
			// programming error caught by TestEmbeddedPublicKeysParse.
			panic(fmt.Sprintf("license: embedded public key is invalid: %v", err))
		}
		keys = append(keys, key)
	}
	return keys
}

// ResolvePublicKeys returns the verification keys to use. When
// CSGHUB_LITE_LICENSE_PUBLIC_KEY_FILE names a readable PEM file its key is
// used instead of the embedded ones, which lets staging issuers with their
// own key pair be exercised without a rebuild.
func ResolvePublicKeys(getenv func(string) string) ([]*rsa.PublicKey, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	path := strings.TrimSpace(getenv(EnvPublicKeyFile))
	if path == "" {
		return EmbeddedPublicKeys(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s (%s): %w", EnvPublicKeyFile, path, err)
	}
	key, err := ParsePublicKey(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s (%s): %w", EnvPublicKeyFile, path, err)
	}
	return []*rsa.PublicKey{key}, nil
}
