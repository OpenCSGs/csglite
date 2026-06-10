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
	mux.HandleFunc("GET /api/ps", s.handlePs)
	mux.HandleFunc("POST /api/show", s.handleShow)
	mux.HandleFunc("POST /api/pull", s.handlePull)
	mux.HandleFunc("POST /api/load", s.handleLoad)
	mux.HandleFunc("POST /api/stop", s.handleStop)
	mux.HandleFunc("DELETE /api/delete", s.handleDelete)
	mux.HandleFunc("POST /api/generate", s.handleGenerate)
	mux.HandleFunc("POST /api/chat", s.handleChat)

	mux.HandleFunc("GET /api/datasets", s.handleDatasetTags)
	mux.HandleFunc("GET /api/datasets/search", s.handleDatasetSearch)
	mux.HandleFunc("GET /api/datasets/{namespace}/{name}/manifest", s.handleDatasetManifest)
	mux.HandleFunc("GET /api/datasets/{namespace}/{name}/files/{path...}", s.handleDatasetFile)
	mux.HandleFunc("POST /api/datasets/show", s.handleDatasetShow)
	mux.HandleFunc("POST /api/datasets/files", s.handleDatasetFiles)
	mux.HandleFunc("POST /api/datasets/pull", s.handleDatasetPull)
	mux.HandleFunc("DELETE /api/datasets/delete", s.handleDatasetDelete)

	mux.HandleFunc("POST /v1/chat/completions", s.handleOpenAIChatCompletions)
	mux.HandleFunc("POST /v1/embeddings", s.handleOpenAIEmbeddings)
	mux.HandleFunc("POST /v1/images/generations", s.handleOpenAIImagesGenerations)
	mux.HandleFunc("POST /v1/images/edits", s.handleOpenAIImagesEdits)
	mux.HandleFunc("POST /v1/audio/transcriptions", s.handleOpenAIAudioTranscriptions)
	mux.HandleFunc("POST /api/images/jobs", s.handleImageGenerationJobCreate)
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

	// New: marketplace, system, logs, settings
	mux.HandleFunc("GET /api/marketplace/models", s.handleMarketplaceModels)
	mux.HandleFunc("GET /api/marketplace/models/{namespace}/{name}", s.handleMarketplaceModelDetail)
	mux.HandleFunc("GET /api/marketplace/datasets", s.handleMarketplaceDatasets)
	mux.HandleFunc("GET /api/system", s.handleSystem)
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("POST /api/settings", s.handleSettingsUpdate)
	mux.HandleFunc("POST /api/settings/directories", s.handleSettingsDirectories)
	mux.HandleFunc("GET /api/image-runtime", s.handleImageRuntimeStatus)
	mux.HandleFunc("POST /api/image-runtime/install", s.handleImageRuntimeInstall)
	mux.HandleFunc("GET /api/asr-runtime", s.handleASRRuntimeStatus)
	mux.HandleFunc("POST /api/asr-runtime/install", s.handleASRRuntimeInstall)
	mux.HandleFunc("GET /api/api-keys", s.handleAPIKeysList)
	mux.HandleFunc("POST /api/api-keys/settings", s.handleAPIKeysSettingsUpdate)
	mux.HandleFunc("POST /api/api-keys", s.handleAPIKeyCreate)
	mux.HandleFunc("DELETE /api/api-keys/{id}", s.handleAPIKeyDelete)
	mux.HandleFunc("GET /api/api-usage", s.handleAPIUsage)
	// Third-party providers
	mux.HandleFunc("GET /api/providers", s.handleProvidersList)
	mux.HandleFunc("POST /api/providers/validate", s.handleProviderValidate)
	mux.HandleFunc("POST /api/providers", s.handleProviderCreate)
	mux.HandleFunc("PUT /api/providers/{id}", s.handleProviderUpdate)
	mux.HandleFunc("DELETE /api/providers/{id}", s.handleProviderDelete)
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

	return corsMiddleware(s.apiAuthMiddleware(LogMiddleware(mux)))
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
