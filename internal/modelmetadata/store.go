package modelmetadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	DirName      = "cache"
	DatabaseFile = "model-metadata.db"
)

type Metadata struct {
	PipelineTag   string
	HasMMProj     bool
	ContextWindow int64
	MaxModelLen   int64
}

type Store struct {
	db   *sql.DB
	path string
}

func Open(storageRoot string) (*Store, error) {
	if strings.TrimSpace(storageRoot) == "" {
		return nil, errors.New("model metadata storage root is required")
	}
	dir := filepath.Join(filepath.Clean(storageRoot), DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating model metadata cache directory: %w", err)
	}
	path := filepath.Join(dir, DatabaseFile)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening model metadata cache: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db, path: path}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) init() error {
	const schema = `
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS model_metadata (
	storage_id TEXT PRIMARY KEY,
	fingerprint TEXT NOT NULL,
	pipeline_tag TEXT NOT NULL DEFAULT '',
	has_mmproj INTEGER NOT NULL DEFAULT 0,
	context_window INTEGER NOT NULL DEFAULT 0,
	max_model_len INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL
);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("initializing model metadata cache: %w", err)
	}
	return nil
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) Get(ctx context.Context, storageID, fingerprint string) (Metadata, bool, error) {
	var metadata Metadata
	var hasMMProj int
	err := s.db.QueryRowContext(ctx, `
SELECT pipeline_tag, has_mmproj, context_window, max_model_len
FROM model_metadata
WHERE storage_id = ? AND fingerprint = ?
`, storageID, fingerprint).Scan(
		&metadata.PipelineTag,
		&hasMMProj,
		&metadata.ContextWindow,
		&metadata.MaxModelLen,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Metadata{}, false, nil
	}
	if err != nil {
		return Metadata{}, false, fmt.Errorf("reading model metadata cache: %w", err)
	}
	metadata.HasMMProj = hasMMProj != 0
	return metadata, true, nil
}

func (s *Store) Put(ctx context.Context, storageID, fingerprint string, metadata Metadata) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO model_metadata (
	storage_id, fingerprint, pipeline_tag, has_mmproj,
	context_window, max_model_len, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(storage_id) DO UPDATE SET
	fingerprint = excluded.fingerprint,
	pipeline_tag = excluded.pipeline_tag,
	has_mmproj = excluded.has_mmproj,
	context_window = excluded.context_window,
	max_model_len = excluded.max_model_len,
	updated_at = excluded.updated_at
`,
		storageID,
		fingerprint,
		metadata.PipelineTag,
		metadata.HasMMProj,
		metadata.ContextWindow,
		metadata.MaxModelLen,
		time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("writing model metadata cache: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Fingerprint hashes file identity metadata without reading model contents.
// Any add, remove, resize, or modification invalidates the cached derivations.
func Fingerprint(modelDir string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(modelDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(modelDir, path)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(
			hash,
			"%s\x00%d\x00%d\x00%d\n",
			filepath.ToSlash(relative),
			info.Size(),
			info.ModTime().UnixNano(),
			info.Mode(),
		)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("fingerprinting model directory: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
