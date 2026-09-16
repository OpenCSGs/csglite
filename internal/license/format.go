// Package license verifies CSGLite enterprise licenses issued by the CSGHub
// (starhub-server) license issuer and turns them into feature entitlements.
//
// CSGLite does not sign licenses. The wire format below mirrors
// starhub-server's builder/rsa package byte for byte so that one issuer serves
// both CSGHub and CSGLite: gob(RSAPayload) is signed with RSA-SHA256 PKCS#1
// v1.5, wrapped in gob(RSAInfo), base64-encoded at 64 columns and framed by a
// LICENSE KEY PEM header and footer.
package license

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/gob"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// PEMHeader and PEMFooter frame the base64 body, exactly as CSGHub emits them.
	PEMHeader = "-----BEGIN LICENSE KEY-----"
	PEMFooter = "-----END LICENSE KEY-----"

	// ProductCSGLite is the DataBody.Product value the CSGHub issuer must set
	// for a license to be accepted by CSGLite.
	ProductCSGLite = "CSGLite"
	// EditionEnterprise unlocks every catalog feature by default.
	EditionEnterprise = "Enterprise"
	// EditionCommunity is the edition reported when no valid license is present.
	EditionCommunity = "Community"
)

var (
	ErrNoPublicKey = errors.New("no license public key configured")
	ErrMalformed   = errors.New("license data is not in LICENSE KEY PEM format")
	ErrSignature   = errors.New("license signature verification failed")
)

// RSAPayload is the signed body. Field names and the embedded DataBody must
// stay identical to starhub-server's common/types.RSAPayload because gob
// matches struct fields by name.
type RSAPayload struct {
	Key string
	DataBody
}

// DataBody mirrors starhub-server's common/types.DataBody.
type DataBody struct {
	Company    string
	Email      string
	Product    string
	Edition    string
	MaxUser    int
	StartTime  time.Time
	ExpireTime time.Time
	Extra      string
	Version    string
}

// RSAInfo mirrors starhub-server's common/types.RSAInfo.
type RSAInfo struct {
	Payload   []byte
	Signature []byte
}

// ParsePublicKey parses a PKIX "PUBLIC KEY" PEM block into an RSA public key.
func ParsePublicKey(data []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM encoded public key found")
	}
	if block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("unexpected PEM block type %q, want PUBLIC KEY", block.Type)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is %T, want RSA", parsed)
	}
	return key, nil
}

// Decode verifies data against any of keys and returns the signed payload.
// It accepts leading and trailing whitespace and either LF or CRLF line
// endings so a license pasted from a terminal or a Windows editor still
// verifies.
func Decode(data string, keys []*rsa.PublicKey) (*RSAPayload, error) {
	if len(keys) == 0 {
		return nil, ErrNoPublicKey
	}
	body, err := unwrapPEM(data)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return nil, fmt.Errorf("%w: base64: %v", ErrMalformed, err)
	}

	var info RSAInfo
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&info); err != nil {
		return nil, fmt.Errorf("%w: envelope: %v", ErrMalformed, err)
	}

	hashed := sha256.Sum256(info.Payload)
	verified := false
	for _, key := range keys {
		if key == nil {
			continue
		}
		if rsa.VerifyPKCS1v15(key, crypto.SHA256, hashed[:], info.Signature) == nil {
			verified = true
			break
		}
	}
	if !verified {
		return nil, ErrSignature
	}

	var payload RSAPayload
	if err := gob.NewDecoder(bytes.NewReader(info.Payload)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrMalformed, err)
	}
	return &payload, nil
}

func unwrapPEM(data string) (string, error) {
	text := strings.TrimSpace(strings.ReplaceAll(data, "\r\n", "\n"))
	if !strings.HasPrefix(text, PEMHeader) || !strings.HasSuffix(text, PEMFooter) {
		return "", ErrMalformed
	}
	body := text[len(PEMHeader) : len(text)-len(PEMFooter)]
	body = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', ' ', '\t':
			return -1
		}
		return r
	}, body)
	if body == "" {
		return "", ErrMalformed
	}
	return body, nil
}
