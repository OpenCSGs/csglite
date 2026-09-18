// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	identityFile = "identity.json"
	nodeKeyFile  = "node.key"
	nodeCertFile = "node.crt"
	// certValidity is deliberately long: the certificate is an identity, not
	// a credential that rotates. Peers pin its fingerprint by node UUID and
	// never consult a CA or the validity window beyond NotBefore.
	certValidity = 10 * 365 * 24 * time.Hour
)

// Identity is what makes a node the same node across restarts and address
// changes: a UUID minted once, a key pair and a self-signed certificate whose
// fingerprint peers pin against that UUID.
type Identity struct {
	UUID      string    `json:"uuid"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`

	cert        tls.Certificate
	leaf        *x509.Certificate
	fingerprint string
	dir         string
}

// Fingerprint is the SHA-256 of the DER certificate, formatted "sha256:<hex>".
func (id *Identity) Fingerprint() string { return id.fingerprint }

// Certificate returns the TLS certificate this node presents to peers.
func (id *Identity) Certificate() tls.Certificate { return id.cert }

// CertPEM returns the PEM-encoded leaf certificate for sharing with peers.
func (id *Identity) CertPEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: id.leaf.Raw}))
}

// LoadOrCreateIdentity reads the identity under dir or mints a new one. A
// missing or unreadable file set is regenerated as a whole so a half-written
// identity never leaves the node with a UUID that does not match its key.
func LoadOrCreateIdentity(dir, defaultName string) (*Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating cluster directory: %w", err)
	}
	id, err := loadIdentity(dir)
	if err == nil {
		if strings.TrimSpace(id.Name) == "" {
			id.Name = defaultName
		}
		return id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return createIdentity(dir, defaultName)
}

func loadIdentity(dir string) (*Identity, error) {
	raw, err := os.ReadFile(filepath.Join(dir, identityFile))
	if err != nil {
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", identityFile, err)
	}
	if _, err := uuid.Parse(id.UUID); err != nil {
		return nil, fmt.Errorf("parsing %s: invalid uuid %q", identityFile, id.UUID)
	}
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, nodeCertFile), filepath.Join(dir, nodeKeyFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("loading node certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parsing node certificate: %w", err)
	}
	if got := uuidFromCert(leaf); got != id.UUID {
		return nil, fmt.Errorf("node certificate belongs to %q, identity file says %q", got, id.UUID)
	}
	id.cert = cert
	id.leaf = leaf
	id.fingerprint = CertFingerprint(leaf)
	id.dir = dir
	return &id, nil
}

func createIdentity(dir, name string) (*Identity, error) {
	nodeUUID := uuid.NewString()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating node key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "csglite-node-" + nodeUUID, Organization: []string{"CSGLite"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		URIs:                  []*url.URL{nodeURI(nodeUUID)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("creating node certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := writeFileAtomic(filepath.Join(dir, nodeKeyFile), keyPEM, 0o600); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(filepath.Join(dir, nodeCertFile), certPEM, 0o600); err != nil {
		return nil, err
	}
	id := &Identity{UUID: nodeUUID, Name: name, CreatedAt: now.UTC()}
	raw, _ := json.MarshalIndent(id, "", "  ")
	if err := writeFileAtomic(filepath.Join(dir, identityFile), raw, 0o600); err != nil {
		return nil, err
	}
	return loadIdentity(dir)
}

// SaveName persists a new display name for this node.
func (id *Identity) SaveName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("node name cannot be empty")
	}
	id.Name = name
	raw, _ := json.MarshalIndent(id, "", "  ")
	return writeFileAtomic(filepath.Join(id.dir, identityFile), raw, 0o600)
}

func nodeURI(nodeUUID string) *url.URL {
	return &url.URL{Scheme: "urn", Opaque: "csglite:node:" + nodeUUID}
}

func uuidFromCert(cert *x509.Certificate) string {
	for _, u := range cert.URIs {
		if u.Scheme == "urn" && strings.HasPrefix(u.Opaque, "csglite:node:") {
			return strings.TrimPrefix(u.Opaque, "csglite:node:")
		}
	}
	return ""
}

// CertFingerprint formats the SHA-256 fingerprint peers pin.
func CertFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ParseCertPEM decodes a peer's PEM certificate and confirms it names the
// expected node UUID, so a peer cannot present another node's certificate
// under its own identity.
func ParseCertPEM(certPEM, wantUUID string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("certificate is not PEM encoded")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing certificate: %w", err)
	}
	if got := uuidFromCert(cert); got != wantUUID {
		return nil, fmt.Errorf("certificate is issued to node %q, not %q", got, wantUUID)
	}
	return cert, nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
