// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import "testing"

func TestUsableHostname(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{" worker-01.example.com ", "worker-01"},
		{"JamesdeMacBook-Pro\n", "JamesdeMacBook-Pro"},
		{"James的MacBook Pro", "James的MacBook Pro"},
		{"WIN-NODE", "WIN-NODE"},
		{"192.168.1.24", ""},
		{"2001:db8::1", ""},
		{"192", ""},
		{"", ""},
	} {
		if got := usableHostname(tc.input); got != tc.want {
			t.Errorf("usableHostname(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestGeneratedNodeNameMigrationPreservesIdentity(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadOrCreateIdentity(dir, genericNodeName)
	if err != nil {
		t.Fatal(err)
	}
	uuid, fingerprint := id.UUID, id.Fingerprint()
	for _, name := range []string{genericNodeName, defaultShortName(uuid), "csglite-node-" + uuid, "my-worker", "csglite-node-custom"} {
		if err := id.SaveName(name); err != nil {
			t.Fatal(err)
		}
		m, err := New(Options{Dir: dir, Host: &fakeHost{}})
		if err != nil {
			t.Fatal(err)
		}
		want := name
		if isGeneratedNodeName(name, uuid) {
			want = defaultNodeName()
		}
		if m.identity.DisplayName() != want {
			t.Fatalf("name = %q, want %q", m.identity.DisplayName(), want)
		}
		if m.identity.UUID != uuid || m.identity.Fingerprint() != fingerprint {
			t.Fatal("renaming changed node identity")
		}
	}
}
