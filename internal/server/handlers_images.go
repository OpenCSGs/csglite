package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/imagegen"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/pkg/api"
)

const maxImageUploadMemory = 64 << 20
const maxImageFormFieldBytes = 1 << 20

type imageInferenceRequest struct {
	api.OpenAIImagesGenerationRequest
	images [][]byte
	mask   []byte
}

type imageGenerateError struct {
	err error
}

func (e *imageGenerateError) Error() string {
	if e.err == nil {
		return "image generation failed"
	}
	return e.err.Error()
}

// POST /v1/images/generations -- OpenAI-compatible text-to-image generation (JSON).
func (s *Server) handleOpenAIImagesGenerations(w http.ResponseWriter, r *http.Request) {
	var req api.OpenAIImagesGenerationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body")
		return
	}
	if errMsg := normalizeOpenAIImagesGenerationRequest(&req); errMsg != "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", errMsg)
		return
	}
	if strings.TrimSpace(req.Image) != "" || len(req.Images) > 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "image editing is not supported on /v1/images/generations; use POST /v1/images/edits")
		return
	}

	resp, err := s.runOpenAIImageInference(r, imageInferenceRequest{OpenAIImagesGenerationRequest: req})
	if err != nil {
		writeOpenAIImageInferenceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /v1/images/edits -- OpenAI-compatible image editing (multipart/form-data).
func (s *Server) handleOpenAIImagesEdits(w http.ResponseWriter, r *http.Request) {
	inferenceReq, errMsg := s.parseOpenAIImagesEditRequest(r)
	if errMsg != "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", errMsg)
		return
	}

	resp, err := s.runOpenAIImageInference(r, inferenceReq)
	if err != nil {
		writeOpenAIImageInferenceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) runOpenAIImageInference(r *http.Request, inferenceReq imageInferenceRequest) (*api.OpenAIImagesGenerationResponse, error) {
	req := inferenceReq.toWorkerRequest()
	if imageGenerationUsesCloud(req) {
		var resp *api.OpenAIImagesGenerationResponse
		var err error
		if len(inferenceReq.images) > 0 {
			resp, err = s.generateCloudImageEdit(r.Context(), inferenceReq)
		} else {
			resp, err = s.generateCloudImage(r.Context(), req)
		}
		if err != nil {
			return nil, err
		}
		if resp.Created == 0 {
			resp.Created = time.Now().Unix()
		}
		s.recordAPIUsage(r, req.Model, req.Source, 0, 0)
		return resp, nil
	}

	eng, err := s.getOrLoadImageEngine(r.Context(), req.Model)
	if err != nil {
		return nil, err
	}
	resp, err := eng.Generate(r.Context(), req)
	if err != nil {
		return nil, &imageGenerateError{err: err}
	}
	if resp.Created == 0 {
		resp.Created = time.Now().Unix()
	}

	s.mu.Lock()
	if me, ok := s.imageEngines[req.Model]; ok {
		me.lastUsed = time.Now()
	}
	s.mu.Unlock()
	return resp, nil
}

func writeOpenAIImageInferenceError(w http.ResponseWriter, err error) {
	if status, ok := imagegen.RuntimeStatusFromError(err); ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"errorCode": http.StatusServiceUnavailable,
			"error": map[string]interface{}{
				"message": err.Error(),
				"type":    "runtime_not_ready",
			},
			"runtime": status,
		})
		return
	}
	var generateErr *imageGenerateError
	if errors.As(err, &generateErr) {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", generateErr.Error())
		return
	}
	if inference.HTTPStatusCode(err) != 0 {
		writeOpenAIInferenceError(w, err)
		return
	}
	writeOpenAIError(w, http.StatusBadRequest, "model_not_found", err.Error())
}

func (r imageInferenceRequest) toWorkerRequest() api.OpenAIImagesGenerationRequest {
	req := r.OpenAIImagesGenerationRequest
	if len(r.images) == 0 {
		return req
	}
	req.Image = base64.StdEncoding.EncodeToString(r.images[0])
	if len(r.images) > 1 {
		req.Images = make([]string, 0, len(r.images)-1)
		for _, image := range r.images[1:] {
			req.Images = append(req.Images, base64.StdEncoding.EncodeToString(image))
		}
	}
	return req
}

func (s *Server) parseOpenAIImagesEditRequest(r *http.Request) (imageInferenceRequest, string) {
	form, files, err := s.readImageEditMultipart(r)
	if err != nil {
		return imageInferenceRequest{}, "invalid multipart request: " + err.Error()
	}

	out := imageInferenceRequest{
		OpenAIImagesGenerationRequest: api.OpenAIImagesGenerationRequest{
			Model:          strings.TrimSpace(form.Get("model")),
			Prompt:         strings.TrimSpace(form.Get("prompt")),
			Size:           strings.TrimSpace(form.Get("size")),
			ResponseFormat: strings.TrimSpace(form.Get("response_format")),
			NegativePrompt: strings.TrimSpace(form.Get("negative_prompt")),
			Source:         strings.TrimSpace(form.Get("source")),
		},
	}

	if out.Model == "" {
		return imageInferenceRequest{}, "model is required"
	}
	if out.Prompt == "" {
		return imageInferenceRequest{}, "prompt is required"
	}
	if out.ResponseFormat == "" {
		out.ResponseFormat = "b64_json"
	}
	if out.ResponseFormat != "b64_json" && out.ResponseFormat != "url" {
		return imageInferenceRequest{}, "response_format must be b64_json or url"
	}
	if value := strings.TrimSpace(form.Get("n")); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 4 {
			return imageInferenceRequest{}, "n must be between 1 and 4"
		}
		out.N = &n
	}
	if value := strings.TrimSpace(form.Get("seed")); value != "" {
		seed, err := strconv.Atoi(value)
		if err != nil {
			return imageInferenceRequest{}, "seed must be an integer"
		}
		out.Seed = &seed
	}
	if value := strings.TrimSpace(form.Get("steps")); value != "" {
		steps, err := strconv.Atoi(value)
		if err != nil || steps < 1 {
			return imageInferenceRequest{}, "steps must be a positive integer"
		}
		out.Steps = &steps
	}
	if value := strings.TrimSpace(form.Get("cfg_scale")); value != "" {
		cfgScale, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return imageInferenceRequest{}, "cfg_scale must be a number"
		}
		out.CFGScale = &cfgScale
	}

	images, errMsg := readMultipartImageFiles(files, "image", "image[]")
	if errMsg != "" {
		return imageInferenceRequest{}, errMsg
	}
	if len(images) == 0 {
		return imageInferenceRequest{}, "image is required"
	}
	out.images = images

	maskFiles, errMsg := readMultipartImageFiles(files, "mask")
	if errMsg != "" {
		return imageInferenceRequest{}, errMsg
	}
	if len(maskFiles) > 1 {
		return imageInferenceRequest{}, "only one mask image is supported"
	}
	if len(maskFiles) == 1 {
		out.mask = maskFiles[0]
	}

	return out, ""
}

func (s *Server) readImageEditMultipart(r *http.Request) (url.Values, map[string][][]byte, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, nil, err
	}
	form := url.Values{}
	files := map[string][][]byte{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		name := part.FormName()
		if name == "" {
			_ = part.Close()
			continue
		}
		if part.FileName() == "" {
			data, err := io.ReadAll(io.LimitReader(part, maxImageFormFieldBytes+1))
			_ = part.Close()
			if err != nil {
				return nil, nil, err
			}
			if len(data) > maxImageFormFieldBytes {
				return nil, nil, fmt.Errorf("form field %s is too large", name)
			}
			form.Add(name, string(data))
			continue
		}
		data, err := s.cacheImageEditPart(part)
		_ = part.Close()
		if err != nil {
			return nil, nil, err
		}
		files[name] = append(files[name], data)
	}
	return form, files, nil
}

func (s *Server) cacheImageEditPart(part interface {
	FileName() string
	io.Reader
}) ([]byte, error) {
	tmpDir := s.cfg.TempDir()
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, err
	}
	ext := filepath.Ext(part.FileName())
	if ext == "" {
		ext = ".image"
	}
	tmp, err := os.CreateTemp(tmpDir, "image-edit-*"+ext)
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	written, copyErr := io.Copy(tmp, io.LimitReader(part, maxImageUploadMemory+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if written > maxImageUploadMemory {
		return nil, fmt.Errorf("uploaded image %s is too large", part.FileName())
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func readMultipartImageFiles(files map[string][][]byte, keys ...string) ([][]byte, string) {
	var images [][]byte
	for _, key := range keys {
		for _, data := range files[key] {
			if len(data) == 0 {
				return nil, "uploaded image is empty"
			}
			images = append(images, data)
		}
	}
	return images, ""
}

func normalizeOpenAIImagesGenerationRequest(req *api.OpenAIImagesGenerationRequest) string {
	req.Model = strings.TrimSpace(req.Model)
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Model == "" {
		return "model is required"
	}
	if req.Prompt == "" {
		return "prompt is required"
	}
	if req.N != nil && (*req.N < 1 || *req.N > 4) {
		return "n must be between 1 and 4"
	}
	if req.ResponseFormat != "" && req.ResponseFormat != "b64_json" {
		return "only response_format=b64_json is supported for local image generation"
	}
	return ""
}

func normalizeOpenAIImagesEditRequest(req *api.OpenAIImagesGenerationRequest) string {
	req.Model = strings.TrimSpace(req.Model)
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Model == "" {
		return "model is required"
	}
	if req.Prompt == "" {
		return "prompt is required"
	}
	if strings.TrimSpace(req.Image) == "" && len(req.Images) == 0 {
		return "image is required"
	}
	if req.N != nil && (*req.N < 1 || *req.N > 4) {
		return "n must be between 1 and 4"
	}
	if req.ResponseFormat != "" && req.ResponseFormat != "b64_json" {
		return "only response_format=b64_json is supported for local image editing"
	}
	return ""
}

func imageGenerationUsesCloud(req api.OpenAIImagesGenerationRequest) bool {
	return strings.EqualFold(strings.TrimSpace(req.Source), "cloud")
}

func (s *Server) generateCloudImage(ctx context.Context, req api.OpenAIImagesGenerationRequest) (*api.OpenAIImagesGenerationResponse, error) {
	apiKey, err := s.cloudAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	req.Model = s.resolveCloudOriginalModelID(req.Model)
	baseURL := resolveCloudURL(s.cfg)
	if s.cloud != nil && strings.TrimSpace(s.cloud.BaseURL()) != "" {
		baseURL = s.cloud.BaseURL()
	}

	body, err := json.Marshal(cloudImageGenerationRequest(req))
	if err != nil {
		return nil, fmt.Errorf("marshaling cloud image request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/images/generations", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating cloud image request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("cloud image generation request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading cloud image response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, inference.NewHTTPStatusError(resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var out api.OpenAIImagesGenerationResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decoding cloud image response: %w", err)
	}
	return &out, nil
}

func (s *Server) generateCloudImageEdit(ctx context.Context, req imageInferenceRequest) (*api.OpenAIImagesGenerationResponse, error) {
	apiKey, err := s.cloudAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	req.Model = s.resolveCloudOriginalModelID(req.Model)
	baseURL := resolveCloudURL(s.cfg)
	if s.cloud != nil && strings.TrimSpace(s.cloud.BaseURL()) != "" {
		baseURL = s.cloud.BaseURL()
	}

	body, contentType, err := buildCloudImageEditBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/images/edits", body)
	if err != nil {
		return nil, fmt.Errorf("creating cloud image edit request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("cloud image edit request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading cloud image edit response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, inference.NewHTTPStatusError(resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var out api.OpenAIImagesGenerationResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decoding cloud image edit response: %w", err)
	}
	return &out, nil
}

func buildCloudImageEditBody(req imageInferenceRequest) (io.Reader, string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("model", req.Model); err != nil {
		return nil, "", err
	}
	if err := writer.WriteField("prompt", req.Prompt); err != nil {
		return nil, "", err
	}
	if req.N != nil {
		if err := writer.WriteField("n", strconv.Itoa(*req.N)); err != nil {
			return nil, "", err
		}
	}
	if strings.TrimSpace(req.Size) != "" {
		if err := writer.WriteField("size", strings.TrimSpace(req.Size)); err != nil {
			return nil, "", err
		}
	}
	if strings.TrimSpace(req.ResponseFormat) != "" {
		if err := writer.WriteField("response_format", strings.TrimSpace(req.ResponseFormat)); err != nil {
			return nil, "", err
		}
	}
	for i, image := range req.images {
		filename := fmt.Sprintf("image-%d.png", i+1)
		part, err := writer.CreateFormFile("image", filename)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(image); err != nil {
			return nil, "", err
		}
	}
	if len(req.mask) > 0 {
		part, err := writer.CreateFormFile("mask", "mask.png")
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(req.mask); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return bytes.NewReader(buf.Bytes()), writer.FormDataContentType(), nil
}

func cloudImageGenerationRequest(req api.OpenAIImagesGenerationRequest) map[string]interface{} {
	body := map[string]interface{}{
		"model":  req.Model,
		"prompt": req.Prompt,
	}
	if req.N != nil {
		body["n"] = *req.N
	}
	if strings.TrimSpace(req.Size) != "" {
		body["size"] = strings.TrimSpace(req.Size)
	}
	if strings.TrimSpace(req.ResponseFormat) != "" {
		body["response_format"] = strings.TrimSpace(req.ResponseFormat)
	}
	return body
}

// GET /api/image-runtime -- report the lazy Diffusers runtime status.
func (s *Server) handleImageRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	manager, err := imagegen.NewRuntimeManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, manager.Status(r.Context()))
}

// POST /api/image-runtime/install -- install or repair the Diffusers runtime.
func (s *Server) handleImageRuntimeInstall(w http.ResponseWriter, r *http.Request) {
	var req api.ImageRuntimeInstallRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	manager, err := imagegen.NewRuntimeManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status, err := manager.InstallWithProgressOptions(r.Context(), nil, req.UpgradePackages)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":  err.Error(),
			"status": status,
		})
		return
	}
	writeJSON(w, http.StatusOK, status)
}
