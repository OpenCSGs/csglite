// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// writeCachedTensor puts a file where llama.cpp would, dated at.
func writeCachedTensor(t *testing.T, dir, name string, size int, at time.Time) {
	t.Helper()
	path := filepath.Join(dir, "rpc", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func newTestWorker(t *testing.T) (*rpcWorker, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "rpc-cache")
	return newRPCWorker(dir, "", func(string, ...any) {}), dir
}

func TestIdleCacheIsKeptWithinTTL(t *testing.T) {
	w, dir := newTestWorker(t)
	now := time.Now()
	writeCachedTensor(t, dir, "e7d2327d212cf96c", 1024, now.Add(-rpcCacheIdleTTL+time.Hour))
	if freed := w.pruneIdleCache(now); freed != 0 {
		t.Fatalf("a cache used inside the TTL was cleared (%d bytes)", freed)
	}
	if _, err := os.Stat(filepath.Join(dir, "rpc", "e7d2327d212cf96c")); err != nil {
		t.Fatalf("cached tensor is gone: %v", err)
	}
}

func TestIdleCacheIsDroppedAfterTTL(t *testing.T) {
	// A cache written before there was a stamp ages from its newest file.
	w, dir := newTestWorker(t)
	now := time.Now()
	old := now.Add(-rpcCacheIdleTTL - time.Hour)
	writeCachedTensor(t, dir, "a", 1000, old.Add(-time.Hour))
	writeCachedTensor(t, dir, "b", 24, old)
	if freed := w.pruneIdleCache(now); freed != 1024 {
		t.Fatalf("freed %d bytes, want 1024", freed)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("cache directory still exists: %v", err)
	}
}

func TestStampKeepsAReusedCacheAlive(t *testing.T) {
	// Reusing a cached tensor rewrites nothing, so the files keep the date of
	// the first split; the stamp is what says the cache is still in use.
	w, dir := newTestWorker(t)
	now := time.Now()
	writeCachedTensor(t, dir, "a", 1024, now.Add(-30*24*time.Hour))
	w.touchCacheStamp(now.Add(-time.Hour))
	if freed := w.pruneIdleCache(now); freed != 0 {
		t.Fatalf("a cache used an hour ago was cleared (%d bytes)", freed)
	}
}

func TestCacheIsNotTouchedWhileTheWorkerRuns(t *testing.T) {
	w, dir := newTestWorker(t)
	now := time.Now()
	writeCachedTensor(t, dir, "a", 1024, now.Add(-30*24*time.Hour))
	w.cmd = &exec.Cmd{}
	if freed := w.pruneIdleCache(now); freed != 0 {
		t.Fatal("cleared the cache of a running worker")
	}
	if freed := w.clearCacheForDisk(0, 50<<30); freed != 0 {
		t.Fatal("cleared the cache of a running worker for disk")
	}
}

func TestCacheIsClearedForDisk(t *testing.T) {
	w, dir := newTestWorker(t)
	writeCachedTensor(t, dir, "a", 2048, time.Now())
	if freed := w.clearCacheForDisk(10<<30, 50<<30); freed != 2048 {
		t.Fatalf("freed %d bytes, want 2048", freed)
	}
}

func TestPruneIdleCacheDueIsThrottled(t *testing.T) {
	w, dir := newTestWorker(t)
	now := time.Now()
	w.pruneIdleCache(now)
	writeCachedTensor(t, dir, "a", 1024, now.Add(-30*24*time.Hour))
	if freed := w.pruneIdleCacheDue(now.Add(time.Minute)); freed != 0 {
		t.Fatal("looked at the cache again a minute later")
	}
	if freed := w.pruneIdleCacheDue(now.Add(rpcCachePruneEvery)); freed != 1024 {
		t.Fatalf("freed %d bytes once due, want 1024", freed)
	}
}
