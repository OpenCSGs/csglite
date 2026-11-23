package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/opencsgs/csglite/ee/edge"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/pkg/api"
)

// Requests served to the CSGHub AI gateway are recorded under this API key,
// with their bodies left out: the content belongs to a remote caller.
const (
	edgeAPIKeyID   = "edge:csghub"
	edgeAPIKeyName = "CSGHub Edge Serving"
)

type edgeRequestContextKey struct{}

// edgeRequestID returns the gateway request ID when the request arrived
// through the edge tunnel.
func edgeRequestID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(edgeRequestContextKey{}).(string)
	return id, ok
}

func newEdgeManager(s *Server, storageRoot string) *edge.Manager {
	if storageRoot == "" {
		return nil
	}
	name, _ := os.Hostname()
	// Keep an IP-shaped host name whole; trim only a domain suffix.
	if i := strings.IndexByte(name, '.'); i > 0 && net.ParseIP(name) == nil {
		name = name[:i]
	}
	m, err := edge.New(edgeHost{s}, filepath.Join(storageRoot, "edge"), name)
	if err != nil {
		log.Printf("edge: %v; edge serving is unavailable in this process", err)
		return nil
	}
	return m
}

func (s *Server) startEdge() {
	if s.edge != nil {
		s.edge.Start()
	}
}

func (s *Server) stopEdge() {
	if s.edge != nil {
		s.edge.Stop()
	}
}

// ---- edge.Host ----

type edgeHost struct{ s *Server }

// InferenceHandler serves requests arriving from the gateway. The gateway
// sends the model ID stored on the approved application. That ID pins the
// route, independent of any source field or headers from the caller.
func (h edgeHost) InferenceHandler() http.Handler {
	s := h.s
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", h.withModelRoute(s.handleOpenAIChatCompletions))
	mux.HandleFunc("POST /v1/embeddings", h.withModelRoute(s.handleOpenAIEmbeddings))
	mux.HandleFunc("POST /v1/responses", h.withModelRoute(s.handleOpenAIResponses))
	mux.HandleFunc("POST /v1/messages", h.withModelRoute(s.handleAnthropicMessages))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not served to the gateway")
	})
	// The edge marker must be in place before observability reads it.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), edgeRequestContextKey{}, r.Header.Get(edge.HeaderRequestID))
		correlationMiddleware(s.observabilityMiddleware(mux)).ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h edgeHost) withModelRoute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The tunnel guard bounds the body before the inference handler runs.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "unable to read edge request")
			return
		}
		contentType := r.Header.Get("Content-Type")
		target, err := inference.ParseModelRequest(body, contentType)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		model, source, err := h.modelRoute(target.Model, strings.TrimPrefix(r.URL.Path, "/v1/"))
		if err != nil {
			writeOpenAIInferenceError(w, err)
			return
		}
		ctx := withProviderRouteSourceContext(r.Context(), source)
		if _, err := effectiveRequestSource(ctx, target.Source); err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		r = r.Clone(ctx)
		if source != "local" {
			body, contentType, err = inference.RewriteModelRequest(body, contentType, model, source)
			if err != nil {
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid edge request body")
				return
			}
			r.Header.Set("Content-Type", contentType)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next(w, r)
	}
}

func (h edgeHost) modelRoute(id, protocol string) (model, source string, err error) {
	serviceID, isService := edge.ModelServiceTarget(id)
	if !isService {
		if edge.IsRoutedModelID(id) {
			return "", "", inference.NewHTTPStatusError(http.StatusNotFound, "shared model route is no longer supported")
		}
		return id, "local", nil
	}
	source = poolSource(serviceID)
	pool, ok := providerPoolForRequest("", source)
	if !ok {
		return "", "", inference.NewHTTPStatusError(http.StatusNotFound, "edge model service not found or disabled")
	}
	details, available := h.s.providerPoolDetails(pool)
	if !available {
		return "", "", providerPoolUnavailableError(pool)
	}
	if !slices.Contains(details.InferenceProtocols, "chat/completions") || !slices.Contains(details.InferenceProtocols, protocol) {
		return "", "", inference.NewHTTPStatusError(http.StatusBadRequest, "endpoint does not match shared model service capability")
	}
	return pool.Model, source, nil
}

// Models lists local models and published AI Gateway chat services.
func (h edgeHost) Models(context.Context) []edge.Model {
	out := h.localModels()
	localByID := make(map[string]int, len(out))
	for i, model := range out {
		localByID[model.ID] = i
	}
	for _, pool := range config.GetProviderPools() {
		if !pool.Enabled {
			continue
		}
		details, _ := h.s.providerPoolDetails(pool)
		if !slices.Contains(details.InferenceProtocols, "chat/completions") {
			continue
		}
		for i, member := range pool.Members {
			if pool.Policy == config.ProviderPoolPolicyFixed && i > 0 {
				break
			}
			if member.Source == "local" && details.SourceStates[member.ID] == "configured" {
				id := h.s.resolveLocalModelStorageID(member.Model)
				if index, ok := localByID[id]; ok {
					out[index].CoveredByService = true
				}
			}
		}
		out = append(out, edge.Model{
			ID: edge.ModelServiceID(pool.ID), Name: pool.Model,
			Source: edge.ModelSourceGateway, Ready: true, State: edge.ModelLoaded,
		})
	}
	return out
}

func (h edgeHost) localModels() []edge.Model {
	infos, err := h.s.listLocalModelInfos()
	if err != nil {
		return nil
	}
	states := h.engineStates()
	active := h.modelActiveRequests()
	out := make([]edge.Model, 0, len(infos))
	for _, info := range infos {
		if !edgeServableModel(info) {
			continue
		}
		// A runnable model that is not in memory is still served: it is
		// asleep and the first request loads it.
		state := states[info.ID]
		if state == "" {
			state = edge.ModelSleeping
		}
		// Applications bind the storage ID ([source/]namespace/name): unlike
		// the short inference ID it never changes when another model with
		// the same name is downloaded, so an approval stays valid for good.
		out = append(out, edge.Model{
			ID:             info.ID,
			Name:           info.Model,
			Ready:          info.LocalInference != nil && info.LocalInference.Supported,
			State:          state,
			Format:         info.Format,
			ContextLength:  info.ContextWindow,
			ActiveRequests: active[info.ID],
		})
	}
	return out
}

// engineStates maps model storage IDs (the engine keys) to loaded or
// loading; models absent from the map are asleep.
func (h edgeHost) engineStates() map[string]string {
	s := h.s
	s.mu.RLock()
	defer s.mu.RUnlock()
	states := make(map[string]string, len(s.engines)+len(s.loading))
	for key := range s.loading {
		states[engineModelIDFromKey(key)] = edge.ModelLoading
	}
	for key := range s.engines {
		states[engineModelIDFromKey(key)] = edge.ModelLoaded
	}
	return states
}

// edgeServableModel reports whether a model answers chat requests:
// language models and the vision/video chat models (image-text-to-text),
// which carry no language_model category of their own.
func edgeServableModel(info api.ModelInfo) bool {
	if info.DecisionOnly {
		return false
	}
	if info.Category == "language_model" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(info.PipelineTag)) {
	case "image-text-to-text", "video-text-to-text":
		return true
	}
	return false
}

func (h edgeHost) ActiveRequests() int {
	s := h.s
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, me := range s.engines {
		n += me.activeRequests
	}
	return n
}

// modelActiveRequests maps model storage IDs to the requests their engine is
// serving right now, so the gateway can tell a busy model from an idle one.
func (h edgeHost) modelActiveRequests() map[string]int {
	s := h.s
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int, len(s.engines))
	for key, me := range s.engines {
		out[engineModelIDFromKey(key)] = me.activeRequests
	}
	return out
}

func (h edgeHost) Cloud() (string, string) {
	return resolveCloudURL(h.s.cfg), strings.TrimSpace(h.s.cfg.Token)
}

func (h edgeHost) Version() string { return h.s.version }

// edgeModelLimit is how many models this edition may contribute to the
// gateway: one in the Community edition, unlimited (0) under an Enterprise
// license.
func (s *Server) edgeModelLimit() int {
	if s.license == nil {
		return license.QuotaMaxEdgeModels.CommunityValue
	}
	return s.license.Limit(license.QuotaMaxEdgeModels)
}

// ---- local API ----

// edgeStatusResponse is the edge serving state plus the local models that
// can be offered to the gateway.
type edgeStatusResponse struct {
	edge.Status
	SignedIn   bool         `json:"signed_in"`
	Models     []edge.Model `json:"models"`
	ModelLimit int          `json:"model_limit"`
}

func (s *Server) withEdge(next func(http.ResponseWriter, *http.Request, *edge.Manager)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.edge == nil {
			writeError(w, http.StatusServiceUnavailable, "edge serving is unavailable")
			return
		}
		next(w, r, s.edge)
	}
}

func (s *Server) handleEdgeStatus(w http.ResponseWriter, r *http.Request, m *edge.Manager) {
	_, token := edgeHost{s}.Cloud()
	models := edgeHost{s}.Models(r.Context())
	models = slices.DeleteFunc(models, func(model edge.Model) bool { return model.CoveredByService })
	if models == nil {
		models = []edge.Model{}
	}
	status := m.Status()
	if strings.Contains(status.LastError, edge.ErrGatewayUnreachable.Error()) {
		status.LastError = edgeUnreachableMessage(r)
	}
	writeJSON(w, http.StatusOK, edgeStatusResponse{Status: status, SignedIn: token != "", Models: models, ModelLimit: s.edgeModelLimit()})
}

func (s *Server) handleEdgeSetEnabled(w http.ResponseWriter, r *http.Request, m *edge.Manager) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := m.SetEnabled(req.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.handleEdgeStatus(w, r, m)
}

func (s *Server) handleEdgeApplications(w http.ResponseWriter, r *http.Request, m *edge.Manager) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	per, _ := strconv.Atoi(r.URL.Query().Get("per"))
	data, total, err := m.Applications(r.Context(), page, per)
	if err != nil {
		writeEdgeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "total": total})
}

func (s *Server) handleEdgeSubmit(w http.ResponseWriter, r *http.Request, m *edge.Manager) {
	var req edge.SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !edge.IsRoutedModelID(req.LocalModelID) {
		req.LocalModelID = s.resolveLocalModelStorageID(req.LocalModelID)
	}
	limit := s.edgeModelLimit()
	if limit > 0 {
		live, err := s.edgeLiveApplications(r.Context(), m)
		if err != nil {
			writeEdgeError(w, r, err)
			return
		}
		if live >= limit {
			writeError(w, http.StatusForbidden, edgeModelLimitMessage(r, limit))
			return
		}
	}
	data, err := m.Submit(r.Context(), req)
	if err != nil {
		writeEdgeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// edgeLiveApplications counts the applications that still hold a model on
// the gateway: pending, approved and suspended ones. Withdrawn, rejected and
// revoked applications are history and do not use a slot.
func (s *Server) edgeLiveApplications(ctx context.Context, m *edge.Manager) (int, error) {
	data, _, err := m.Applications(ctx, 1, 100)
	if err != nil {
		return 0, err
	}
	var apps []struct {
		Status string `json:"status"`
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &apps); err != nil {
			// A shape this build does not understand must not lock the user
			// out of applying; the gateway still enforces its own rules.
			return 0, nil
		}
	}
	live := 0
	for _, app := range apps {
		switch app.Status {
		case "pending", "approved", "suspended":
			live++
		}
	}
	return live, nil
}

func edgeModelLimitMessage(r *http.Request, limit int) string {
	if strings.HasPrefix(requestLocale(r), "zh") {
		return fmt.Sprintf("当前版本最多贡献 %d 个模型；导入企业版 License 可解除限制。", limit)
	}
	return fmt.Sprintf("This edition can contribute %d model(s). Import an Enterprise license to lift the limit.", limit)
}

func (s *Server) handleEdgeWithdraw(w http.ResponseWriter, r *http.Request, m *edge.Manager) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}
	data, err := m.Withdraw(r.Context(), id)
	if err != nil {
		writeEdgeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

func (s *Server) handleEdgeEarnings(w http.ResponseWriter, r *http.Request, m *edge.Manager) {
	data, err := m.Earnings(r.Context(), strings.TrimSpace(r.URL.Query().Get("month")))
	if err != nil {
		writeEdgeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// edgeBalanceResponse is the signed-in user's earnings balance exactly as
// CSGHub records it, and the CSGHub page with their statements and payee
// details. Lite shows only CSGHub's figures so an owner never sees two
// versions of what they earned.
type edgeBalanceResponse struct {
	Balance       json.RawMessage `json:"balance"`
	StatementsURL string          `json:"statements_url"`
}

func (s *Server) handleEdgeBalance(w http.ResponseWriter, r *http.Request, m *edge.Manager) {
	data, err := m.Balance(r.Context())
	if err != nil {
		writeEdgeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, edgeBalanceResponse{
		Balance:       data,
		StatementsURL: strings.TrimRight(s.cfg.DisplayURL(), "/") + "/settings/edge-earnings",
	})
}

func (s *Server) handleEdgeStatements(w http.ResponseWriter, r *http.Request, m *edge.Manager) {
	data, err := m.Statements(r.Context())
	if err != nil {
		writeEdgeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// writeEdgeError passes gateway API failures on with their status so the UI
// can show the gateway's message (for example a review conflict). The
// gateway's own 404 page is reported as a missing edge API in the caller's language.
func writeEdgeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, edge.ErrUseModelService) {
		message := err.Error()
		if strings.HasPrefix(requestLocale(r), "zh") {
			message = "此本地模型已通过模型服务提供，请选择对应的模型服务申请。"
		}
		writeError(w, http.StatusConflict, message)
		return
	}
	if errors.Is(err, edge.ErrModelUnavailable) {
		message := err.Error()
		if strings.HasPrefix(requestLocale(r), "zh") {
			message = "所选模型当前不可用于算力共享。"
		}
		writeError(w, http.StatusConflict, message)
		return
	}
	if errors.Is(err, edge.ErrGatewayUnreachable) {
		writeError(w, http.StatusBadGateway, edgeUnreachableMessage(r))
		return
	}
	var apiErr *edge.APIError
	if errors.As(err, &apiErr) {
		status := apiErr.Status
		if status < http.StatusBadRequest || status >= http.StatusInternalServerError {
			status = http.StatusBadGateway
		}
		writeError(w, status, apiErr.Message)
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

func edgeUnreachableMessage(r *http.Request) string {
	if strings.HasPrefix(requestLocale(r), "zh") {
		return "当前 AI 网关未提供端侧服务接口（HTTP 404）。请确认网关地址及服务端部署。"
	}
	return "This AI gateway does not provide the edge serving API (HTTP 404). Check the gateway address and server deployment."
}
