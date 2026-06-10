package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csghub-lite/internal/csghub"
	"github.com/opencsgs/csghub-lite/internal/imagegen"
	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

type apiErrorResponse struct {
	Error     string `json:"error"`
	ErrorCode int    `json:"errorCode"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiErrorResponse{Error: msg, ErrorCode: status})
}

func writeInferenceError(w http.ResponseWriter, err error) {
	status := inference.HTTPStatusCode(err)
	if status == 0 {
		status = http.StatusInternalServerError
	}
	writeError(w, status, inference.HTTPErrorMessage(err))
}

func writeSSE(w http.ResponseWriter, v interface{}) {
	data, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func writeNDJSON(w http.ResponseWriter, v interface{}) {
	data, _ := json.Marshal(v)
	fmt.Fprintf(w, "%s\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func requestWantsSSE(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("X-CSGHUB-Stream"), "sse") {
		return true
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream")
}

func requestDisablesThinking(r *http.Request) bool {
	value := strings.TrimSpace(strings.ToLower(r.Header.Get("X-CSGHUB-Disable-Thinking")))
	return value == "1" || value == "true"
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GET /api/tags -- list available local, OpenCSG, and third-party provider models
func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	infos, err := s.listAvailableModelsWithRefresh(r.Context(), requestWantsModelRefresh(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	provider := normalizeModelProvider(r.URL.Query().Get("provider"))
	if provider != "" {
		filtered := make([]api.ModelInfo, 0, len(infos))
		for _, info := range infos {
			if !modelMatchesProvider(info, provider) {
				continue
			}
			filtered = append(filtered, info)
		}
		infos = filtered
	}

	var ok bool
	infos, ok = filterModelsByPipelineCategory(infos, r.URL.Query().Get("category"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid category")
		return
	}

	for i := range infos {
		if infos[i].Provider == "" {
			infos[i].Provider = modelProviderID(infos[i])
		}
		if infos[i].Category == "" {
			infos[i].Category = categoryForPipelineTag(infos[i].PipelineTag)
		}
	}

	writeJSON(w, http.StatusOK, api.TagsResponse{Models: infos})
}

// GET /api/pipeline-tags -- list supported Hugging Face pipeline tags by csghub-lite category
func (s *Server) handlePipelineTags(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.PipelineTagsResponse{PipelineTags: supportedPipelineTagGroups})
}

// GET /api/ps -- list running models
func (s *Server) handlePs(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	models := make([]api.RunningModel, 0, len(s.engines)+len(s.loading)+len(s.imageEngines)+len(s.imageLoading)+len(s.asrEngines)+len(s.asrLoading))
	for id, me := range s.engines {
		modelID := engineModelIDFromKey(id)
		lm, err := s.manager.Get(modelID)
		if err != nil {
			continue
		}
		models = append(models, api.RunningModel{
			Name:      s.localInferenceModelID(lm.FullName()),
			Model:     s.localInferenceModelID(lm.FullName()),
			Size:      lm.Size,
			Format:    string(lm.Format),
			Status:    "running",
			ExpiresAt: me.expiresAt(),
		})
	}
	for id := range s.loading {
		if _, ok := s.engines[id]; ok {
			continue
		}
		modelID := engineModelIDFromKey(id)
		lm, err := s.manager.Get(modelID)
		if err != nil {
			continue
		}
		models = append(models, api.RunningModel{
			Name:   s.localInferenceModelID(lm.FullName()),
			Model:  s.localInferenceModelID(lm.FullName()),
			Size:   lm.Size,
			Format: string(lm.Format),
			Status: "loading",
		})
	}
	for id, me := range s.imageEngines {
		lm, err := s.manager.Get(id)
		if err != nil {
			continue
		}
		models = append(models, api.RunningModel{
			Name:      s.localInferenceModelID(lm.FullName()),
			Model:     s.localInferenceModelID(lm.FullName()),
			Size:      lm.Size,
			Format:    string(lm.Format),
			Status:    "running",
			ExpiresAt: me.lastUsed.Add(me.keepAlive),
		})
	}
	for id := range s.imageLoading {
		if _, ok := s.imageEngines[id]; ok {
			continue
		}
		lm, err := s.manager.Get(id)
		if err != nil {
			continue
		}
		models = append(models, api.RunningModel{
			Name:   s.localInferenceModelID(lm.FullName()),
			Model:  s.localInferenceModelID(lm.FullName()),
			Size:   lm.Size,
			Format: string(lm.Format),
			Status: "loading",
		})
	}
	for id, me := range s.asrEngines {
		lm, err := s.manager.Get(id)
		if err != nil {
			continue
		}
		expiresAt := time.Time{}
		if me.keepAlive >= 0 {
			expiresAt = me.lastUsed.Add(me.keepAlive)
		}
		models = append(models, api.RunningModel{
			Name:      s.localInferenceModelID(lm.FullName()),
			Model:     s.localInferenceModelID(lm.FullName()),
			Size:      lm.Size,
			Format:    string(lm.Format),
			Status:    "running",
			ExpiresAt: expiresAt,
		})
	}
	for id := range s.asrLoading {
		if _, ok := s.asrEngines[id]; ok {
			continue
		}
		lm, err := s.manager.Get(id)
		if err != nil {
			continue
		}
		models = append(models, api.RunningModel{
			Name:   s.localInferenceModelID(lm.FullName()),
			Model:  s.localInferenceModelID(lm.FullName()),
			Size:   lm.Size,
			Format: string(lm.Format),
			Status: "loading",
		})
	}

	writeJSON(w, http.StatusOK, api.PsResponse{Models: models})
}

// POST /api/stop -- unload a model
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	var req api.StopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	s.mu.Lock()
	stopped := false
	modelID := s.resolveLocalModelStorageID(req.Model)
	for _, key := range []string{engineCacheKey(modelID, engineModeChat), engineCacheKey(modelID, engineModeEmbed)} {
		if me, ok := s.engines[key]; ok {
			me.engine.Close()
			delete(s.engines, key)
			stopped = true
		}
	}
	if me, ok := s.imageEngines[modelID]; ok {
		me.engine.Close()
		delete(s.imageEngines, modelID)
		stopped = true
	}
	if me, ok := s.asrEngines[modelID]; ok {
		me.engine.Close()
		delete(s.asrEngines, modelID)
		stopped = true
	}
	s.mu.Unlock()

	if !stopped {
		writeError(w, http.StatusNotFound, fmt.Sprintf("model %q is not running", req.Model))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// POST /api/show -- model details
func (s *Server) handleShow(w http.ResponseWriter, r *http.Request) {
	var req api.ShowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	lm, err := s.manager.ResolveLocalModel(req.Model)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", req.Model))
		return
	}

	writeJSON(w, http.StatusOK, api.ShowResponse{
		Details: s.localModelInfo(lm),
	})
}

// POST /api/pull -- download a model
func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	var req api.PullRequest
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

	safeSSE(api.PullResponse{Status: "pulling " + req.Model})
	log.Printf("MODEL %s: pull started", req.Model)

	lastProgressLog := time.Time{}
	progress := func(p csghub.SnapshotProgress) {
		safeSSE(api.PullResponse{
			Status:         fmt.Sprintf("downloading %s", p.FileName),
			Digest:         p.FileName,
			Total:          p.BytesTotal,
			Completed:      p.BytesCompleted,
			TotalBytes:     p.BytesTotalAll,
			CompletedBytes: p.BytesCompletedAll,
		})
		if time.Since(lastProgressLog) >= 5*time.Second || (p.BytesTotal > 0 && p.BytesCompleted >= p.BytesTotal) {
			log.Printf("MODEL %s: pulling file=%s completed=%d total=%d", req.Model, p.FileName, p.BytesCompleted, p.BytesTotal)
			lastProgressLog = time.Now()
		}
	}

	_, err := s.manager.Pull(r.Context(), req.Model, strings.TrimSpace(req.Quant), progress)
	if err != nil {
		log.Printf("pull %s failed: %v", req.Model, err)
		safeSSE(api.PullResponse{Status: "error: " + err.Error()})
		return
	}

	log.Printf("MODEL %s: pull complete", req.Model)
	safeSSE(api.PullResponse{Status: "success"})
}

// DELETE /api/delete -- remove a model
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req api.DeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	modelID := s.resolveLocalModelStorageID(req.Model)

	// Close engine if running
	s.mu.Lock()
	for _, key := range []string{engineCacheKey(modelID, engineModeChat), engineCacheKey(modelID, engineModeEmbed)} {
		if me, ok := s.engines[key]; ok {
			me.engine.Close()
			delete(s.engines, key)
		}
	}
	s.mu.Unlock()

	if err := s.manager.Remove(modelID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// POST /api/load -- eagerly load (and convert if necessary) a model
func (s *Server) handleLoad(w http.ResponseWriter, r *http.Request) {
	var req api.LoadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	requestedNumCtx := 0
	requestedNumParallel := 0
	requestedNGPULayers := -1
	requestedCacheTypeK := ""
	requestedCacheTypeV := ""
	requestedDType := ""
	requestedKeepAlive, keepAliveSet, err := api.ParseKeepAlive(req.KeepAlive)
	if err != nil {
		writeError(w, http.StatusBadRequest, "keep_alive "+err.Error())
		return
	}
	if req.NumCtx > 0 {
		requestedNumCtx = req.NumCtx
	}
	if req.NumParallel > 0 {
		requestedNumParallel = req.NumParallel
	}
	if req.NGPULayers != nil {
		requestedNGPULayers = *req.NGPULayers
	}
	if req.CacheTypeK != "" {
		requestedCacheTypeK = req.CacheTypeK
	}
	if req.CacheTypeV != "" {
		requestedCacheTypeV = req.CacheTypeV
	}
	if req.DType != "" {
		requestedDType = req.DType
	}
	embeddingModel := s.modelUsesEmbeddingEngine(req.Model)
	imageGenerationModel := s.modelUsesImageGenerationEngine(req.Model)
	asrModel := s.modelUsesASREngine(req.Model)

	stream := req.Stream != nil && *req.Stream

	if !stream {
		log.Printf("MODEL %s: load requested stream=false num_ctx=%d num_parallel=%d n_gpu_layers=%d cache_type_k=%q cache_type_v=%q dtype=%q", req.Model, requestedNumCtx, requestedNumParallel, requestedNGPULayers, requestedCacheTypeK, requestedCacheTypeV, requestedDType)
		var err error
		if imageGenerationModel {
			_, err = s.getOrLoadImageEngine(context.Background(), req.Model)
		} else if asrModel {
			_, err = s.getOrLoadASREngine(context.Background(), req.Model)
		} else if embeddingModel {
			_, err = s.getOrLoadEmbeddingEngineWithOpts(req.Model, requestedNumCtx, requestedNGPULayers, requestedDType)
		} else {
			_, err = s.getOrLoadEngineFull(req.Model, nil, requestedNumCtx, requestedNumParallel, requestedNGPULayers, requestedCacheTypeK, requestedCacheTypeV, requestedDType)
		}
		if err != nil {
			log.Printf("MODEL %s: load failed: %v", req.Model, err)
			if status, ok := imagegen.RuntimeStatusFromError(err); ok {
				writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
					"error":     err.Error(),
					"errorCode": http.StatusServiceUnavailable,
					"runtime":   status,
				})
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if imageGenerationModel {
			s.touchImageEngine(req.Model)
		} else if asrModel {
			s.touchASREngine(req.Model)
		} else if embeddingModel {
			s.touchEngineKey(engineCacheKey(req.Model, engineModeEmbed))
		} else {
			s.touchEngine(req.Model)
		}
		if keepAliveSet {
			if imageGenerationModel {
				s.setImageEngineKeepAlive(req.Model, requestedKeepAlive)
			} else if asrModel {
				s.setASREngineKeepAlive(req.Model, requestedKeepAlive)
			} else {
				s.setEngineKeepAlive(req.Model, requestedKeepAlive)
			}
		}
		log.Printf("MODEL %s: load ready", req.Model)
		writeJSON(w, http.StatusOK, api.LoadResponse{Status: "ready"})
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

	safeSSE(api.LoadResponse{Status: "loading " + req.Model})
	log.Printf("MODEL %s: load requested stream=true num_ctx=%d num_parallel=%d n_gpu_layers=%d cache_type_k=%q cache_type_v=%q dtype=%q", req.Model, requestedNumCtx, requestedNumParallel, requestedNGPULayers, requestedCacheTypeK, requestedCacheTypeV, requestedDType)

	lastLoadProgressLog := time.Time{}
	progress := func(step string, current, total int) {
		safeSSE(api.LoadResponse{
			Status:  "converting",
			Step:    step,
			Current: current,
			Total:   total,
		})
		if time.Since(lastLoadProgressLog) >= 2*time.Second || current == total {
			log.Printf("MODEL %s: load progress step=%q current=%d total=%d", req.Model, step, current, total)
			lastLoadProgressLog = time.Now()
		}
	}

	if imageGenerationModel {
		imageProgress := func(step string, current, total int) {
			safeSSE(api.LoadResponse{
				Status:  "installing image runtime",
				Step:    step,
				Current: current,
				Total:   total,
			})
			if time.Since(lastLoadProgressLog) >= 2*time.Second || current == total {
				log.Printf("MODEL %s: image runtime progress step=%q current=%d total=%d", req.Model, step, current, total)
				lastLoadProgressLog = time.Now()
			}
		}
		_, err = s.getOrLoadImageEngineWithProgress(context.Background(), req.Model, imageProgress, false)
	} else if asrModel {
		asrProgress := func(step string, current, total int) {
			safeSSE(api.LoadResponse{
				Status:  "installing asr runtime",
				Step:    step,
				Current: current,
				Total:   total,
			})
			if time.Since(lastLoadProgressLog) >= 2*time.Second || current == total {
				log.Printf("MODEL %s: ASR runtime progress step=%q current=%d total=%d", req.Model, step, current, total)
				lastLoadProgressLog = time.Now()
			}
		}
		runtimeManager, runtimeErr := imagegen.NewRuntimeManager()
		if runtimeErr != nil {
			err = runtimeErr
		} else if err = ensureASRRuntimeReady(context.Background(), runtimeManager, asrProgress, false); err == nil {
			_, err = s.getOrLoadASREngine(context.Background(), req.Model)
		}
	} else if embeddingModel {
		_, err = s.getOrLoadEngineFullMode(req.Model, progress, requestedNumCtx, 0, requestedNGPULayers, "", "", requestedDType, engineModeEmbed)
	} else {
		_, err = s.getOrLoadEngineWithProgressAndOpts(req.Model, progress, requestedNumCtx, requestedNumParallel, requestedNGPULayers, requestedCacheTypeK, requestedCacheTypeV, requestedDType)
	}
	if err != nil {
		log.Printf("load %s failed: %v", req.Model, err)
		if _, ok := imagegen.RuntimeStatusFromError(err); ok {
			safeSSE(api.LoadResponse{Status: "error: " + err.Error()})
			return
		}
		safeSSE(api.LoadResponse{Status: "error: " + err.Error()})
		return
	}
	if imageGenerationModel {
		s.touchImageEngine(req.Model)
	} else if asrModel {
		s.touchASREngine(req.Model)
	} else if embeddingModel {
		s.touchEngineKey(engineCacheKey(req.Model, engineModeEmbed))
	} else {
		s.touchEngine(req.Model)
	}
	if keepAliveSet {
		if imageGenerationModel {
			s.setImageEngineKeepAlive(req.Model, requestedKeepAlive)
		} else if asrModel {
			s.setASREngineKeepAlive(req.Model, requestedKeepAlive)
		} else {
			s.setEngineKeepAlive(req.Model, requestedKeepAlive)
		}
	}

	log.Printf("MODEL %s: load ready", req.Model)
	safeSSE(api.LoadResponse{Status: "ready"})
}

// POST /api/generate -- text generation
func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	var req api.GenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	opts := inference.DefaultOptions()
	requestedNumCtx := 0
	requestedNumParallel := 0
	requestedNGPULayers := -1
	requestedCacheTypeK := ""
	requestedCacheTypeV := ""
	requestedDType := ""
	if req.Options != nil {
		if req.Options.Temperature > 0 {
			opts.Temperature = req.Options.Temperature
		}
		if req.Options.TopP > 0 {
			opts.TopP = req.Options.TopP
		}
		if req.Options.TopK > 0 {
			opts.TopK = req.Options.TopK
		}
		if req.Options.MaxTokens > 0 {
			opts.MaxTokens = req.Options.MaxTokens
		}
		if req.Options.NumCtx > 0 {
			opts.NumCtx = req.Options.NumCtx
			requestedNumCtx = req.Options.NumCtx
		}
		if req.Options.NumParallel > 0 {
			requestedNumParallel = req.Options.NumParallel
		}
		if req.Options.NGPULayers != nil {
			requestedNGPULayers = *req.Options.NGPULayers
		}
		if req.Options.CacheTypeK != "" {
			requestedCacheTypeK = req.Options.CacheTypeK
		}
		if req.Options.CacheTypeV != "" {
			requestedCacheTypeV = req.Options.CacheTypeV
		}
		if req.Options.DType != "" {
			requestedDType = req.Options.DType
		}
	}

	eng, err := s.getOrLoadEngineWithOpts(req.Model, requestedNumCtx, requestedNumParallel, requestedNGPULayers, requestedCacheTypeK, requestedCacheTypeV, requestedDType)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer s.touchEngine(req.Model)

	stream := req.Stream == nil || *req.Stream
	inputTokens := estimateAnthropicTokens(req.Prompt)
	if inputTokens == 0 {
		inputTokens = 1
	}

	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		var full strings.Builder
		onToken := func(token string) {
			full.WriteString(token)
			writeSSE(w, api.GenerateResponse{
				Model:     req.Model,
				Response:  token,
				Done:      false,
				CreatedAt: time.Now(),
			})
		}

		_, err := eng.Generate(r.Context(), req.Prompt, opts, onToken)
		if err != nil {
			writeSSE(w, api.GenerateResponse{
				Model:     req.Model,
				Response:  "Error: " + err.Error(),
				Done:      true,
				CreatedAt: time.Now(),
			})
			return
		}
		s.recordAPIUsage(r, req.Model, "", inputTokens, estimateAnthropicTokens(full.String()))
		writeSSE(w, api.GenerateResponse{
			Model:     req.Model,
			Done:      true,
			CreatedAt: time.Now(),
		})
	} else {
		response, err := eng.Generate(r.Context(), req.Prompt, opts, nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.recordAPIUsage(r, req.Model, "", inputTokens, estimateAnthropicTokens(response))
		writeJSON(w, http.StatusOK, api.GenerateResponse{
			Model:     req.Model,
			Response:  response,
			Done:      true,
			CreatedAt: time.Now(),
		})
	}
}

// POST /api/chat -- chat completions
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req api.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if s.modelUsesEmbeddingEngine(req.Model) {
		s.handleEmbeddingChat(w, r, req)
		return
	}

	opts := inference.DefaultOptions()
	requestedNumCtx := 0
	requestedNumParallel := 0
	requestedNGPULayers := -1
	requestedCacheTypeK := ""
	requestedCacheTypeV := ""
	requestedDType := ""
	if req.Options != nil {
		if req.Options.Temperature > 0 {
			opts.Temperature = req.Options.Temperature
		}
		if req.Options.TopP > 0 {
			opts.TopP = req.Options.TopP
		}
		if req.Options.TopK > 0 {
			opts.TopK = req.Options.TopK
		}
		if req.Options.MaxTokens > 0 {
			opts.MaxTokens = req.Options.MaxTokens
		}
		if req.Options.NumCtx > 0 {
			opts.NumCtx = req.Options.NumCtx
			requestedNumCtx = req.Options.NumCtx
		}
		if req.Options.NumParallel > 0 {
			requestedNumParallel = req.Options.NumParallel
		}
		if req.Options.NGPULayers != nil {
			requestedNGPULayers = *req.Options.NGPULayers
		}
		if req.Options.CacheTypeK != "" {
			requestedCacheTypeK = req.Options.CacheTypeK
		}
		if req.Options.CacheTypeV != "" {
			requestedCacheTypeV = req.Options.CacheTypeV
		}
		if req.Options.DType != "" {
			requestedDType = req.Options.DType
		}
	}
	if requestDisablesThinking(r) {
		opts.DisableThinking = true
	}

	eng, err := s.getChatEngine(r.Context(), req.Model, req.Source, requestedNumCtx, requestedNumParallel, requestedNGPULayers, requestedCacheTypeK, requestedCacheTypeV, requestedDType)
	if err != nil {
		writeInferenceError(w, err)
		return
	}
	defer s.touchEngine(req.Model)

	var messages []inference.Message
	for _, m := range req.Messages {
		messages = append(messages, inference.Message{Role: m.Role, Content: m.Content, ReasoningContent: m.ReasoningContent})
	}
	currentDateContext := currentDateContextForQuery(latestUserText(req.Messages), time.Now())
	messages = insertSystemMessage(messages, inference.Message{
		Role:    "system",
		Content: currentDateContext,
	})
	inputTokens := countMessageTokens(req.Messages)
	inputTokens += estimateAnthropicTokens(currentDateContext)

	stream := req.Stream == nil || *req.Stream
	if hasToolChatFeatures(req) {
		s.handleChatWithTools(w, r, req, eng, opts, stream)
		return
	}

	if stream {
		if requestWantsSSE(r) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			wroteChunk := false
			messages, searchContext := s.augmentChatMessagesWithWebSearch(r.Context(), req, messages, eng, func(v interface{}) {
				wroteChunk = true
				writeSSE(w, v)
			})
			inputTokens += estimateAnthropicTokens(searchContext)
			var full strings.Builder
			onToken := func(token string) {
				wroteChunk = true
				full.WriteString(token)
				writeSSE(w, api.ChatResponse{
					Model: req.Model,
					Message: &api.Message{
						Role:    "assistant",
						Content: token,
					},
					Done:      false,
					CreatedAt: time.Now(),
				})
			}

			fullResp, err := eng.Chat(r.Context(), messages, opts, onToken)
			if err != nil {
				if !wroteChunk {
					writeInferenceError(w, err)
					return
				}
				writeSSE(w, api.ChatResponse{
					Model: req.Model,
					Message: &api.Message{
						Role:    "assistant",
						Content: "Error: " + err.Error(),
					},
					Done:      true,
					CreatedAt: time.Now(),
				})
				return
			}
			_ = fullResp
			s.recordAPIUsage(r, req.Model, req.Source, inputTokens, estimateAnthropicTokens(full.String()))
			writeSSE(w, api.ChatResponse{
				Model:     req.Model,
				Done:      true,
				CreatedAt: time.Now(),
			})
			return
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		wroteChunk := false
		messages, searchContext := s.augmentChatMessagesWithWebSearch(r.Context(), req, messages, eng, func(v interface{}) {
			wroteChunk = true
			writeNDJSON(w, v)
		})
		inputTokens += estimateAnthropicTokens(searchContext)
		var full strings.Builder
		onToken := func(token string) {
			wroteChunk = true
			full.WriteString(token)
			writeNDJSON(w, api.ChatResponse{
				Model: req.Model,
				Message: &api.Message{
					Role:    "assistant",
					Content: token,
				},
				Done:      false,
				CreatedAt: time.Now(),
			})
		}

		_, err := eng.Chat(r.Context(), messages, opts, onToken)
		if err != nil {
			if !wroteChunk {
				writeInferenceError(w, err)
				return
			}
			writeNDJSON(w, api.ChatResponse{
				Model: req.Model,
				Message: &api.Message{
					Role:    "assistant",
					Content: "Error: " + err.Error(),
				},
				Done:      true,
				CreatedAt: time.Now(),
			})
			return
		}
		s.recordAPIUsage(r, req.Model, req.Source, inputTokens, estimateAnthropicTokens(full.String()))
		writeNDJSON(w, api.ChatResponse{
			Model: req.Model,
			Message: &api.Message{
				Role:    "assistant",
				Content: "",
			},
			Done:      true,
			CreatedAt: time.Now(),
		})
	} else {
		messages, searchContext := s.augmentChatMessagesWithWebSearch(r.Context(), req, messages, eng, nil)
		inputTokens += estimateAnthropicTokens(searchContext)
		response, err := eng.Chat(r.Context(), messages, opts, nil)
		if err != nil {
			writeInferenceError(w, err)
			return
		}
		s.recordAPIUsage(r, req.Model, req.Source, inputTokens, estimateAnthropicTokens(response))
		writeJSON(w, http.StatusOK, api.ChatResponse{
			Model: req.Model,
			Message: &api.Message{
				Role:    "assistant",
				Content: response,
			},
			Done:      true,
			CreatedAt: time.Now(),
		})
	}
}
