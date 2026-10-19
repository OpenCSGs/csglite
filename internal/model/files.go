package model

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

func normalizeLocalModel(m *LocalModel) {
	if m == nil || len(m.FileEntries) == 0 {
		return
	}

	entries := make([]LocalModelFile, 0, len(m.FileEntries))
	seen := make(map[string]struct{}, len(m.FileEntries))
	for _, entry := range m.FileEntries {
		relPath := cleanLocalModelPath(entry.Path)
		if relPath == "" || relPath == "manifest.json" {
			continue
		}
		if _, ok := seen[relPath]; ok {
			continue
		}
		seen[relPath] = struct{}{}
		entry.Path = relPath
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	m.FileEntries = entries

	files := localModelFilePaths(entries)
	if len(files) > 0 {
		m.Files = files
	}
	if m.Format == "" || m.Format == FormatUnknown {
		m.Format = DetectFormat(m.Files)
	}
}

func EnsureLocalModelFiles(modelDir string, m *LocalModel) (bool, error) {
	if m == nil {
		return false, nil
	}

	normalizeLocalModel(m)

	known := make(map[string]LocalModelFile, len(m.FileEntries))
	for _, entry := range m.FileEntries {
		known[entry.Path] = entry
	}

	entries := make([]LocalModelFile, 0, len(m.FileEntries))
	seen := make(map[string]struct{}, len(m.FileEntries))
	changed := false

	err := filepath.WalkDir(modelDir, func(fullPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fullPath == modelDir {
			return nil
		}

		relPath, err := filepath.Rel(modelDir, fullPath)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		if d.Type()&os.ModeSymlink != 0 {
			changed = true
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if relPath == "manifest.json" {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			changed = true
			return nil
		}

		entry, ok := known[relPath]
		if !ok {
			entry = LocalModelFile{Path: relPath}
			changed = true
		}

		if entry.Path != relPath {
			entry.Path = relPath
			changed = true
		}
		if entry.Size != info.Size() {
			entry.Size = info.Size()
			entry.SHA256 = ""
			changed = true
		}
		if entry.SHA256 == "" {
			sum, err := fileSHA256(fullPath)
			if err != nil {
				return err
			}
			entry.SHA256 = sum
			changed = true
		}

		entries = append(entries, entry)
		seen[relPath] = struct{}{}
		return nil
	})
	if err != nil {
		return false, err
	}

	for relPath := range known {
		if _, ok := seen[relPath]; !ok {
			changed = true
			break
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	files := localModelFilePaths(entries)
	if !reflect.DeepEqual(m.FileEntries, entries) {
		m.FileEntries = entries
		changed = true
	}
	if !reflect.DeepEqual(m.Files, files) {
		m.Files = files
		changed = true
	}

	return changed, nil
}

func cleanLocalModelPath(relPath string) string {
	trimmed := strings.TrimSpace(filepath.ToSlash(relPath))
	if trimmed == "" {
		return ""
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == "/" || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return strings.TrimPrefix(cleaned, "./")
}

func localModelFilePaths(entries []LocalModelFile) []string {
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Path == "" {
			continue
		}
		files = append(files, entry.Path)
	}
	return files
}

func fileSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// RefreshManifestFiles rescans modelDir after files were added or removed in
// place, such as a GGUF conversion replacing its SafeTensors source, and
// rewrites the manifest's file list, size and format to match the disk.
func RefreshManifestFiles(modelDir string) error {
	lm, err := LoadManifestInDir(modelDir)
	if err != nil {
		return err
	}
	if _, err := EnsureLocalModelFiles(modelDir, lm); err != nil {
		return err
	}
	lm.Size = localModelEntriesSize(lm.FileEntries)
	if format := DetectFormat(lm.Files); format != FormatUnknown {
		lm.Format = format
	}
	return SaveManifestInDir(modelDir, lm)
}

// RecordRemovedSourceWeights adds names to the manifest's removed source
// weights before they are deleted, so an interrupted removal still leaves the
// model marked as no longer convertible.
func RecordRemovedSourceWeights(modelDir string, names []string) error {
	lm, err := LoadManifestInDir(modelDir)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(lm.RemovedSourceWeights)+len(names))
	for _, name := range lm.RemovedSourceWeights {
		seen[name] = struct{}{}
	}
	for _, name := range names {
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			lm.RemovedSourceWeights = append(lm.RemovedSourceWeights, name)
		}
	}
	return SaveManifestInDir(modelDir, lm)
}
