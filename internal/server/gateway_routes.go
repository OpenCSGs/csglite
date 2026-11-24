package server

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/ctxcompress"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/pkg/api"
)

const gatewayPrefix = "/ai-gateway"

// The unified gateway publishes services only. Explicit-source routes remain
// separate so an unpublished source can never shadow a public service alias.
func (s *Server) registerGatewayInferenceRoutes(mux *http.ServeMux, directTools bool) {
	mux.HandleFunc("GET /ai-gateway/v1/models", s.handleGatewayModels)
	mux.HandleFunc("POST /ai-gateway/v1/chat/completions", s.withGatewayService(s.withContextCompression(ctxcompress.ProtocolOpenAIChat, s.handleOpenAIChatCompletions)))
	mux.HandleFunc("POST /ai-gateway/v1/embeddings", s.withGatewayService(s.handleOpenAIEmbeddings))
	mux.HandleFunc("POST /ai-gateway/v1/responses", s.withGatewayService(s.withContextCompression(ctxcompress.ProtocolResponses, s.handleOpenAIResponses)))
	mux.HandleFunc("GET /ai-gateway/v1/responses", s.handleOpenAIResponsesUnsupported)
	mux.HandleFunc("POST /ai-gateway/v1/messages", s.withGatewayService(s.withContextCompression(ctxcompress.ProtocolAnthropic, s.handleAnthropicMessages)))
	mux.HandleFunc("POST /ai-gateway/v1/messages/count_tokens", s.withGatewayService(s.handleAnthropicCountTokens))
	for path, handler := range map[string]http.HandlerFunc{
		"decisions": s.handleDecisions, "images/generations": s.handleOpenAIImagesGenerations, "images/edits": s.handleOpenAIImagesEdits,
		"audio/transcriptions": s.handleOpenAIAudioTranscriptions, "audio/speech": s.handleOpenAIAudioSpeech,
	} {
		mux.HandleFunc("POST /ai-gateway/v1/"+path, s.withGatewayProtocol(handler, directTools))
	}

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
		if !s.providerPoolAvailable(pool) {
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
	return s.withGatewayProtocol(next, false)
}

func (s *Server) withGatewayProtocol(next http.HandlerFunc, allowDirect bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(io.LimitReader(r.Body, (32<<20)+1))
		if err != nil {
			writeOpenAIError(w, 413, "invalid_request_error", "request body too large or unreadable")
			return
		}
		if len(data) > 32<<20 {
			if allowDirect {
				s.serveLargeDirectGatewayRequest(w, r, next, data)
			} else {
				writeOpenAIError(w, 413, "invalid_request_error", "request body too large")
			}
			return
		}
		target, err := inference.ParseModelRequest(data, r.Header.Get("Content-Type"))
		if err != nil {
			writeOpenAIError(w, 400, "invalid_request_error", err.Error())
			return
		}
		var selected *config.ProviderPool
		for _, p := range config.GetProviderPools() {
			if p.Model == target.Model {
				selected = &p
				break
			}
		}
		if selected == nil && allowDirect {
			r.Body = io.NopCloser(bytes.NewReader(data))
			r.ContentLength = int64(len(data))
			next(w, r)
			return
		}
		if selected == nil || !selected.Enabled {
			writeOpenAIError(w, 404, "model_not_found", "model service not found or disabled")
			return
		}
		source := poolSource(selected.ID)
		if target.Source != "" && target.Source != source {
			writeOpenAIError(w, 400, "invalid_request_error", "gateway requests must use the published model service; omit source and configure routing in the model service members")
			return
		}
		caps, available := s.providerPoolDetails(*selected)
		if !available {
			writeOpenAIInferenceError(w, providerPoolUnavailableError(*selected))
			return
		}
		protocol := strings.TrimPrefix(providerRouteLegacyPath(r.URL.Path), "/v1/")
		checkProtocol := protocol
		if protocol == "messages/count_tokens" {
			checkProtocol = "messages"
		}
		if !slices.Contains(caps.InferenceProtocols, checkProtocol) {
			writeOpenAIError(w, 400, "invalid_request_error", "endpoint does not match model service capability")
			return
		}
		r = r.Clone(withProviderRouteSourceContext(r.Context(), source))
		r.Body = io.NopCloser(bytes.NewReader(data))
		r.ContentLength = int64(len(data))
		switch protocol {
		case "decisions", "images/generations", "images/edits", "audio/transcriptions", "audio/speech":
			s.servePoolProtocol(w, r, *selected, next, data)
		default:
			next(w, r)
		}
	}
}

// Auxiliary endpoints historically accepted large direct uploads. Keep that
// contract on console/desktop listeners without buffering an entire audio file
// or extending the public tunnel/service limit. All spooling uses Lite storage.
func (s *Server) serveLargeDirectGatewayRequest(w http.ResponseWriter, r *http.Request, next http.HandlerFunc, prefix []byte) {
	if err := os.MkdirAll(s.cfg.TempDir(), 0700); err != nil {
		writeOpenAIError(w, 500, "server_error", "unable to spool inference upload")
		return
	}
	file, err := os.CreateTemp(s.cfg.TempDir(), "gateway-upload-*")
	if err != nil {
		writeOpenAIError(w, 500, "server_error", "unable to spool inference upload")
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, err := io.Copy(file, io.MultiReader(bytes.NewReader(prefix), r.Body))
	if err != nil {
		writeOpenAIError(w, 400, "invalid_request_error", "unable to read inference upload")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeOpenAIError(w, 500, "server_error", "unable to read inference upload")
		return
	}
	target, err := inference.ReadModelRequest(file, r.Header.Get("Content-Type"))
	if err != nil {
		writeOpenAIError(w, 400, "invalid_request_error", err.Error())
		return
	}
	for _, pool := range config.GetProviderPools() {
		if pool.Model == target.Model {
			writeOpenAIError(w, 413, "invalid_request_error", "model service request body exceeds 32 MiB")
			return
		}
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeOpenAIError(w, 500, "server_error", "unable to read inference upload")
		return
	}
	r.Body = io.NopCloser(file)
	r.ContentLength = size
	next(w, r)
}

// Auxiliary inference protocols retain their model/source contract. They are
// mounted on the console and desktop listeners, not the public cluster listener.
func (s *Server) registerGatewayToolRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /ai-gateway/v1/decide", s.handleDecide)
	mux.HandleFunc("GET /ai-gateway/v1/decide/info", s.handleDecideInfo)
	mux.HandleFunc("POST /ai-gateway/v1/realtime/calls", s.handleRealtimeCalls)
	mux.HandleFunc("DELETE /ai-gateway/v1/realtime/calls/{call_id}", s.handleRealtimeCallDelete)
	mux.HandleFunc("GET /ai-gateway/v1/realtime", s.handleRealtimeWebSocket)
	mux.HandleFunc("GET /ai-gateway/v1/realtime/transcription", s.handleRealtimeTranscriptionWebSocket)
	mux.HandleFunc("GET /ai-gateway/v1/audio/transcriptions/realtime", s.handleRealtimeTranscriptionWebSocket)
}
