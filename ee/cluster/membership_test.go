// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJoinTokenRoundTrip(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info, token, err := st.Create("Lab")
	if err != nil {
		t.Fatal(err)
	}
	clusterUUID, secret, err := ParseJoinToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if clusterUUID != info.UUID || secret == "" {
		t.Fatalf("token %q does not name cluster %s", token, info.UUID)
	}
	if !st.VerifyJoinToken(token) {
		t.Fatal("freshly minted token rejected")
	}
	if st.VerifyJoinToken(token + "x") {
		t.Fatal("tampered token accepted")
	}
	if st.JoinTokenHash() != hashSecret(secret) {
		t.Fatal("stored hash is not the hash of the secret")
	}
	rotated, err := st.RotateJoinToken()
	if err != nil {
		t.Fatal(err)
	}
	if st.VerifyJoinToken(token) || !st.VerifyJoinToken(rotated) {
		t.Fatal("rotation must invalidate the old token only")
	}
	for _, bad := range []string{"", "csgl1-", "nope-" + info.UUID + "-abc", "csgl1-not-a-uuid-abc", "csgl1-" + info.UUID} {
		if _, _, err := ParseJoinToken(bad); err == nil {
			t.Errorf("token %q parsed", bad)
		}
	}
}

func TestStorePersistsMembersAndTombstones(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Create("Lab"); err != nil {
		t.Fatal(err)
	}
	mem := Member{UUID: "11111111-1111-1111-1111-111111111111", Name: "b", CertFingerprint: "sha256:aa", LastAddresses: []string{"10.0.0.2:11438"}}
	if added, err := st.Upsert(mem, true); err != nil || !added {
		t.Fatalf("upsert: added=%v err=%v", added, err)
	}
	// Same member, different certificate: refused unless forced.
	if _, err := st.Upsert(Member{UUID: mem.UUID, CertFingerprint: "sha256:bb"}, false); err == nil {
		t.Fatal("certificate swap accepted without an explicit pairing")
	}
	st.RecordAddress(mem.UUID, "10.0.0.9:11438")
	st.RecordAddress(mem.UUID, "10.0.0.9:11438")
	st.RecordAddress(mem.UUID, "10.0.0.3:11438")
	st.RecordAddress(mem.UUID, "10.0.0.4:11438")

	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.Member(mem.UUID)
	if !ok {
		t.Fatal("member lost after reopen")
	}
	if len(got.LastAddresses) != maxLastAddresses || got.LastAddresses[0] != "10.0.0.4:11438" {
		t.Fatalf("last addresses %v", got.LastAddresses)
	}
	if removed, err := reopened.Remove(mem.UUID); err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if !reopened.IsTombstoned(mem.UUID) {
		t.Fatal("removed member has no tombstone")
	}
	// Gossip cannot resurrect a tombstoned member...
	if added, _ := reopened.Upsert(mem, false); added {
		t.Fatal("tombstoned member re-added by gossip")
	}
	// ...but an explicit pairing can.
	if added, err := reopened.Upsert(mem, true); err != nil || !added {
		t.Fatalf("explicit re-pair: %v %v", added, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, clusterFile))
	var onDisk storeFile
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.JoinTokenHash == "" || len(onDisk.Members) != 1 {
		t.Fatalf("on-disk %+v", onDisk)
	}
}

func TestNodeCodeIsSingleUseAndExpires(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	code, expires, err := st.NodeCode()
	if err != nil || len(code) != 8 || time.Until(expires) <= 0 {
		t.Fatalf("code %q expires %v err %v", code, expires, err)
	}
	again, _, _ := st.NodeCode()
	if again != code {
		t.Fatal("a valid code must be reused, not reminted")
	}
	if st.VerifyNodeCode("00000000") && code != "00000000" {
		t.Fatal("wrong code accepted")
	}
	if !st.VerifyNodeCode(code) {
		t.Fatal("right code rejected")
	}
	if st.VerifyNodeCode(code) {
		t.Fatal("code accepted twice")
	}
	if _, _, err := st.Create("Lab"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.NodeCode(); err == nil {
		t.Fatal("a clustered node must not hand out admission codes")
	}
}

func TestHandshakeMACBindsEveryField(t *testing.T) {
	base := HandshakeMAC("secret", "uuid", "sha256:fp", "nonce", 100)
	for _, alt := range []string{
		HandshakeMAC("other", "uuid", "sha256:fp", "nonce", 100),
		HandshakeMAC("secret", "uuid2", "sha256:fp", "nonce", 100),
		HandshakeMAC("secret", "uuid", "sha256:xx", "nonce", 100),
		HandshakeMAC("secret", "uuid", "sha256:fp", "nonce2", 100),
		HandshakeMAC("secret", "uuid", "sha256:fp", "nonce", 101),
	} {
		if alt == base {
			t.Fatal("MAC ignores a field")
		}
	}
	if !hmacEqual(base, HandshakeMAC("secret", "uuid", "sha256:fp", "nonce", 100)) {
		t.Fatal("equal MACs compare unequal")
	}
}

func TestSettingsNormalize(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := st.UpdateSettings(func(s *Settings) {
		s.Weight = 999
		s.RoutingMode = "weird"
		s.State = "gone"
		s.AffinityMaxQueue = -4
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Weight != 200 || s.RoutingMode != RoutingLocalFirst || s.State != NodeStateActive || s.AffinityMaxQueue != 0 {
		t.Fatalf("normalized %+v", s)
	}
}
