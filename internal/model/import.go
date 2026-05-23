package model

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencsgs/csghub-lite/internal/csghub"
)

type ImportSourceKind string

const (
	ImportSourceDirectory ImportSourceKind = "directory"
	ImportSourceArchive   ImportSourceKind = "archive"
)

type ImportOptions struct {
	ModelID   string
	Source    string
	Kind      ImportSourceKind
	Overwrite bool
}

// Import copies a prepared local source into the managed model store and writes a fresh manifest.
func (m *Manager) Import(opts ImportOptions) (*LocalModel, error) {
	namespace, name, err := parseSafeLocalModelID(opts.ModelID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.Source) == "" {
		return nil, fmt.Errorf("source is required")
	}

	stagingParent := m.cfg.TempDir()
	if err := os.MkdirAll(stagingParent, 0o755); err != nil {
		return nil, fmt.Errorf("creating staging parent: %w", err)
	}
	staging, err := os.MkdirTemp(stagingParent, ".csghub-model-import-*")
	if err != nil {
		return nil, fmt.Errorf("creating staging dir: %w", err)
	}
	defer os.RemoveAll(staging)

	sourceDir := opts.Source
	if opts.Kind == ImportSourceArchive {
		sourceDir = filepath.Join(staging, "archive")
		if err := os.MkdirAll(sourceDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating archive staging dir: %w", err)
		}
		if err := extractModelArchive(opts.Source, sourceDir); err != nil {
			return nil, err
		}
	}
	if opts.Kind != ImportSourceArchive && opts.Kind != ImportSourceDirectory {
		return nil, fmt.Errorf("unsupported import source kind %q", opts.Kind)
	}

	contentRoot, err := modelContentRoot(sourceDir)
	if err != nil {
		return nil, err
	}

	preparedDir := filepath.Join(staging, "prepared")
	if err := copyModelTree(contentRoot, preparedDir); err != nil {
		return nil, fmt.Errorf("copying model files: %w", err)
	}
	if _, _, err := FindModelFile(preparedDir); err != nil {
		return nil, fmt.Errorf("no supported model weight file found")
	}

	lm := &LocalModel{
		Namespace:    namespace,
		Name:         name,
		DownloadedAt: time.Now(),
		PipelineTag:  DetectPipelineTag(preparedDir),
	}
	if changed, err := EnsureLocalModelFiles(preparedDir, lm); err != nil {
		return nil, fmt.Errorf("scanning model files: %w", err)
	} else if changed {
		lm.Format = DetectFormat(lm.Files)
		lm.Size = localModelEntriesSize(lm.FileEntries)
	}
	if lm.Format == FormatUnknown {
		return nil, fmt.Errorf("no supported model weight file found")
	}
	if err := SaveManifestInDir(preparedDir, lm); err != nil {
		return nil, fmt.Errorf("writing manifest: %w", err)
	}

	destDir := ModelDir(m.cfg.ModelDir, namespace, name)
	if err := installPreparedModel(preparedDir, destDir, opts.Overwrite); err != nil {
		return nil, err
	}
	return LoadManifest(m.cfg.ModelDir, namespace, name)
}

// ImportPreparedDirectory installs an already-staged directory into the managed
// model store without copying it through an additional prepared directory first.
func (m *Manager) ImportPreparedDirectory(opts ImportOptions) (*LocalModel, error) {
	namespace, name, err := parseSafeLocalModelID(opts.ModelID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.Source) == "" {
		return nil, fmt.Errorf("source is required")
	}
	if opts.Kind != ImportSourceDirectory {
		return nil, fmt.Errorf("unsupported import source kind %q", opts.Kind)
	}

	preparedDir, err := modelContentRoot(opts.Source)
	if err != nil {
		return nil, err
	}
	if _, _, err := FindModelFile(preparedDir); err != nil {
		return nil, fmt.Errorf("no supported model weight file found")
	}

	lm := &LocalModel{
		Namespace:    namespace,
		Name:         name,
		DownloadedAt: time.Now(),
		PipelineTag:  DetectPipelineTag(preparedDir),
	}
	if changed, err := EnsureLocalModelFiles(preparedDir, lm); err != nil {
		return nil, fmt.Errorf("scanning model files: %w", err)
	} else if changed {
		lm.Format = DetectFormat(lm.Files)
		lm.Size = localModelEntriesSize(lm.FileEntries)
	}
	if lm.Format == FormatUnknown {
		return nil, fmt.Errorf("no supported model weight file found")
	}
	if err := SaveManifestInDir(preparedDir, lm); err != nil {
		return nil, fmt.Errorf("writing manifest: %w", err)
	}

	destDir := ModelDir(m.cfg.ModelDir, namespace, name)
	if err := installPreparedModel(preparedDir, destDir, opts.Overwrite); err != nil {
		return nil, err
	}
	return LoadManifest(m.cfg.ModelDir, namespace, name)
}

func parseSafeLocalModelID(modelID string) (string, string, error) {
	namespace, name, err := csghub.ParseModelID(strings.TrimSpace(modelID))
	if err != nil {
		return "", "", err
	}
	if !safeModelIDSegment(namespace) || !safeModelIDSegment(name) {
		return "", "", fmt.Errorf("invalid model ID %q", modelID)
	}
	return namespace, name, nil
}

func safeModelIDSegment(segment string) bool {
	segment = strings.TrimSpace(segment)
	if segment == "" || segment == "." || segment == ".." {
		return false
	}
	if strings.ContainsAny(segment, `/\`) {
		return false
	}
	return filepath.Base(segment) == segment && path.Base(segment) == segment
}

func modelContentRoot(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("reading model source: %w", err)
	}
	var meaningful []fs.DirEntry
	for _, entry := range entries {
		name := entry.Name()
		if name == "__MACOSX" || name == ".DS_Store" {
			continue
		}
		meaningful = append(meaningful, entry)
	}
	if len(meaningful) == 1 && meaningful[0].IsDir() {
		return filepath.Join(root, meaningful[0].Name()), nil
	}
	return root, nil
}

func copyModelTree(src, dst string) error {
	return filepath.WalkDir(src, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not supported: %s", rel)
		}
		cleanRel := cleanLocalModelPath(rel)
		if cleanRel == "" {
			return fmt.Errorf("invalid path %q", rel)
		}
		if cleanRel == "manifest.json" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, filepath.FromSlash(cleanRel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type: %s", rel)
		}
		return copyFile(current, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func extractModelArchive(archivePath, dst string) error {
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZipArchive(archivePath, dst)
	case strings.HasSuffix(lower, ".tar"):
		return extractTarArchive(archivePath, dst, false)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarArchive(archivePath, dst, true)
	default:
		return fmt.Errorf("unsupported archive format")
	}
}

func extractZipArchive(archivePath, dst string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("opening zip archive: %w", err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not supported: %s", file.Name)
		}
		target, err := safeArchiveTarget(dst, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		if err := writeArchiveFile(rc, target, file.FileInfo().Mode().Perm()); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
	}
	return nil
}

func extractTarArchive(archivePath, dst string, gzipped bool) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("opening tar archive: %w", err)
	}
	defer f.Close()
	var reader io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("opening gzip archive: %w", err)
		}
		defer gz.Close()
		reader = gz
	}
	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar archive: %w", err)
		}
		target, err := safeArchiveTarget(dst, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := writeArchiveFile(tr, target, fs.FileMode(header.Mode).Perm()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported archive entry type: %s", header.Name)
		}
	}
	return nil
}

func safeArchiveTarget(root, rawPath string) (string, error) {
	relPath := cleanLocalModelPath(strings.ReplaceAll(rawPath, "\\", "/"))
	if relPath == "" {
		return "", fmt.Errorf("invalid archive path %q", rawPath)
	}
	target := filepath.Join(root, filepath.FromSlash(relPath))
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("invalid archive path %q", rawPath)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid archive path %q", rawPath)
	}
	return target, nil
}

func writeArchiveFile(src io.Reader, dst string, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, src); err != nil {
		return err
	}
	return out.Close()
}

func localModelEntriesSize(entries []LocalModelFile) int64 {
	var total int64
	for _, entry := range entries {
		total += entry.Size
	}
	return total
}

func installPreparedModel(preparedDir, destDir string, overwrite bool) error {
	if _, err := os.Stat(destDir); err == nil && !overwrite {
		return fmt.Errorf("model already exists")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return err
	}

	backupDir := ""
	if _, err := os.Stat(destDir); err == nil {
		backupDir = destDir + ".backup-" + time.Now().Format("20060102150405")
		if err := os.Rename(destDir, backupDir); err != nil {
			return fmt.Errorf("backing up existing model: %w", err)
		}
	}
	if err := os.Rename(preparedDir, destDir); err != nil {
		if backupDir != "" {
			_ = os.Rename(backupDir, destDir)
		}
		return fmt.Errorf("installing model: %w", err)
	}
	if backupDir != "" {
		_ = os.RemoveAll(backupDir)
	}
	return nil
}

// SaveManifestInDir writes a manifest to a prepared model directory before it is installed.
func SaveManifestInDir(modelDir string, m *LocalModel) error {
	normalizeLocalModel(m)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(modelDir, "manifest.json"), data, 0o644)
}
