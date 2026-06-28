package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/dataset"
	"github.com/opencsgs/csglite/pkg/api"
)

// GET /api/datasets -- list local datasets
func (s *Server) handleDatasetTags(w http.ResponseWriter, r *http.Request) {
	datasets, err := s.datasetManager.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var infos []api.DatasetInfo
	for _, d := range datasets {
		infos = append(infos, localDatasetInfo(d))
	}

	writeJSON(w, http.StatusOK, api.DatasetTagsResponse{Datasets: infos})
}

// POST /api/datasets/show -- dataset details
func (s *Server) handleDatasetShow(w http.ResponseWriter, r *http.Request) {
	var req api.DatasetShowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ld, err := s.datasetManager.Get(req.Dataset)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dataset %q not found", req.Dataset))
		return
	}

	writeJSON(w, http.StatusOK, api.DatasetShowResponse{
		Details: localDatasetInfo(ld),
		Files:   ld.Files,
	})
}

// POST /api/datasets/pull -- download a dataset
func (s *Server) handleDatasetPull(w http.ResponseWriter, r *http.Request) {
	var req api.DatasetPullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var mu sync.Mutex
	safeSSE := func(v interface{}) {
		mu.Lock()
		writeSSE(w, v)
		mu.Unlock()
	}

	safeSSE(api.DatasetPullResponse{Status: "pulling " + req.Dataset})

	progress := func(p csghub.SnapshotProgress) {
		safeSSE(api.DatasetPullResponse{
			Status:    fmt.Sprintf("downloading %s", p.FileName),
			Digest:    p.FileName,
			Total:     p.BytesTotal,
			Completed: p.BytesCompleted,
		})
	}

	_, err := s.datasetManager.Pull(r.Context(), req.Dataset, progress)
	if err != nil {
		log.Printf("pull dataset %s failed: %v", req.Dataset, err)
		safeSSE(api.DatasetPullResponse{Status: "error: " + err.Error()})
		return
	}

	safeSSE(api.DatasetPullResponse{Status: "success"})
}

// POST /api/datasets/files -- browse files in a dataset directory
func (s *Server) handleDatasetFiles(w http.ResponseWriter, r *http.Request) {
	var req api.DatasetFilesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	entries, err := s.datasetManager.ListFiles(req.Dataset, req.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("cannot list files: %v", err))
		return
	}

	var apiEntries []api.DatasetFileEntry
	for _, e := range entries {
		apiEntries = append(apiEntries, api.DatasetFileEntry{
			Name:       e.Name,
			Size:       e.Size,
			IsDir:      e.IsDir,
			ModifiedAt: e.ModifiedAt,
		})
	}

	writeJSON(w, http.StatusOK, api.DatasetFilesResponse{
		Dataset: req.Dataset,
		Path:    req.Path,
		Entries: apiEntries,
	})
}

// DELETE /api/datasets/delete -- remove a dataset
func (s *Server) handleDatasetDelete(w http.ResponseWriter, r *http.Request) {
	var req api.DatasetDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.datasetManager.Remove(req.Dataset); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

const (
	defaultDatasetSearchLimit = 20
	maxDatasetSearchLimit     = 100
)

// GET /api/datasets/search -- search local datasets
func (s *Server) handleDatasetSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")

	query := strings.TrimSpace(r.URL.Query().Get("q"))

	limit, err := parseDatasetSearchLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	offset, err := parseDatasetSearchOffset(r.URL.Query().Get("offset"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	datasets, err := s.datasetManager.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	sort.SliceStable(datasets, func(i, j int) bool {
		left := strings.TrimSpace(datasets[i].FullName())
		right := strings.TrimSpace(datasets[j].FullName())
		if datasets[i].DownloadedAt.Equal(datasets[j].DownloadedAt) {
			return left < right
		}
		return datasets[i].DownloadedAt.After(datasets[j].DownloadedAt)
	})

	var filtered []api.DatasetInfo
	for _, d := range datasets {
		info := localDatasetInfo(d)
		if !matchesDatasetSearch(info, query) {
			continue
		}
		filtered = append(filtered, info)
	}

	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}

	writeJSON(w, http.StatusOK, api.DatasetSearchResponse{
		Query:    query,
		Limit:    limit,
		Offset:   offset,
		Total:    total,
		HasMore:  end < total,
		Datasets: filtered[offset:end],
	})
}

func matchesDatasetSearch(item api.DatasetInfo, query string) bool {
	query = strings.TrimSpace(query)
	if query == "" {
		return true
	}
	query = strings.ToLower(query)
	fields := []string{item.Name, item.Dataset, item.Origin}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), query) {
			return true
		}
	}
	return false
}

func parseDatasetSearchLimit(s string) (int, error) {
	if s == "" {
		return defaultDatasetSearchLimit, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("limit must be a positive integer, got %q", s)
	}
	if n > maxDatasetSearchLimit {
		n = maxDatasetSearchLimit
	}
	return n, nil
}

func parseDatasetSearchOffset(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("offset must be a non-negative integer, got %q", s)
	}
	return n, nil
}

func localDatasetInfo(d *dataset.LocalDataset) api.DatasetInfo {
	return api.DatasetInfo{
		Name:        d.FullName(),
		Dataset:     d.FullName(),
		Size:        d.Size,
		Files:       len(d.Files),
		ModifiedAt:  d.DownloadedAt,
		Origin:      string(d.Origin),
		Description: strings.TrimSpace(d.Description),
		License:     strings.TrimSpace(d.License),
	}
}
