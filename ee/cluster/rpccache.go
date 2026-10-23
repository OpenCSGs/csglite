// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// The worker's tensor cache is what makes a second split of the same model
// take a minute instead of eight, and nothing upstream ever removes from it:
// llama.cpp names each file by a hash of the tensor it holds, keeps no record
// of which model a file belongs to, and does not touch a file when it reuses
// it. Left alone it holds every model ever split onto this machine, at roughly
// the size of the share this machine took. So it is kept for as long as it is
// plausibly about to be used again, and dropped whole after that or when the
// disk needs the room.
//
// It is dropped whole because nothing finer is sound. File times say when a
// tensor was first written, not when it was last read, so evicting the oldest
// files would take the model that is split here every day before the one
// tried once last week.
const (
	// rpcCacheIdleTTL is how long the cache is kept after this machine last
	// took part in a split.
	rpcCacheIdleTTL = 7 * 24 * time.Hour
	// rpcCachePruneEvery is how often an idle cache is looked at. Its age is
	// counted in days, so looking more often only costs directory walks.
	rpcCachePruneEvery = time.Hour
	// rpcCacheStampName marks when the worker last ran. llama.cpp writes its
	// files into an rpc/ folder below the cache directory, so the stamp sits
	// beside that folder and is never taken for a tensor.
	rpcCacheStampName = ".last-used"
)

// touchCacheStamp records that the worker has just used the cache. The caller
// holds the lock.
func (w *rpcWorker) touchCacheStamp(now time.Time) {
	if w.cacheDir == "" {
		return
	}
	stamp := filepath.Join(w.cacheDir, rpcCacheStampName)
	if err := os.Chtimes(stamp, now, now); err == nil {
		return
	}
	if err := os.WriteFile(stamp, nil, 0o600); err != nil && !os.IsNotExist(err) {
		w.logf("cluster: recording when the RPC tensor cache was last used: %v", err)
	}
}

// cacheUsage reports the cache's size and when it was last used: the stamp, or
// the newest file for a cache written before there was one.
func (w *rpcWorker) cacheUsage() (size int64, lastUsed time.Time) {
	_ = filepath.WalkDir(w.cacheDir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if d.Name() != rpcCacheStampName {
			size += info.Size()
		}
		if info.ModTime().After(lastUsed) {
			lastUsed = info.ModTime()
		}
		return nil
	})
	return size, lastUsed
}

// pruneIdleCache drops the cache once it has gone unused for rpcCacheIdleTTL.
// It reports how much it freed.
func (w *rpcWorker) pruneIdleCache(now time.Time) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cacheDir == "" || w.cmd != nil {
		return 0
	}
	w.lastPrune = now
	size, lastUsed := w.cacheUsage()
	if size == 0 || now.Sub(lastUsed) < rpcCacheIdleTTL {
		return 0
	}
	idle := now.Sub(lastUsed).Truncate(time.Hour)
	return w.clearCacheLocked(size, fmt.Sprintf("nothing has been split onto this node for %s", idle))
}

// pruneIdleCacheDue is pruneIdleCache at most once per rpcCachePruneEvery.
func (w *rpcWorker) pruneIdleCacheDue(now time.Time) int64 {
	w.mu.Lock()
	due := now.Sub(w.lastPrune) >= rpcCachePruneEvery
	w.mu.Unlock()
	if !due {
		return 0
	}
	return w.pruneIdleCache(now)
}

// clearCacheForDisk drops the cache because the disk is short of room. A cache
// that would only speed up a reload is the first thing to give up.
func (w *rpcWorker) clearCacheForDisk(free, reserve uint64) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cacheDir == "" || w.cmd != nil {
		return 0
	}
	size, _ := w.cacheUsage()
	if size == 0 {
		return 0
	}
	return w.clearCacheLocked(size, fmt.Sprintf("only %s is free on disk, below the %s the cluster keeps in reserve",
		formatBytes(int64(free)), formatBytes(int64(reserve))))
}

// clearCacheLocked removes the whole cache. The caller holds the lock and has
// made sure no worker is running, since one might be reading a file right now.
func (w *rpcWorker) clearCacheLocked(size int64, why string) int64 {
	if err := os.RemoveAll(w.cacheDir); err != nil {
		w.logf("cluster: clearing the RPC tensor cache: %v", err)
		return 0
	}
	w.logf("cluster: cleared the RPC tensor cache (%s) because %s; the next split onto this node will send its weights over the network again",
		formatBytes(size), why)
	return size
}

// formatBytes renders a byte count for a log line.
func formatBytes(n int64) string {
	const gib = 1 << 30
	if n >= gib {
		return fmt.Sprintf("%.1f GB", float64(n)/gib)
	}
	return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
}
