package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestHandleSettingsDirectories(t *testing.T) {
	s := newTestServer(t)

	baseDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(baseDir, "beta"), 0o755); err != nil {
		t.Fatalf("mkdir beta: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(baseDir, "Alpha"), 0o755); err != nil {
		t.Fatalf("mkdir Alpha: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "notes.txt"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}

	body, err := json.Marshal(api.DirectoryBrowseRequest{Path: baseDir})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/directories", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleSettingsDirectories(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.DirectoryBrowseResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	wantPath, err := filepath.Abs(baseDir)
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	if resp.CurrentPath != wantPath {
		t.Fatalf("current_path = %q, want %q", resp.CurrentPath, wantPath)
	}
	if resp.ParentPath != filepath.Dir(wantPath) {
		t.Fatalf("parent_path = %q, want %q", resp.ParentPath, filepath.Dir(wantPath))
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(resp.Entries))
	}
	if resp.Entries[0].Name != "Alpha" || resp.Entries[1].Name != "beta" {
		t.Fatalf("entries = %#v, want Alpha and beta only", resp.Entries)
	}
	if len(resp.Roots) == 0 {
		t.Fatal("expected at least one root entry")
	}
}

func TestHandleSettingsDirectories_RejectsFiles(t *testing.T) {
	s := newTestServer(t)

	filePath := filepath.Join(t.TempDir(), "model.bin")
	if err := os.WriteFile(filePath, []byte("model"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	body, err := json.Marshal(api.DirectoryBrowseRequest{Path: filePath})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/directories", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleSettingsDirectories(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp apiErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ErrorCode != http.StatusBadRequest {
		t.Fatalf("errorCode = %d, want %d", resp.ErrorCode, http.StatusBadRequest)
	}
	if !strings.Contains(resp.Error, "not a directory") {
		t.Fatalf("error = %q, want not a directory", resp.Error)
	}
}
