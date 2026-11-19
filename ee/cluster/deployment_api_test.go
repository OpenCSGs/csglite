// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"testing"
	"time"
)

func TestStalledDownloadSpeed(t *testing.T) {
	now := time.Now().UTC()
	previous := DeploymentStatus{Phase: "downloading", CompletedBytes: 10, SpeedBytesPerSec: 4096, UpdatedAt: now}

	cases := []struct {
		name string
		next DeploymentStatus
		want int64
	}{
		{
			name: "progressing receipt keeps its speed",
			next: DeploymentStatus{Phase: "downloading", CompletedBytes: 20, SpeedBytesPerSec: 4096, UpdatedAt: now.Add(time.Second)},
			want: 4096,
		},
		{
			name: "repeated receipt drops the frozen speed",
			next: DeploymentStatus{Phase: "downloading", CompletedBytes: 10, SpeedBytesPerSec: 4096, UpdatedAt: now},
			want: 0,
		},
		{
			name: "worker clock stepping back drops the frozen speed",
			next: DeploymentStatus{Phase: "downloading", CompletedBytes: 10, SpeedBytesPerSec: 4096, UpdatedAt: now.Add(-time.Minute)},
			want: 0,
		},
		{
			name: "new report without new bytes keeps its speed",
			next: DeploymentStatus{Phase: "downloading", CompletedBytes: 10, SpeedBytesPerSec: 4096, UpdatedAt: now.Add(time.Second)},
			want: 4096,
		},
		{
			name: "phase change is left alone",
			next: DeploymentStatus{Phase: "starting", SpeedBytesPerSec: 0, UpdatedAt: now},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stalledDownloadSpeed(previous, tc.next); got != tc.want {
				t.Fatalf("stalledDownloadSpeed = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestClearStaleDownloadSpeed(t *testing.T) {
	now := time.Now().UTC()
	all := []Deployment{
		{ID: "fresh", Status: DeploymentStatus{Phase: "downloading", SpeedBytesPerSec: 4096, UpdatedAt: now.Add(-time.Second)}},
		{ID: "stale", Status: DeploymentStatus{Phase: "downloading", SpeedBytesPerSec: 4096, UpdatedAt: now.Add(-time.Minute)}},
		{ID: "ready", Status: DeploymentStatus{Phase: "ready", SpeedBytesPerSec: 4096, UpdatedAt: now.Add(-time.Minute)}},
		{ID: "ahead", Status: DeploymentStatus{Phase: "downloading", SpeedBytesPerSec: 4096, UpdatedAt: now.Add(time.Minute)}},
	}
	got := clearStaleDownloadSpeed(all, now)
	want := map[string]int64{"fresh": 4096, "stale": 0, "ready": 4096, "ahead": 4096}
	for _, d := range got {
		if d.Status.SpeedBytesPerSec != want[d.ID] {
			t.Fatalf("%s speed = %d, want %d", d.ID, d.Status.SpeedBytesPerSec, want[d.ID])
		}
	}
}

func TestClearStaleDownloadSpeedLeavesStoreUntouched(t *testing.T) {
	store, err := openDeploymentStore(t.TempDir())
	if err != nil {
		t.Fatalf("open deployment store: %v", err)
	}
	stale := time.Now().UTC().Add(-time.Minute)
	if err := store.update(false, "stale", func(d *Deployment, exists bool) error {
		d.ID = "stale"
		d.Status = DeploymentStatus{Phase: "downloading", CompletedBytes: 10, SpeedBytesPerSec: 4096, UpdatedAt: stale}
		return nil
	}); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	view := clearStaleDownloadSpeed(store.list(false), time.Now().UTC())
	if len(view) != 1 || view[0].Status.SpeedBytesPerSec != 0 {
		t.Fatalf("view = %+v, want a cleared speed", view)
	}
	stored := store.list(false)
	if len(stored) != 1 || stored[0].Status.SpeedBytesPerSec != 4096 {
		t.Fatalf("stored = %+v, want the persisted speed untouched", stored)
	}
}
