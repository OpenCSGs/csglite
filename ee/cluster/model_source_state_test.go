// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"errors"
	"testing"
)

func TestModelSourceStateTracksExactPinsAndRecovery(t *testing.T) {
	m, err := New(Options{Dir: t.TempDir(), Host: &fakeHost{licensed: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.store.Create("test"); err != nil {
		t.Fatal(err)
	}
	for _, uuid := range []string{"worker", "other"} {
		if _, err := m.store.Upsert(Member{UUID: uuid, Name: uuid, CertFingerprint: "test"}, true); err != nil {
			t.Fatal(err)
		}
	}
	st := &Status{Licensed: true, State: NodeStateActive, AcceptWork: true, Models: []ModelStatus{{ID: "deployment/jev", Loaded: false, Active: 20}}}
	m.dir.MarkSuccess("worker", "worker:11438", st)
	m.dir.MarkSuccess("other", "other:11438", st)
	ctx := context.Background()
	check := func(source, want string) {
		t.Helper()
		if got := m.ModelSourceState(ctx, "deployment/jev", source); got != want {
			t.Fatalf("%s: got %s want %s", source, got, want)
		}
	}
	check("node:worker", "configured") // Sleeping or busy does not remove a service.
	for i := 0; i < 6; i++ {
		m.dir.MarkFailure("worker", errors.New("offline"))
	}
	check("node:worker", "offline")
	check("cluster", "configured") // The other copy remains routable.
	if got := m.ModelSourceState(ctx, "deployment/missing", "node:other"); got != "missing" {
		t.Fatalf("wrong model accepted: %s", got)
	}
	check("node:removed", "missing")
	m.dir.MarkSuccess("worker", "worker:11438", st)
	check("node:worker", "configured")
	for _, modify := range []func(*Status){func(s *Status) { s.State = NodeStateMaintenance }, func(s *Status) { s.State = NodeStateDrain }, func(s *Status) { s.AcceptWork = false }, func(s *Status) { s.Licensed = false }} {
		unavailable := *st
		modify(&unavailable)
		m.dir.MarkSuccess("worker", "worker:11438", &unavailable)
		check("node:worker", "unavailable")
	}
}
