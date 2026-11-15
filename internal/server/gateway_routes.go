package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/ctxcompress"
	"github.com/opencsgs/csglite/pkg/api"
)

const gatewayPrefix = "/ai-gateway"

// The unified gateway publishes services only. Explicit-source routes remain
// separate so an unpublished source can never shadow a public service alias.
func (s *Server) registerGatewayInferenceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ai-gateway/v1/models", s.handleGatewayModels)
	mux.HandleFunc("POST /ai-gateway/v1/chat/completions", s.withGatewayService(s.withContextCompression(ctxcompress.ProtocolOpenAIChat, s.handleOpenAIChatCompletions)))
	mux.HandleFunc("POST /ai-gateway/v1/embeddings", s.withGatewayService(s.handleOpenAIEmbeddings))
	mux.HandleFunc("POST /ai-gateway/v1/responses", s.withGatewayService(s.withContextCompression(ctxcompress.ProtocolResponses, s.handleOpenAIResponses)))
	mux.HandleFunc("GET /ai-gateway/v1/responses", s.handleOpenAIResponsesUnsupported)
	mux.HandleFunc("POST /ai-gateway/v1/messages", s.withGatewayService(s.withContextCompression(ctxcompress.ProtocolAnthropic, s.handleAnthropicMessages)))
	mux.HandleFunc("POST /ai-gateway/v1/messages/count_tokens", s.withGatewayService(s.handleAnthropicCountTokens))
	notFound := func(w http.ResponseWriter, _ *http.Request) {
		writeOpenAIError(w, http.StatusNotFound, "not_found_error", "gateway endpoint not found")
	}
	mux.HandleFunc("GET /ai-gateway/v1/", notFound)
	mux.HandleFunc("POST /ai-gateway/v1/", notFound)
}

func (s *Server) handleGatewayModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	models := []api.OpenAIModel{}
	anthropic := []api.AnthropicModelInfo{}
	seen := map[string]struct{}{}
	for _, pool := range config.GetProviderPools() {
		if !pool.Enabled {
			continue
		}
		available := false
		for _, member := range pool.Members {
			if s.providerPoolSourceState(member) == "configured" {
				available = true
				break
			}
			if pool.Policy == config.ProviderPoolPolicyFixed {
				break
			}
		}
		if !available {
			continue
		}
		info := api.ModelInfo{Model: pool.Model, DisplayName: pool.Name}
		models = appendOpenAIModel(models, seen, pool.Model, info.ModifiedAt)
		anthropic = append(anthropic, s.anthropicModelFromInfo(info))
	}
	if requestWantsAnthropicModels(r) {
		response := api.AnthropicModelListResponse{Data: anthropic, HasMore: false}
		if len(anthropic) > 0 {
			response.FirstID = anthropic[0].ID
			response.LastID = anthropic[len(anthropic)-1].ID
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	writeJSON(w, http.StatusOK, api.OpenAIModelList{Object: "list", Data: models})
}

func (s *Server) withGatewayService(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
		if err != nil {
			writeOpenAIError(w, 413, "invalid_request_error", "request body too large or unreadable")
			return
		}
		var request struct {
			Model  string `json:"model"`
			Source string `json:"source"`
		}
		if json.Unmarshal(body, &request) != nil || strings.TrimSpace(request.Model) == "" {
			writeOpenAIError(w, 400, "invalid_request_error", "a valid model service ID is required")
			return
		}
		var selected *config.ProviderPool
		for _, pool := range config.GetProviderPools() {
			if pool.Model == request.Model {
				selected = &pool
				break
			}
		}
		if selected == nil || !selected.Enabled {
			writeOpenAIError(w, 404, "model_not_found", "model service not found or disabled")
			return
		}
		source := poolSource(selected.ID)
		if request.Source != "" && request.Source != source {
			writeOpenAIError(w, 400, "invalid_request_error", "gateway requests must use the published model service")
			return
		}
		embedding := strings.HasSuffix(r.URL.Path, "/embeddings")
		if (selected.Category == "embedding" && !embedding) || (selected.Category == "chat" && embedding) {
			writeOpenAIError(w, 400, "invalid_request_error", "endpoint does not match model service capability")
			return
		}
		r = r.Clone(withProviderRouteSourceContext(r.Context(), source))
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next(w, r)
	}
}

// Auxiliary inference protocols retain their model/source contract. They are
// mounted on the console and desktop listeners, not the public cluster listener.
func (s *Server) registerGatewayToolRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /ai-gateway/v1/decisions", s.handleDecisions)
	mux.HandleFunc("POST /ai-gateway/v1/decide", s.handleDecide)
	mux.HandleFunc("GET /ai-gateway/v1/decide/info", s.handleDecideInfo)
	mux.HandleFunc("POST /ai-gateway/v1/images/generations", s.handleOpenAIImagesGenerations)
	mux.HandleFunc("POST /ai-gateway/v1/images/edits", s.handleOpenAIImagesEdits)
	mux.HandleFunc("POST /ai-gateway/v1/audio/transcriptions", s.handleOpenAIAudioTranscriptions)
	mux.HandleFunc("POST /ai-gateway/v1/audio/speech", s.handleOpenAIAudioSpeech)
	mux.HandleFunc("POST /ai-gateway/v1/realtime/calls", s.handleRealtimeCalls)
	mux.HandleFunc("DELETE /ai-gateway/v1/realtime/calls/{call_id}", s.handleRealtimeCallDelete)
	mux.HandleFunc("GET /ai-gateway/v1/realtime", s.handleRealtimeWebSocket)
	mux.HandleFunc("GET /ai-gateway/v1/realtime/transcription", s.handleRealtimeTranscriptionWebSocket)
	mux.HandleFunc("GET /ai-gateway/v1/audio/transcriptions/realtime", s.handleRealtimeTranscriptionWebSocket)
}
