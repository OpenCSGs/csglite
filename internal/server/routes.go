package server

import "net/http"

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/tags", s.handleTags)
	mux.HandleFunc("GET /api/pipeline-tags", s.handlePipelineTags)
	mux.HandleFunc("GET /api/tags/manage", s.handleProviderTagsManageList)
	mux.HandleFunc("POST /api/tags/manage", s.handleProviderTagsManageAdd)
	mux.HandleFunc("PUT /api/tags/manage", s.handleProviderTagsManageReplace)
	mux.HandleFunc("PATCH /api/tags/manage", s.handleProviderTagsManageUpdate)
	mux.HandleFunc("DELETE /api/tags/manage", s.handleProviderTagsManageDelete)
	mux.HandleFunc("GET /api/models/search", s.handleLocalModelSearch)
	mux.HandleFunc("POST /api/models/upload", s.handleModelUpload)
	mux.HandleFunc("POST /api/models/upload/start", s.handleModelUploadStart)
	mux.HandleFunc("PUT /api/models/upload/{uploadID}/file", s.handleModelUploadFile)
	mux.HandleFunc("POST /api/models/upload/{uploadID}/complete", s.handleModelUploadComplete)
	mux.HandleFunc("DELETE /api/models/upload/{uploadID}", s.handleModelUploadCancel)
	mux.HandleFunc("GET /api/models/{model}/manifest", s.handleModelManifestByPublicID)
	mux.HandleFunc("GET /api/models/{namespace}/{name}/manifest", s.handleModelManifest)
	mux.HandleFunc("GET /api/models/{namespace}/{name}/files/{path...}", s.handleModelFile)
	mux.HandleFunc("GET /api/model-files/{artifactSource}/{namespace}/{name}/{path...}", s.handleModelFile)
	mux.HandleFunc("GET /api/ps", s.handlePs)
	mux.HandleFunc("POST /api/show", s.handleShow)
	mux.HandleFunc("POST /api/pull", s.handlePull)
	mux.HandleFunc("POST /api/pull/jobs", s.handlePullJobCreate)
	mux.HandleFunc("GET /api/pull/jobs/{jobID}", s.handlePullJobGet)
	mux.HandleFunc("DELETE /api/pull/jobs/{jobID}", s.handlePullJobCancel)
	mux.HandleFunc("DELETE /api/pull/partial", s.handlePartialModelPullDelete)
	mux.HandleFunc("POST /api/load", s.handleLoad)
	mux.HandleFunc("POST /api/stop", s.handleStop)
	mux.HandleFunc("DELETE /api/delete", s.handleDelete)
	mux.HandleFunc("POST /api/generate", s.handleGenerate)
	mux.HandleFunc("POST /api/chat", s.handleChat)

	mux.HandleFunc("GET /api/datasets", s.handleDatasetTags)
	mux.HandleFunc("GET /api/datasets/search", s.handleDatasetSearch)
	mux.HandleFunc("GET /api/datasets/{namespace}/{name}/manifest", s.handleDatasetManifest)
	mux.HandleFunc("GET /api/dataset-manifests/{artifactSource}/{namespace}/{name}", s.handleDatasetManifest)
	mux.HandleFunc("GET /api/datasets/{namespace}/{name}/export", s.handleLocalDatasetExport)
	mux.HandleFunc("POST /api/datasets/{namespace}/{name}/publish", s.handleLocalDatasetPublish)
	mux.HandleFunc("GET /api/datasets/{namespace}/{name}/files/{path...}", s.handleDatasetFile)
	mux.HandleFunc("GET /api/dataset-files/{artifactSource}/{namespace}/{name}/{path...}", s.handleDatasetFile)
	mux.HandleFunc("POST /api/datasets/show", s.handleDatasetShow)
	mux.HandleFunc("POST /api/datasets/files", s.handleDatasetFiles)
	mux.HandleFunc("POST /api/datasets/pull", s.handleDatasetPull)
	mux.HandleFunc("POST /api/datasets/pull/jobs", s.handleDatasetPullJobCreate)
	mux.HandleFunc("DELETE /api/datasets/pull/partial", s.handlePartialDatasetPullDelete)
	mux.HandleFunc("DELETE /api/datasets/delete", s.handleDatasetDelete)

	mux.HandleFunc("POST /v1/chat/completions", s.handleOpenAIChatCompletions)
	mux.HandleFunc("POST /v1/embeddings", s.handleOpenAIEmbeddings)
	mux.HandleFunc("POST /v1/images/generations", s.handleOpenAIImagesGenerations)
	mux.HandleFunc("POST /v1/images/edits", s.handleOpenAIImagesEdits)
	mux.HandleFunc("POST /v1/audio/transcriptions", s.handleOpenAIAudioTranscriptions)
	mux.HandleFunc("POST /api/images/jobs", s.handleImageGenerationJobCreate)
	mux.HandleFunc("GET /api/images/jobs", s.handleImageGenerationJobList)
	mux.HandleFunc("GET /api/images/jobs/{jobID}", s.handleImageGenerationJobGet)
	mux.HandleFunc("GET /api/images/jobs/{jobID}/result", s.handleImageGenerationJobResult)
	mux.HandleFunc("DELETE /api/images/jobs/{jobID}", s.handleImageGenerationJobCancel)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /v1/responses", s.handleOpenAIResponsesUnsupported)
	mux.HandleFunc("POST /v1/responses", s.handleOpenAIResponses)
	mux.HandleFunc("POST /v1/messages", s.handleAnthropicMessages)
	mux.HandleFunc("POST /v1/messages/count_tokens", s.handleAnthropicCountTokens)
	mux.HandleFunc("POST /anthropic/messages", s.handleAnthropicMessages)
	mux.HandleFunc("POST /anthropic/messages/count_tokens", s.handleAnthropicCountTokens)
	mux.HandleFunc("POST /anthropic/v1/messages", s.handleAnthropicMessages)
	mux.HandleFunc("POST /anthropic/v1/messages/count_tokens", s.handleAnthropicCountTokens)
	s.registerProviderInferenceRoutes(mux)

	// New: marketplace, system, logs, settings
	mux.HandleFunc("GET /api/marketplace/models", s.handleMarketplaceModels)
	mux.HandleFunc("POST /api/marketplace/models/extra", s.handleMarketplaceModelExtras)
	mux.HandleFunc("GET /api/marketplace/models/{namespace}/{name}", s.handleMarketplaceModelDetail)
	mux.HandleFunc("GET /api/marketplace/datasets", s.handleMarketplaceDatasets)
	mux.HandleFunc("GET /api/marketplace/datasets/{namespace}/{name}", s.handleMarketplaceDatasetDetail)
	mux.HandleFunc("GET /api/marketplace/datasets/{namespace}/{name}/extras", s.handleMarketplaceDatasetExtras)
	mux.HandleFunc("GET /api/system", s.handleSystem)
	mux.HandleFunc("POST /api/system/open-external", s.handleDesktopOpenExternal)
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("POST /api/settings", s.handleSettingsUpdate)
	mux.HandleFunc("POST /api/settings/directories", s.handleSettingsDirectories)
	mux.HandleFunc("GET /api/image-runtime", s.handleImageRuntimeStatus)
	mux.HandleFunc("POST /api/image-runtime/install", s.handleImageRuntimeInstall)
	mux.HandleFunc("GET /api/asr-runtime", s.handleASRRuntimeStatus)
	mux.HandleFunc("POST /api/asr-runtime/install", s.handleASRRuntimeInstall)
	mux.HandleFunc("GET /api/embedding-runtime", s.handleEmbeddingRuntimeStatus)
	mux.HandleFunc("POST /api/embedding-runtime/install", s.handleEmbeddingRuntimeInstall)
	mux.HandleFunc("GET /api/api-keys", s.handleAPIKeysList)
	mux.HandleFunc("POST /api/api-keys/settings", s.handleAPIKeysSettingsUpdate)
	mux.HandleFunc("POST /api/api-keys", s.handleAPIKeyCreate)
	mux.HandleFunc("DELETE /api/api-keys/{id}", s.handleAPIKeyDelete)
	mux.HandleFunc("GET /api/api-usage", s.handleAPIUsage)
	mux.HandleFunc("GET /api/observability/requests", s.handleObservabilityRequests)
	mux.HandleFunc("GET /api/observability/requests/{id}", s.handleObservabilityRequest)
	mux.HandleFunc("GET /api/observability/traces", s.handleObservabilityTraces)
	mux.HandleFunc("GET /api/observability/traces/{traceID}", s.handleObservabilityTrace)
	mux.HandleFunc("POST /api/observability/dataset-exports/preview", s.handleDatasetExportPreview)
	mux.HandleFunc("POST /api/observability/dataset-exports", s.handleDatasetExportCreate)
	mux.HandleFunc("GET /api/observability/dataset-export-jobs/{jobID}", s.handleDatasetExportJob)
	mux.HandleFunc("GET /api/observability/dataset-exports/{exportID}/download", s.handleDatasetExportDownload)
	mux.HandleFunc("DELETE /api/observability", s.handleObservabilityClear)
	// Third-party providers
	mux.HandleFunc("GET /api/providers", s.handleProvidersList)
	mux.HandleFunc("POST /api/providers/validate", s.handleProviderValidate)
	mux.HandleFunc("POST /api/providers", s.handleProviderCreate)
	mux.HandleFunc("PUT /api/providers/{id}", s.handleProviderUpdate)
	mux.HandleFunc("DELETE /api/providers/{id}", s.handleProviderDelete)
	mux.HandleFunc("GET /api/provider-pools", s.handleProviderPoolsList)
	mux.HandleFunc("POST /api/provider-pools", s.handleProviderPoolCreate)
	mux.HandleFunc("PUT /api/provider-pools/{id}", s.handleProviderPoolUpdate)
	mux.HandleFunc("DELETE /api/provider-pools/{id}", s.handleProviderPoolDelete)
	mux.HandleFunc("GET /api/cloud/auth", s.handleCloudAuthStatus)
	mux.HandleFunc("POST /api/cloud/auth/token", s.handleCloudAuthTokenSave)
	mux.HandleFunc("DELETE /api/cloud/auth/token", s.handleCloudAuthTokenDelete)
	mux.HandleFunc("POST /api/cloud/api-key", s.handleCloudAPIKeySave)
	mux.HandleFunc("DELETE /api/cloud/api-key", s.handleCloudAPIKeyDelete)
	mux.HandleFunc("POST /api/shutdown", s.handleShutdown)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("GET /api/apps", s.handleApps)
	mux.HandleFunc("POST /api/apps/install", s.handleAppInstall)
	mux.HandleFunc("POST /api/apps/uninstall", s.handleAppUninstall)
	mux.HandleFunc("POST /api/apps/start", s.handleAppStart)
	mux.HandleFunc("POST /api/apps/stop", s.handleAppStop)
	mux.HandleFunc("POST /api/apps/open", s.handleAppOpen)
	mux.HandleFunc("POST /api/apps/model", s.handleAppModelSave)
	mux.HandleFunc("POST /api/apps/provider", s.handleAppProviderSwitch)
	mux.HandleFunc("POST /api/apps/path", s.handleAppSetPath)
	mux.HandleFunc("GET /api/apps/logs", s.handleAppLogs)
	mux.HandleFunc("GET /api/apps/shell/{id}/ws", s.handleAppShellWS)
	mux.HandleFunc("POST /api/apps/shell/{id}/close", s.handleAppShellClose)

	// Conversation history
	mux.HandleFunc("GET /api/conversations", s.handleConversationsList)
	mux.HandleFunc("POST /api/conversations", s.handleConversationCreate)
	mux.HandleFunc("GET /api/conversations/search", s.handleConversationsSearch)
	mux.HandleFunc("GET /api/conversations/{id}", s.handleConversationGet)
	mux.HandleFunc("PUT /api/conversations/{id}", s.handleConversationUpdate)
	mux.HandleFunc("DELETE /api/conversations/{id}", s.handleConversationDelete)

	// Upgrade API
	mux.HandleFunc("GET /api/upgrade/check", s.handleUpgradeCheck)
	mux.HandleFunc("POST /api/upgrade", s.handleUpgrade)

	// Static files: serve embedded web UI or dev fallback
	if hasEmbeddedStatic() {
		mux.Handle("GET /", staticHandler())
	} else {
		mux.Handle("GET /", devStaticHandler("web/dist"))
	}

	return correlationMiddleware(LogMiddleware(
		s.desktopAuthMiddleware(s.corsMiddleware(s.apiAuthMiddleware(
			s.observabilityMiddleware(providerPoolUsageMiddleware(mux)),
		))),
	))
}

func (s *Server) externalAPIRoutes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/tags", s.handleTags)
	mux.HandleFunc("GET /api/pipeline-tags", s.handlePipelineTags)
	mux.HandleFunc("GET /api/ps", s.handlePs)
	mux.HandleFunc("POST /api/show", s.handleShow)
	mux.HandleFunc("POST /api/load", s.handleLoad)
	mux.HandleFunc("POST /api/stop", s.handleStop)
	mux.HandleFunc("POST /api/generate", s.handleGenerate)
	mux.HandleFunc("POST /api/chat", s.handleChat)
	mux.HandleFunc("GET /api/models/{model}/manifest", s.handleModelManifestByPublicID)
	mux.HandleFunc("GET /api/models/{namespace}/{name}/manifest", s.handleModelManifest)
	mux.HandleFunc("GET /api/models/{namespace}/{name}/files/{path...}", s.handleModelFile)
	mux.HandleFunc("GET /api/model-files/{artifactSource}/{namespace}/{name}/{path...}", s.handleModelFile)
	mux.HandleFunc("GET /api/datasets/{namespace}/{name}/manifest", s.handleDatasetManifest)
	mux.HandleFunc("GET /api/dataset-manifests/{artifactSource}/{namespace}/{name}", s.handleDatasetManifest)
	mux.HandleFunc("GET /api/datasets/{namespace}/{name}/files/{path...}", s.handleDatasetFile)
	mux.HandleFunc("GET /api/dataset-files/{artifactSource}/{namespace}/{name}/{path...}", s.handleDatasetFile)

	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /v1/responses", s.handleOpenAIResponsesUnsupported)
	mux.HandleFunc("POST /v1/chat/completions", s.handleOpenAIChatCompletions)
	mux.HandleFunc("POST /v1/embeddings", s.handleOpenAIEmbeddings)
	mux.HandleFunc("POST /v1/images/generations", s.handleOpenAIImagesGenerations)
	mux.HandleFunc("POST /v1/images/edits", s.handleOpenAIImagesEdits)
	mux.HandleFunc("POST /v1/audio/transcriptions", s.handleOpenAIAudioTranscriptions)
	mux.HandleFunc("POST /v1/responses", s.handleOpenAIResponses)
	mux.HandleFunc("POST /v1/messages", s.handleAnthropicMessages)
	mux.HandleFunc("POST /v1/messages/count_tokens", s.handleAnthropicCountTokens)
	mux.HandleFunc("POST /anthropic/messages", s.handleAnthropicMessages)
	mux.HandleFunc("POST /anthropic/messages/count_tokens", s.handleAnthropicCountTokens)
	mux.HandleFunc("POST /anthropic/v1/messages", s.handleAnthropicMessages)
	mux.HandleFunc("POST /anthropic/v1/messages/count_tokens", s.handleAnthropicCountTokens)
	s.registerProviderInferenceRoutes(mux)

	return correlationMiddleware(LogMiddleware(
		desktopExternalAPIMiddleware(s.apiAuthMiddleware(
			s.observabilityMiddleware(providerPoolUsageMiddleware(mux)),
		)),
	))
}

func desktopExternalAPIMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			writeError(w, http.StatusForbidden, "browser origins are not allowed on the desktop API")
			return
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.DesktopMode {
			if origin := r.Header.Get("Origin"); origin != "" && isAllowedDesktopOrigin(r) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Add("Vary", "Origin")
			}
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, X-B3-TraceId, X-CSGLite-Request-ID, X-CSGLite-Trace-ID, X-CSGLite-Thread-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Range, Authorization, x-api-key, X-Request-ID, X-B3-TraceId, X-CSGLite-Desktop-Token, X-CSGLite-Trace-ID, X-CSGLite-Thread-ID")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
