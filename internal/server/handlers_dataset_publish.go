package server

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/pkg/api"
)

const agenticHubURL = "https://opencsg.com/agentichub"

var datasetRepositoryPartPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func (s *Server) handleLocalDatasetExport(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPathValues(r)
	local, err := s.datasetManager.GetWithFileEntries(datasetID)
	if err != nil {
		writeError(w, http.StatusNotFound, "local dataset not found")
		return
	}
	root, err := s.datasetManager.DatasetPath(datasetID)
	if err != nil {
		writeError(w, http.StatusNotFound, "local dataset not found")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, local.Name))
	archive := zip.NewWriter(w)
	for _, file := range local.FileEntries {
		localPath, err := safeLocalDatasetFile(root, file.Path)
		if err != nil {
			_ = archive.Close()
			return
		}
		source, err := os.Open(localPath)
		if err != nil {
			_ = archive.Close()
			return
		}
		entry, err := archive.Create(filepath.ToSlash(file.Path))
		if err == nil {
			_, err = io.Copy(entry, source)
		}
		_ = source.Close()
		if err != nil {
			_ = archive.Close()
			return
		}
	}
	_ = archive.Close()
}

func (s *Server) handleLocalDatasetPublish(w http.ResponseWriter, r *http.Request) {
	localID := datasetIDFromPathValues(r)
	local, err := s.datasetManager.GetWithFileEntries(localID)
	if err != nil {
		writeError(w, http.StatusNotFound, "local dataset not found")
		return
	}
	var request api.DatasetPublishRequest
	if err := decodeDatasetExportJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" {
		request.Name = local.Name
	}
	if !datasetRepositoryPartPattern.MatchString(request.Name) {
		writeError(w, http.StatusBadRequest, "dataset name must use letters, numbers, dots, underscores, or hyphens")
		return
	}
	if !request.Private && !request.ConfirmPublic {
		writeError(w, http.StatusBadRequest, "public dataset upload requires explicit confirmation")
		return
	}
	token := strings.TrimSpace(s.cfg.Token)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "sign in to CSGHub before uploading a dataset")
		return
	}
	client := csghub.NewClient(s.cfg.ServerURL, token)
	user, err := client.GetCurrentUser(r.Context())
	if err != nil || user == nil {
		writeError(w, http.StatusUnauthorized, "failed to resolve the current CSGHub user")
		return
	}
	namespace := strings.TrimSpace(user.Username)
	if !datasetRepositoryPartPattern.MatchString(namespace) {
		writeError(w, http.StatusUnauthorized, "current CSGHub username is unavailable")
		return
	}
	if request.Create {
		remote, createErr := client.CreateDataset(r.Context(), csghub.CreateDatasetRequest{
			Namespace:     namespace,
			Name:          request.Name,
			Nickname:      strings.TrimSpace(request.Nickname),
			Description:   firstNonEmpty(strings.TrimSpace(request.Description), local.Description),
			Private:       request.Private,
			License:       firstNonEmpty(strings.TrimSpace(request.License), local.License),
			DefaultBranch: "main",
		})
		if createErr != nil {
			writeError(w, http.StatusBadGateway, createErr.Error())
			return
		}
		if owner, _, ok := strings.Cut(remote.Path, "/"); ok {
			namespace = owner
		}
	}
	if namespace == "" {
		writeError(w, http.StatusBadGateway, "CSGHub did not return the created dataset namespace")
		return
	}
	root, err := s.datasetManager.DatasetPath(localID)
	if err != nil {
		writeError(w, http.StatusNotFound, "local dataset not found")
		return
	}
	files := make([]api.DatasetExportFile, 0, len(local.FileEntries))
	for _, file := range local.FileEntries {
		localPath, pathErr := safeLocalDatasetFile(root, file.Path)
		if pathErr != nil {
			writeError(w, http.StatusInternalServerError, pathErr.Error())
			return
		}
		if err := client.UploadDatasetFile(
			r.Context(), namespace, request.Name, localPath, filepath.ToSlash(file.Path), "main",
			"Upload CSGHub Lite local dataset",
		); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		files = append(files, api.DatasetExportFile{Path: filepath.ToSlash(file.Path), Size: file.Size, SHA256: file.SHA256})
	}
	displayURL := strings.TrimSuffix(s.cfg.DisplayURL(), "/")
	writeJSON(w, http.StatusOK, api.DatasetPublishResponse{
		DatasetID:     namespace + "/" + request.Name,
		Revision:      "main",
		URL:           fmt.Sprintf("%s/datasets/%s/%s", displayURL, namespace, request.Name),
		AgenticHubURL: agenticHubURL,
		Files:         files,
	})
}

func safeLocalDatasetFile(root, relative string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid local dataset file path %q", relative)
	}
	return target, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
