package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/internal/modelregistry"
	"github.com/opencsgs/csglite/pkg/api"
)

// Model sync between members: a node that needs a model first copies it from
// a peer that already holds it over the cluster's mTLS channel (LAN speed, no
// internet traffic) and only downloads from the model source when no peer has
// it. The copy lands in the same directory layout as a normal pull, so the
// rest of CSGLite cannot tell the difference.

// ModelBundle implements cluster.Host: the files of a complete local model.
func (h *clusterHost) ModelBundle(modelID string) (*cluster.ModelBundle, error) {
	s := h.s
	lm, err := s.manager.ResolveLocalModel(strings.TrimSpace(modelID))
	if err != nil {
		return nil, err
	}
	storageID := lm.FullName()
	if s.pullJobs != nil && s.pullJobs.hasActiveModelAnySource(lm.Namespace+"/"+lm.Name) {
		return nil, fmt.Errorf("model %q is still downloading on this node", modelID)
	}
	dir, err := s.manager.ModelPath(storageID)
	if err != nil {
		return nil, err
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	bundle := &cluster.ModelBundle{Dir: dir, Manifest: manifest}
	err = filepath.Walk(dir, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "manifest.json" || strings.HasSuffix(rel, ".part") || strings.HasSuffix(rel, ".tmp") || strings.HasPrefix(filepath.Base(rel), ".") {
			return nil
		}
		bundle.Files = append(bundle.Files, cluster.BundleFile{Path: rel, Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(bundle.Files) == 0 {
		return nil, fmt.Errorf("model %q has no files", modelID)
	}
	return bundle, nil
}

// hasActiveModelAnySource reports whether a pull job for the model is
// running or queued, whatever its source.
func (st *pullJobStore) hasActiveModelAnySource(name string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, id := range st.activeKey {
		job := st.jobs[id]
		if job == nil {
			continue
		}
		job.mu.Lock()
		active := job.kind == "model" && job.name == name && (job.status == pullJobRunning || job.status == pullJobQueued)
		job.mu.Unlock()
		if active {
			return true
		}
	}
	return false
}

// pullModelFromPeer tries to satisfy a model pull job from a cluster member.
// It returns handled=true when the model was copied (or the copy failed in a
// way that must not fall back, such as cancellation); false means "no peer
// has it, download from the source as usual".
func (s *Server) pullModelFromPeer(ctx context.Context, job *pullJob) (handled bool, err error) {
	if s.cluster == nil || !s.cluster.Active() || !s.cluster.Store().InCluster() || job.kind != "model" {
		return false, nil
	}
	if len(job.quants) > 0 || strings.TrimSpace(job.revision) != "" {
		// A specific quantization or revision names a subset of the
		// repository; a peer's copy may be a different one. Let the source
		// resolve it.
		return false, nil
	}
	source, err := modelregistry.NormalizeSource(job.source)
	if err != nil {
		return false, nil
	}
	parts := strings.Split(strings.Trim(job.name, "/"), "/")
	if len(parts) != 2 {
		return false, nil
	}
	namespace, name := parts[0], parts[1]
	destDir := model.RegistryModelDir(s.cfg.ModelDir, string(source), namespace, name)
	if _, statErr := os.Stat(filepath.Join(destDir, "manifest.json")); statErr == nil {
		return false, nil // installed already; the normal path reports it
	}
	// Peers advertise models under their public id: the short name for
	// OpenCSG repositories, source/namespace/name for other registries.
	lm := &model.LocalModel{Namespace: namespace, Name: name, ArtifactSource: string(source)}
	clusterID := model.InferenceModelID(lm)
	job.setProgress(api.PullResponse{Status: "looking for a cluster node that has " + clusterID})
	pm, err := s.cluster.FindPeerModel(ctx, clusterID)
	if pm == nil && clusterID != lm.FullName() {
		pm, err = s.cluster.FindPeerModel(ctx, lm.FullName())
	}
	if err != nil || pm == nil {
		if err != nil {
			log.Printf("PULL JOB %s: no cluster copy of %s (%v); downloading from %s", job.id, clusterID, err, source)
		}
		return false, nil
	}
	log.Printf("PULL JOB %s: copying %s from cluster node %s (%d files, %d bytes)", job.id, clusterID, pm.Node.Name, len(pm.Files), pm.TotalSize())

	tmpDir := destDir + ".cluster-copy"
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return false, nil
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	total := pm.TotalSize()
	var doneAll int64
	for _, f := range pm.Files {
		if ctx.Err() != nil {
			cleanup()
			return true, ctx.Err()
		}
		written, copyErr := s.copyPeerFile(ctx, pm, f, tmpDir, func(done int64) {
			job.setProgress(api.PullResponse{
				Status:         fmt.Sprintf("copying %s from %s", f.Path, pm.Node.Name),
				Digest:         f.Path,
				Total:          f.Size,
				Completed:      done,
				TotalBytes:     total,
				CompletedBytes: doneAll + done,
			})
		})
		if copyErr != nil {
			cleanup()
			if ctx.Err() != nil {
				return true, ctx.Err()
			}
			log.Printf("PULL JOB %s: copying %s from %s failed (%v); downloading from %s instead", job.id, f.Path, pm.Node.Name, copyErr, source)
			return false, nil
		}
		doneAll += written
	}

	// The peer's manifest, stamped with our download time.
	var manifest map[string]any
	if err := json.Unmarshal(pm.Manifest, &manifest); err != nil {
		cleanup()
		return false, nil
	}
	manifest["downloaded_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	raw, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), raw, 0o644); err != nil {
		cleanup()
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		cleanup()
		return false, nil
	}
	if err := os.Rename(tmpDir, destDir); err != nil {
		cleanup()
		log.Printf("PULL JOB %s: installing cluster copy failed: %v", job.id, err)
		return false, nil
	}
	job.setProgress(api.PullResponse{Status: "copied from " + pm.Node.Name, TotalBytes: total, CompletedBytes: total})
	log.Printf("PULL JOB %s: %s copied from cluster node %s", job.id, clusterID, pm.Node.Name)
	return true, nil
}

// copyPeerFile streams one file from the peer into tmpDir and verifies size
// and, when the peer sent one, the SHA-256 trailer.
func (s *Server) copyPeerFile(ctx context.Context, pm *cluster.PeerModel, f cluster.BundleFile, tmpDir string, progress func(done int64)) (int64, error) {
	resp, err := s.cluster.OpenPeerFile(ctx, pm, f.Path)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	target := filepath.Join(tmpDir, filepath.FromSlash(f.Path))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	out, err := os.Create(target)
	if err != nil {
		return 0, err
	}
	hasher := sha256.New()
	var written int64
	buf := make([]byte, 1<<20)
	lastReport := time.Now()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				return written, err
			}
			hasher.Write(buf[:n])
			written += int64(n)
			if time.Since(lastReport) > 500*time.Millisecond {
				progress(written)
				lastReport = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			out.Close()
			return written, readErr
		}
	}
	if err := out.Close(); err != nil {
		return written, err
	}
	progress(written)
	if written != f.Size {
		return written, fmt.Errorf("%s: got %d bytes, expected %d", f.Path, written, f.Size)
	}
	if want := strings.TrimSpace(resp.Trailer.Get(cluster.SHA256Trailer)); want != "" {
		if got := hex.EncodeToString(hasher.Sum(nil)); got != want {
			return written, fmt.Errorf("%s: checksum mismatch", f.Path)
		}
	}
	return written, nil
}
