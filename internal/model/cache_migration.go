package model

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// AdoptCacheDirectory moves a legacy cache into the configured library layout,
// preserving its manifest, converted files and partial download state. Existing
// files are never overwritten with a different snapshot or different bytes.
func (m *Manager) AdoptCacheDirectory(ctx context.Context, legacy, id string) error {
	unlock, err := m.LockArtifact(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	source, namespace, name, err := parseStorageModelID(id)
	if err != nil {
		return err
	}
	dest := RegistryModelDir(m.cfg.ModelDir, string(source), namespace, name)
	if filepath.Clean(legacy) == filepath.Clean(dest) {
		return nil
	}
	if _, err := os.Lstat(legacy); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	var paths []string
	err = filepath.WalkDir(legacy, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("legacy model contains a symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported legacy cache file: %s", path)
		}
		rel, err := filepath.Rel(legacy, path)
		if err != nil {
			return err
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return err
	}
	old, oldErr := LoadManifestInDir(legacy)
	if oldErr != nil && !errors.Is(oldErr, os.ErrNotExist) {
		return oldErr
	}
	if old != nil && old.FullName() != id {
		return fmt.Errorf("legacy cache identity %q does not match %q", old.FullName(), id)
	}
	if _, err := os.Stat(dest); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := os.Rename(legacy, dest); err == nil {
			return nil
		}
		// ModelDir may be on another filesystem. Stage beside the destination,
		// then atomically install and only remove the original after success.
		stage, err := os.MkdirTemp(filepath.Dir(dest), ".cache-migrate-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		if err := copyModelTree(legacy, stage); err != nil {
			return err
		}
		if old != nil {
			if err := copyFile(filepath.Join(legacy, "manifest.json"), filepath.Join(stage, "manifest.json"), 0644); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Rename(stage, dest); err != nil {
			return err
		}
		return os.RemoveAll(legacy)
	} else if err != nil {
		return err
	}
	current, currentErr := LoadManifestInDir(dest)
	if currentErr != nil && !errors.Is(currentErr, os.ErrNotExist) {
		return currentErr
	}
	if current != nil && old != nil {
		sameResolved := current.ResolvedRevision != "" && current.ResolvedRevision == old.ResolvedRevision
		differentResolved := current.ResolvedRevision != "" && old.ResolvedRevision != "" && current.ResolvedRevision != old.ResolvedRevision
		if current.FullName() != id || differentResolved || (!sameResolved && current.RequestedRevision != old.RequestedRevision) {
			return fmt.Errorf("shared model %q conflicts with the legacy snapshot; legacy files retained", id)
		}
	} else {
		a, aErr := loadModelPullState(legacy)
		b, bErr := loadModelPullState(dest)
		if aErr != nil || bErr != nil || a != b {
			return fmt.Errorf("incompatible partial model cache for %q; legacy files retained", id)
		}
	}
	// Verify every collision before adding any missing files. The manifest is
	// merged separately; source checkpoints removed by conversion stay recorded.
	var missing []string
	for _, rel := range paths {
		if rel == "manifest.json" {
			continue
		}
		target := filepath.Join(dest, rel)
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			missing = append(missing, rel)
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("shared cache path is not a regular file: %s", target)
		}
		a, err := fileSHA256(filepath.Join(legacy, rel))
		if err != nil {
			return err
		}
		b, err := fileSHA256(target)
		if err != nil {
			return err
		}
		if a != b {
			return fmt.Errorf("shared model file %q conflicts with legacy cache; legacy files retained", rel)
		}
	}
	for _, rel := range missing {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		stage, err := os.CreateTemp(filepath.Dir(target), ".cache-migrate-*")
		if err != nil {
			return err
		}
		stagePath := stage.Name()
		if err := stage.Close(); err != nil {
			_ = os.Remove(stagePath)
			return err
		}
		err = copyFile(filepath.Join(legacy, rel), stagePath, 0644)
		if err == nil {
			err = os.Rename(stagePath, target)
		}
		_ = os.Remove(stagePath)
		if err != nil {
			return err
		}
	}
	if current != nil && old != nil {
		for _, removed := range old.RemovedSourceWeights {
			if !slices.Contains(current.RemovedSourceWeights, removed) {
				current.RemovedSourceWeights = append(current.RemovedSourceWeights, removed)
			}
		}
		if _, err := EnsureLocalModelFiles(dest, current); err != nil {
			return err
		}
		current.Size = localModelEntriesSize(current.FileEntries)
		current.Format = DetectFormat(current.Files)
		if err := SaveManifestInDir(dest, current); err != nil {
			return err
		}
	}
	return os.RemoveAll(legacy)
}
