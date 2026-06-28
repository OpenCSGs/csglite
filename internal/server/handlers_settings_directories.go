package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/opencsgs/csglite/pkg/api"
)

// POST /api/settings/directories -- browse local directories for settings fields
func (s *Server) handleSettingsDirectories(w http.ResponseWriter, r *http.Request) {
	var req api.DirectoryBrowseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	targetPath := strings.TrimSpace(req.Path)
	if targetPath == "" {
		targetPath = s.cfg.StorageDir()
	}

	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot resolve directory %q: %v", targetPath, err))
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("directory %q not found", absPath))
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot access directory %q: %v", absPath, err))
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is not a directory", absPath))
		return
	}

	entries, err := listBrowsableDirectories(absPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot list directory %q: %v", absPath, err))
		return
	}

	homePath, _ := os.UserHomeDir()
	writeJSON(w, http.StatusOK, api.DirectoryBrowseResponse{
		CurrentPath: absPath,
		ParentPath:  parentDirectory(absPath),
		HomePath:    homePath,
		Roots:       availableDirectoryRoots(absPath, homePath),
		Entries:     entries,
	})
}

func listBrowsableDirectories(path string) ([]api.DirectoryEntry, error) {
	rawEntries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	entries := make([]api.DirectoryEntry, 0, len(rawEntries))
	for _, entry := range rawEntries {
		fullPath := filepath.Join(path, entry.Name())
		if !isBrowsableDirectory(fullPath, entry) {
			continue
		}
		entries = append(entries, api.DirectoryEntry{
			Name: entry.Name(),
			Path: fullPath,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		left := strings.ToLower(entries[i].Name)
		right := strings.ToLower(entries[j].Name)
		if left == right {
			return entries[i].Name < entries[j].Name
		}
		return left < right
	})

	return entries, nil
}

func isBrowsableDirectory(fullPath string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(fullPath)
	return err == nil && info.IsDir()
}

func parentDirectory(path string) string {
	cleaned := filepath.Clean(path)
	parent := filepath.Dir(cleaned)
	if parent == cleaned {
		return ""
	}
	return parent
}

func availableDirectoryRoots(paths ...string) []string {
	if runtime.GOOS != "windows" {
		return []string{string(filepath.Separator)}
	}

	var roots []string
	for letter := 'A'; letter <= 'Z'; letter++ {
		root := fmt.Sprintf("%c:%c", letter, filepath.Separator)
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			roots = append(roots, root)
		}
	}

	for _, path := range paths {
		root := volumeRoot(path)
		if root == "" || containsString(roots, root) {
			continue
		}
		roots = append(roots, root)
	}

	return roots
}

func volumeRoot(path string) string {
	volume := filepath.VolumeName(path)
	if volume == "" {
		return ""
	}
	return volume + string(filepath.Separator)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
