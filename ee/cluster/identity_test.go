// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIdentityIsMintedOnceAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreateIdentity(dir, "box-01")
	if err != nil {
		t.Fatal(err)
	}
	if first.UUID == "" || first.Fingerprint() == "" || first.Name != "box-01" {
		t.Fatalf("incomplete identity %+v", first)
	}
	second, err := LoadOrCreateIdentity(dir, "other-name")
	if err != nil {
		t.Fatal(err)
	}
	if second.UUID != first.UUID || second.Fingerprint() != first.Fingerprint() {
		t.Fatalf("identity changed across loads: %s/%s vs %s/%s", first.UUID, first.Fingerprint(), second.UUID, second.Fingerprint())
	}
	if second.Name != "box-01" {
		t.Fatalf("saved name lost: %q", second.Name)
	}
	// The private key must not be readable by anyone else. Windows has no
	// Unix permission bits and Go reports 0666 for any file it can write, so
	// the check is meaningful only where the mode is real; access there is
	// governed by the directory's ACL instead.
	info, err := os.Stat(filepath.Join(dir, nodeKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Fatalf("private key is world/group readable: %o", perm)
		}
	}
}

func TestIdentityCertificateNamesTheNode(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadOrCreateIdentity(dir, "box")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ParseCertPEM(id.CertPEM(), id.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if CertFingerprint(cert) != id.Fingerprint() {
		t.Fatal("fingerprint mismatch")
	}
	if _, err := ParseCertPEM(id.CertPEM(), "00000000-0000-0000-0000-000000000000"); err == nil {
		t.Fatal("certificate accepted for a different uuid")
	}
}

func TestCorruptIdentityFileIsRegenerated(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreateIdentity(dir, "box")
	if err != nil {
		t.Fatal(err)
	}
	// A missing key with a present identity file must not leave a UUID that
	// has no matching certificate.
	if err := os.Remove(filepath.Join(dir, nodeKeyFile)); err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateIdentity(dir, "box")
	if err != nil {
		t.Fatal(err)
	}
	if second.UUID == first.UUID {
		t.Fatal("identity without its key must be regenerated as a whole")
	}
	if _, err := ParseCertPEM(second.CertPEM(), second.UUID); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(second.Fingerprint(), "sha256:") {
		t.Fatalf("fingerprint format %q", second.Fingerprint())
	}
}
