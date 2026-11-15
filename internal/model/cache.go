package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/ggufpick"
	"github.com/opencsgs/csglite/internal/modelregistry"
)

// All Managers addressing the same library directory share the operation lock.
// This also serializes ordinary pulls with deployment preparation and migration.
var artifactOperations = struct {
	sync.Mutex
	entries map[string]*artifactOperation
}{entries: map[string]*artifactOperation{}}

type artifactOperation struct {
	token chan struct{}
	users int
}

func (m *Manager) LockArtifact(ctx context.Context, modelID string) (func(), error) {
	source, namespace, name, err := parseStorageModelID(modelID)
	if err != nil {
		return nil, err
	}
	key, err := filepath.Abs(RegistryModelDir(m.cfg.ModelDir, string(source), namespace, name))
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		key = strings.ToLower(key)
	}
	artifactOperations.Lock()
	op := artifactOperations.entries[key]
	if op == nil {
		op = &artifactOperation{token: make(chan struct{}, 1)}
		artifactOperations.entries[key] = op
	}
	op.users++
	artifactOperations.Unlock()
	releaseRef := func() {
		artifactOperations.Lock()
		op.users--
		if op.users == 0 {
			delete(artifactOperations.entries, key)
		}
		artifactOperations.Unlock()
	}
	select {
	case op.token <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-op.token; releaseRef() }) }, nil
	case <-ctx.Done():
		releaseRef()
		return nil, ctx.Err()
	}
}

// EnsureFrom reuses a complete installed snapshot before falling back to the
// ordinary downloader. A different revision never overwrites the shared cache.
func (m *Manager) EnsureFrom(ctx context.Context, repo, sourceValue, revision string, quants []string, progress csghub.SnapshotProgressFunc) (*LocalModel, error) {
	source, err := modelregistry.NormalizeSource(sourceValue)
	if err != nil {
		return nil, err
	}
	id := repo
	if source != modelregistry.SourceOpenCSG {
		id = string(source) + "/" + repo
	}
	unlock, err := m.LockArtifact(ctx, id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lm, err := m.Get(id)
	if err == nil {
		if err = validateCachedRevision(lm, revision); err != nil {
			return nil, err
		}
		dir, err := m.ModelPath(id)
		if err != nil {
			return nil, err
		}
		if err = validateCachedFiles(dir, lm); err != nil {
			return nil, err
		}
		if lm.FullName() != id {
			return nil, fmt.Errorf("cached model identity %q does not match %q", lm.FullName(), id)
		}
		if err := validateCachedQuants(dir, lm, quants); err != nil {
			return nil, err
		}
		if progress != nil {
			progress(csghub.SnapshotProgress{BytesCompletedAll: lm.Size, BytesTotalAll: lm.Size})
		}
		return lm, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading shared model cache: %w", err)
	}
	return m.pullFrom(ctx, repo, string(source), revision, quants, progress)
}

func validateCachedRevision(lm *LocalModel, revision string) error {
	requested := strings.TrimSpace(revision)
	stored := strings.TrimSpace(lm.RequestedRevision)
	resolved := strings.TrimSpace(lm.ResolvedRevision)
	if stored == "" && resolved != "" {
		stored = "main"
		if lm.ArtifactSource == "modelscope" {
			stored = "master"
		}
	}
	if requested == "" {
		requested = "main"
		if lm.ArtifactSource == "modelscope" {
			requested = "master"
		}
	}
	if requested != stored && requested != resolved {
		return fmt.Errorf("model %q is already cached at revision %q; requested %q: remove the unused model before changing its revision", lm.FullName(), firstNonEmptyModelValue(stored, resolved, "unknown"), requested)
	}
	return nil
}

func validateCachedFiles(dir string, lm *LocalModel) error {
	for _, name := range lm.Files {
		if slices.Contains(lm.RemovedSourceWeights, name) {
			continue
		}
		clean := cleanLocalModelPath(name)
		if clean == "" {
			return fmt.Errorf("invalid cached model path %q", name)
		}
		info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(clean)))
		if err != nil {
			return fmt.Errorf("shared model cache is incomplete: %w", err)
		}
		for _, entry := range lm.FileEntries {
			if entry.Path == name && entry.Size > 0 && entry.Size != info.Size() {
				return fmt.Errorf("shared model cache file %s has an incomplete size", name)
			}
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cached model file is not regular: %s", name)
		}
	}
	return nil
}

// RemoveWhen runs the reference/runtime check under the same lock as downloads.
func (m *Manager) RemoveWhen(ctx context.Context, id string, check func() error) error {
	unlock, err := m.LockArtifact(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	if check != nil {
		if err := check(); err != nil {
			return err
		}
	}
	return m.remove(id)
}

// RemoveCacheWhen also clears an interrupted download. Normal Remove still
// requires an installed manifest, preserving the existing library contract.
func (m *Manager) RemoveCacheWhen(ctx context.Context, id string, check func() error) error {
	unlock, err := m.LockArtifact(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	if check != nil {
		if err := check(); err != nil {
			return err
		}
	}
	source, namespace, name, err := parseStorageModelID(id)
	if err != nil {
		return err
	}
	if _, err := m.Get(id); errors.Is(err, os.ErrNotExist) {
		_, err = m.removePartial(namespace+"/"+name, string(source))
		return err
	} else if err != nil {
		return err
	}
	return m.remove(id)
}

func validateCachedQuants(dir string, lm *LocalModel, quants []string) error {
	if len(quants) == 0 {
		return nil
	}
	// Original HF checkpoints may be converted by the runtime; quant selection
	// applies to prebuilt GGUFs only when source weights are no longer available.
	for _, name := range lm.Files {
		if slices.Contains(lm.RemovedSourceWeights, name) {
			continue
		}
		if strings.HasSuffix(name, ".safetensors") || strings.HasSuffix(name, ".bin") {
			return nil
		}
	}
	paths, err := FindWeightGGUFRelPaths(dir)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	for _, quant := range quants {
		found := false
		for _, path := range paths {
			if strings.EqualFold(ggufpick.QuantLabelFromRepoPath(path), quant) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("cached model %q has no %s GGUF; choose an available quantization or remove the unused model before downloading another variant", lm.FullName(), quant)
		}
	}
	return nil
}
