// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"errors"
	"testing"
	"time"
)

func TestDirectoryFollowsAnAddressChange(t *testing.T) {
	d := NewDirectory()
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	d.Track("n1", []string{"10.0.0.5:11438"})
	d.MarkSuccess("n1", "10.0.0.5:11438", &Status{UUID: "n1"})
	// The node reboots and comes back on a new IP; discovery reports it.
	now = now.Add(30 * time.Second)
	d.MarkFailure("n1", errors.New("connection refused"))
	d.LearnAddress("n1", "10.0.0.77:11438", now)
	if got := d.Candidates("n1"); len(got) == 0 || got[0] != "10.0.0.77:11438" {
		t.Fatalf("new address not preferred: %v", got)
	}
	if rt, _ := d.Get("n1"); rt.Health != HealthSuspect {
		t.Fatalf("one failure should make the node suspect, got %s", rt.Health)
	}
	d.MarkSuccess("n1", "10.0.0.77:11438", &Status{UUID: "n1"})
	rt, _ := d.Get("n1")
	if rt.Health != HealthHealthy || rt.Addr != "10.0.0.77:11438" || rt.Failures != 0 {
		t.Fatalf("recovery %+v", rt)
	}
	snap := d.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("an address change must not create a second entry: %d", len(snap))
	}
}

func TestDirectoryDeclaresDownAfterSustainedFailures(t *testing.T) {
	d := NewDirectory()
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	d.Track("n1", []string{"10.0.0.5:11438"})
	d.MarkSuccess("n1", "10.0.0.5:11438", &Status{UUID: "n1"})
	for i := 0; i < 2; i++ {
		now = now.Add(10 * time.Second)
		d.MarkFailure("n1", errors.New("timeout"))
	}
	if rt, _ := d.Get("n1"); rt.Health != HealthSuspect {
		t.Fatalf("two quick failures should only be suspect: %s", rt.Health)
	}
	now = now.Add(45 * time.Second) // more than a minute since the last success
	d.MarkFailure("n1", errors.New("timeout"))
	rt, _ := d.Get("n1")
	if rt.Health != HealthDown {
		t.Fatalf("expected down, got %s", rt.Health)
	}
	if rt.NextPoll.Sub(now) != pollBackoff[2] {
		t.Fatalf("backoff %v", rt.NextPoll.Sub(now))
	}
	if !d.BeginPoll("n1") {
		t.Fatal("poll refused")
	}
	if rt, _ := d.Get("n1"); rt.Health != HealthProbing {
		t.Fatalf("down node under poll should be probing: %s", rt.Health)
	}
	d.MarkSuccess("n1", "10.0.0.5:11438", &Status{UUID: "n1"})
	if rt, _ := d.Get("n1"); rt.Health != HealthHealthy {
		t.Fatalf("probe success should heal: %s", rt.Health)
	}
}

func TestDirectoryReservationsAndBreakers(t *testing.T) {
	d := NewDirectory()
	d.Track("n1", nil)
	release := d.Reserve("n1")
	d.Reserve("n1")
	if rt, _ := d.Get("n1"); rt.reserveSeq != 2 {
		t.Fatalf("reserved %d", rt.reserveSeq)
	}
	release()
	if rt, _ := d.Get("n1"); rt.reserveSeq != 1 {
		t.Fatalf("after release %d", rt.reserveSeq)
	}
	d.MarkSuccess("n1", "a:1", &Status{UUID: "n1"})
	if rt, _ := d.Get("n1"); rt.reserveSeq != 0 {
		t.Fatal("telemetry should clear reservations")
	}
	d.RequestFailed("n1", "m", true)
	if !d.ModelBroken("n1", "m") || d.ModelBroken("n1", "other") {
		t.Fatal("model breaker scope wrong")
	}
	if rt, _ := d.Get("n1"); rt.Health != HealthHealthy {
		t.Fatal("a model-specific failure must not mark the node")
	}
	d.RequestFailed("n1", "m", false)
	if rt, _ := d.Get("n1"); rt.Health != HealthSuspect {
		t.Fatal("a transport failure must mark the node suspect")
	}
	d.RequestSucceeded("n1")
	if rt, _ := d.Get("n1"); rt.Health != HealthHealthy {
		t.Fatal("a streamed answer is proof of life")
	}
}

func TestDirectoryDiscoveredNodes(t *testing.T) {
	d := NewDirectory()
	obs := Observation{Announcement: Announcement{UUID: "x", Name: "box", ClusterPort: 11438}, Seen: time.Now()}
	obs.Addr = mustAddr("10.1.1.1")
	d.Observe(obs, func(string) bool { return false })
	if got := d.Discovered(time.Minute); len(got) != 1 || got[0].Endpoint() != "10.1.1.1:11438" {
		t.Fatalf("discovered %+v", got)
	}
	// Once paired, the same observation feeds the member's address instead.
	d.Track("x", nil)
	d.Observe(obs, func(string) bool { return true })
	if got := d.Discovered(time.Minute); len(got) != 0 {
		t.Fatal("member still listed as discovered")
	}
	if got := d.Candidates("x"); len(got) != 1 || got[0] != "10.1.1.1:11438" {
		t.Fatalf("member address %v", got)
	}
}
