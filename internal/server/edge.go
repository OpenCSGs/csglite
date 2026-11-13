package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opencsgs/csglite/ee/edge"
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

// InferenceHandler serves requests arriving from the gateway. Like cluster
// forwarding, the route source is pinned to "local": a remote caller pays
// for this machine's model and must never be routed onward to a cloud
// provider or a cluster peer.
func (h edgeHost) InferenceHandler() http.Handler {
	s := h.s
	mux := http.NewServeMux()
	local := func(handler http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), providerRouteSourceContextKey{}, "local")
			handler(w, r.WithContext(ctx))
		}
	}
	mux.HandleFunc("POST /v1/chat/completions", local(s.handleOpenAIChatCompletions))
	mux.HandleFunc("POST /v1/embeddings", local(s.handleOpenAIEmbeddings))
	mux.HandleFunc("POST /v1/responses", local(s.handleOpenAIResponses))
	mux.HandleFunc("POST /v1/messages", local(s.handleAnthropicMessages))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not served to the gateway")
	})
	// The edge marker must be in place before observability reads it.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), edgeRequestContextKey{}, r.Header.Get(edge.HeaderRequestID))
		correlationMiddleware(s.observabilityMiddleware(mux)).ServeHTTP(w, r.WithContext(ctx))
	})
}

// Models reports downloaded language models; a model is ready when this
// machine can run it locally.
func (h edgeHost) Models(context.Context) []edge.Model {
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

// edgeServableModel reports whether a local model answers chat requests:
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
	if models == nil {
		models = []edge.Model{}
	}
	writeJSON(w, http.StatusOK, edgeStatusResponse{Status: m.Status(), SignedIn: token != "", Models: models, ModelLimit: s.edgeModelLimit()})
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
// gateway's own 404 page is reported as unreachable, in the caller's language.
func writeEdgeError(w http.ResponseWriter, r *http.Request, err error) {
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
		return "不能连接 CSGHub AI 网关"
	}
	return "Cannot connect to the CSGHub AI gateway."
}
