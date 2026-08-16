package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/datasetexport"
	"github.com/opencsgs/csglite/internal/observability"
	"github.com/opencsgs/csglite/pkg/api"
)

func (s *Server) handleDatasetExportPreview(w http.ResponseWriter, r *http.Request) {
	var request api.DatasetExportRequest
	if err := decodeDatasetExportJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.observabilityMu.RLock()
	defer s.observabilityMu.RUnlock()
	if s.observability == nil {
		writeError(w, http.StatusServiceUnavailable, "observability store is unavailable")
		return
	}
	options, err := s.datasetExportOptions(r.Context(), request)
	if err != nil {
		writeDatasetExportError(w, err)
		return
	}
	preview, err := datasetexport.BuildPreview(r.Context(), s.observability, options)
	if err != nil {
		writeDatasetExportError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, datasetExportPreviewResponse(preview))
}

func (s *Server) handleDatasetExportCreate(w http.ResponseWriter, r *http.Request) {
	var request api.DatasetExportRequest
	if err := decodeDatasetExportJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	root, err := s.datasetExportStorageRoot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "dataset export storage is unavailable")
		return
	}
	if s.datasetExportJobs == nil {
		writeError(w, http.StatusServiceUnavailable, "dataset export jobs are unavailable")
		return
	}
	s.observabilityMu.RLock()
	if s.observability == nil {
		s.observabilityMu.RUnlock()
		writeError(w, http.StatusServiceUnavailable, "observability store is unavailable")
		return
	}
	options, err := s.datasetExportOptions(r.Context(), request)
	s.observabilityMu.RUnlock()
	if err != nil {
		writeDatasetExportError(w, err)
		return
	}
	job := s.datasetExportJobs.Start(func() (datasetexport.Artifact, error) {
		s.observabilityMu.RLock()
		defer s.observabilityMu.RUnlock()
		if s.observability == nil {
			return datasetexport.Artifact{}, errors.New("observability store is unavailable")
		}
		return datasetexport.Export(context.Background(), s.observability, root, s.cfg.DatasetDir, options)
	})
	writeJSON(w, http.StatusAccepted, datasetExportJobResponse(job))
}

func (s *Server) handleDatasetExportJob(w http.ResponseWriter, r *http.Request) {
	if s.datasetExportJobs == nil {
		writeError(w, http.StatusServiceUnavailable, "dataset export jobs are unavailable")
		return
	}
	job, ok := s.datasetExportJobs.Get(r.PathValue("jobID"))
	if !ok {
		writeError(w, http.StatusNotFound, "dataset export job not found")
		return
	}
	writeJSON(w, http.StatusOK, datasetExportJobResponse(job))
}

func (s *Server) handleDatasetExportDownload(w http.ResponseWriter, r *http.Request) {
	root, err := s.datasetExportStorageRoot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "dataset export storage is unavailable")
		return
	}
	artifact, err := datasetexport.LoadArtifact(root, s.cfg.DatasetDir, r.PathValue("exportID"))
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "dataset export not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	namespace, name, ok := strings.Cut(artifact.DatasetID, "/")
	if !ok {
		writeError(w, http.StatusInternalServerError, "dataset export index is invalid")
		return
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/api/datasets/%s/%s/export", url.PathEscape(namespace), url.PathEscape(name)),
		http.StatusTemporaryRedirect,
	)
}

func (s *Server) datasetExportStorageRoot() (string, error) {
	root := strings.TrimSpace(s.cfg.StorageDir())
	if root != "" {
		return root, nil
	}
	return config.DefaultStorageDir()
}

func decodeDatasetExportJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("request body must contain one JSON object")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

func (s *Server) datasetExportOptions(ctx context.Context, request api.DatasetExportRequest) (datasetexport.Options, error) {
	options := datasetexport.Options{
		TraceIDs:        request.TraceIDs,
		Format:          request.Format,
		RedactionPolicy: request.RedactionPolicy,
		Confirmed:       request.Confirmed,
		DatasetName:     request.DatasetName,
	}
	if len(request.TraceIDs) > 0 && request.Filter != nil {
		return options, errors.New("trace_ids and filter cannot be used together")
	}
	if request.Filter == nil {
		return options, nil
	}
	if request.Filter.From != nil && request.Filter.To != nil && request.Filter.From.After(*request.Filter.To) {
		return options, errors.New("filter.from must be before or equal to filter.to")
	}
	filter := observability.RequestFilter{
		From:   request.Filter.From,
		To:     request.Filter.To,
		Status: strings.TrimSpace(request.Filter.Status),
		Model:  strings.TrimSpace(request.Filter.Model),
		Source: strings.TrimSpace(request.Filter.Source),
		Query:  strings.TrimSpace(request.Filter.Query),
	}
	traceIDs, err := s.observability.ListTraceIDs(ctx, filter)
	if err != nil {
		return options, err
	}
	if len(traceIDs) == 0 {
		return options, errors.New("no traces match the export filter")
	}
	options.TraceIDs = traceIDs
	return options, nil
}

func datasetExportPreviewResponse(preview datasetexport.Preview) api.DatasetExportPreviewResponse {
	return api.DatasetExportPreviewResponse{
		Selected: preview.Selected,
		Exported: preview.Exported,
		Excluded: preview.Excluded,
		Degraded: preview.Degraded,
		Risks:    datasetExportRisks(preview.Risks),
		Sample:   preview.Sample,
	}
}

func datasetExportResponse(artifact datasetexport.Artifact) api.DatasetExportResponse {
	return api.DatasetExportResponse{
		ID:          artifact.ID,
		DatasetID:   artifact.DatasetID,
		Format:      artifact.Format,
		CreatedAt:   artifact.CreatedAt,
		Selected:    artifact.Selected,
		Exported:    artifact.Exported,
		Excluded:    artifact.Excluded,
		Degraded:    artifact.Degraded,
		Risks:       datasetExportRisks(artifact.Risks),
		Files:       datasetExportFiles(artifact.Files),
		DownloadURL: "/api/observability/dataset-exports/" + artifact.ID + "/download",
	}
}

func datasetExportJobResponse(job datasetExportJob) api.DatasetExportJobResponse {
	response := api.DatasetExportJobResponse{
		ID:        job.ID,
		Status:    job.Status,
		CreatedAt: job.CreatedAt,
		UpdatedAt: job.UpdatedAt,
		Error:     job.Error,
	}
	if job.Artifact != nil {
		export := datasetExportResponse(*job.Artifact)
		response.Export = &export
	}
	return response
}

func datasetExportRisks(risks []datasetexport.RiskSummary) []api.DatasetExportRisk {
	result := make([]api.DatasetExportRisk, 0, len(risks))
	for _, risk := range risks {
		result = append(result, api.DatasetExportRisk{Type: risk.Type, Count: risk.Count})
	}
	return result
}

func datasetExportFiles(files []datasetexport.File) []api.DatasetExportFile {
	result := make([]api.DatasetExportFile, 0, len(files))
	for _, file := range files {
		result = append(result, api.DatasetExportFile{Path: file.Path, Size: file.Size, SHA256: file.SHA256})
	}
	return result
}

func writeDatasetExportError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "trace not found")
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}
