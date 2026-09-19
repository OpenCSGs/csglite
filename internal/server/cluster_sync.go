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
	// The manifest names the files a download produced; anything else in
	// the directory (a GGUF converted from safetensors, for instance) is a
	// derived extra the receiver may regenerate itself.
	listed := map[string]bool{}
	for _, f := range lm.Files {
		listed[filepath.ToSlash(strings.TrimPrefix(f, "./"))] = true
	}
	for _, f := range lm.FileEntries {
		listed[filepath.ToSlash(strings.TrimPrefix(f.Path, "./"))] = true
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
		entry := cluster.BundleFile{Path: rel, Size: info.Size()}
		if len(listed) == 0 || listed[rel] {
			bundle.Files = append(bundle.Files, entry)
		} else {
			bundle.Extras = append(bundle.Extras, entry)
		}
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

// peerExtrasMinRate is the copy throughput below which derived files are not
// copied from a peer: regenerating a converted GGUF locally beats pulling
// gigabytes over a slow link.
const peerExtrasMinRate = 20 << 20 // 20 MB/s

// peerRateSampleMin is the smallest copy whose throughput is trusted.
const peerRateSampleMin = 64 << 20

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
	copyStarted := time.Now()
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
	elapsed := time.Since(copyStarted)
	rate := float64(total) / elapsed.Seconds()
	job.setProgress(api.PullResponse{Status: "copied from " + pm.Node.Name, TotalBytes: total, CompletedBytes: total})
	log.Printf("PULL JOB %s: %s copied from cluster node %s (%d bytes in %s, %.1f MB/s)", job.id, clusterID, pm.Node.Name, total, elapsed.Round(time.Second), rate/1e6)

	// The model is installed and usable now. Derived files (a converted
	// GGUF) are copied afterwards, in the background and only over a link
	// fast enough for that to beat converting here; a failure changes
	// nothing.
	if len(pm.Extras) > 0 {
		// A copy too small to time is not evidence of a slow link.
		if total < peerRateSampleMin || rate >= peerExtrasMinRate {
			go s.copyPeerExtras(pm, destDir, clusterID)
		} else {
			log.Printf("PULL JOB %s: link to %s ran at %.1f MB/s; skipping %d derived file(s) (%d bytes), they are regenerated on first use", job.id, pm.Node.Name, rate/1e6, len(pm.Extras), pm.ExtrasSize())
		}
	}
	return true, nil
}

// copyPeerExtras fetches derived artifacts into an installed model directory,
// each through a temporary name so a partial file is never picked up.
func (s *Server) copyPeerExtras(pm *cluster.PeerModel, destDir, clusterID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	tmpDir := destDir + ".cluster-extras"
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return
	}
	defer os.RemoveAll(tmpDir)
	for _, f := range pm.Extras {
		target, err := resolveUnder(destDir, f.Path)
		if err != nil {
			log.Printf("cluster: refusing derived file %s of %s from %s: %v", f.Path, clusterID, pm.Node.Name, err)
			return
		}
		staged, err := resolveUnder(tmpDir, f.Path)
		if err != nil {
			log.Printf("cluster: refusing derived file %s of %s from %s: %v", f.Path, clusterID, pm.Node.Name, err)
			return
		}
		if info, err := os.Stat(target); err == nil && info.Size() == f.Size {
			continue
		}
		if _, err := s.copyPeerFile(ctx, pm, f, tmpDir, func(int64) {}); err != nil {
			log.Printf("cluster: derived file %s of %s not copied from %s: %v", f.Path, clusterID, pm.Node.Name, err)
			return
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return
		}
		if err := os.Rename(staged, target); err != nil {
			log.Printf("cluster: installing derived file %s of %s: %v", f.Path, clusterID, err)
			return
		}
		log.Printf("cluster: derived file %s of %s copied from %s", f.Path, clusterID, pm.Node.Name)
	}
}

// copyPeerFile streams one file from the peer into tmpDir and verifies size
// and, when the peer sent one, the SHA-256 trailer.
// resolveUnder turns a path a peer named into a local one, and refuses
// anything that would land outside root. The cluster package already rejects
// an unsafe bundle when it is fetched; this is the second lock on the same
// door, because everything below writes files and a single missed check is a
// write anywhere the server can reach.
func resolveUnder(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "\\") {
		return "", fmt.Errorf("unsafe file path %q", rel)
	}
	target := filepath.Join(root, filepath.FromSlash(rel))
	cleanRoot := filepath.Clean(root)
	if target != cleanRoot && !strings.HasPrefix(target, cleanRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("file path %q escapes %s", rel, cleanRoot)
	}
	return target, nil
}

func (s *Server) copyPeerFile(ctx context.Context, pm *cluster.PeerModel, f cluster.BundleFile, tmpDir string, progress func(done int64)) (int64, error) {
	resp, err := s.cluster.OpenPeerFile(ctx, pm, f.Path)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	target, err := resolveUnder(tmpDir, f.Path)
	if err != nil {
		return 0, err
	}
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
