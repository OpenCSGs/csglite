// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// adminAPI serves /api/cluster/*, the endpoints an operator drives from the
// web UI or the CLI. It is a thin face over the manager rather than more
// methods on it: an endpoint added here does not grow the type that also runs
// discovery, gossip and scheduling.
type adminAPI struct{ m *Manager }

// Admin returns this node's operator API.
func (m *Manager) Admin() *adminAPI { return &adminAPI{m: m} }

// ---- API views ----

// ---- JSON helpers matching the server's error envelope ----

// ---- views ----

// ---- HTTP handlers (mounted by the server under /api/cluster) ----

// HandleGet is GET /api/cluster.
func (a *adminAPI) HandleGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.m.View(r.Context()))
}

// HandleSummary is GET /api/cluster/summary.
func (a *adminAPI) HandleSummary(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.m.Summary(r.Context()))
}

// HandleCreate is POST /api/cluster.
func (a *adminAPI) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	info, token, err := a.m.CreateCluster(req.Name)
	if err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"cluster": info, "join_token": token})
}

// HandleLeave is DELETE /api/cluster.
func (a *adminAPI) HandleLeave(w http.ResponseWriter, r *http.Request) {
	if err := a.m.Leave(r.Context()); err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.m.View(r.Context()))
}

// HandleJoin is POST /api/cluster/join.
func (a *adminAPI) HandleJoin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token   string `json:"token"`
		Address string `json:"address"`
	}
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Token) == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := a.m.Join(ctx, req.Token, req.Address); err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.m.View(r.Context()))
}

// HandleToken is GET /api/cluster/token.
func (a *adminAPI) HandleToken(w http.ResponseWriter, r *http.Request) {
	if !a.m.store.InCluster() {
		writeOpError(w, ErrNotClustered)
		return
	}
	token := a.m.store.JoinToken()
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "available": token != ""})
}

// HandleTokenRotate is POST /api/cluster/token/rotate.
func (a *adminAPI) HandleTokenRotate(w http.ResponseWriter, r *http.Request) {
	token, err := a.m.JoinToken(true)
	if err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "available": true})
}

// HandleEnable is POST /api/cluster/enable: start listening and discovering
// without joining anything yet, so unpaired nodes show up.
func (a *adminAPI) HandleEnable(w http.ResponseWriter, r *http.Request) {
	if err := a.m.Activate(); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.m.View(r.Context()))
}

// HandleDisable is POST /api/cluster/disable: back to dormant (not while a
// member; leave first).
func (a *adminAPI) HandleDisable(w http.ResponseWriter, r *http.Request) {
	if err := a.m.Deactivate(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.m.View(r.Context()))
}

// HandleCode is GET /api/cluster/code.
func (a *adminAPI) HandleCode(w http.ResponseWriter, r *http.Request) {
	// Showing the code means an invite is expected, which needs the
	// listener up.
	if err := a.m.Activate(); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	code, expires, err := a.m.store.NodeCode()
	if err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "expires_at": expires})
}

// HandleDiscovered is GET /api/cluster/discovered. A dormant node lists
// nothing and says so; reading must not switch networking on.
func (a *adminAPI) HandleDiscovered(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"nodes": a.m.DiscoveredNodes(), "active": a.m.Active()})
}

// HandleInvite is POST /api/cluster/invite.
func (a *adminAPI) HandleInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID    string `json:"uuid"`
		Code    string `json:"code"`
		Address string `json:"address"`
	}
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Code) == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}
	if strings.TrimSpace(req.UUID) == "" && strings.TrimSpace(req.Address) == "" {
		writeError(w, http.StatusBadRequest, "uuid or address is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	mem, err := a.m.Invite(ctx, strings.TrimSpace(req.UUID), req.Code, req.Address)
	if err != nil {
		writeOpError(w, err)
		return
	}
	for _, v := range a.m.memberViews(r.Context(), false) {
		if v.UUID == mem.UUID {
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	writeJSON(w, http.StatusOK, NodeView{UUID: mem.UUID, Name: mem.Name, Health: HealthUnknown})
}

// HandleNodeUpdate is PUT /api/cluster/nodes/{uuid}.
func (a *adminAPI) HandleNodeUpdate(w http.ResponseWriter, r *http.Request) {
	nodeUUID := r.PathValue("uuid")
	var req struct {
		Name          *string `json:"name"`
		StaticAddress *string `json:"static_address"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if nodeUUID == a.m.identity.UUID {
		if req.Name != nil {
			if err := a.m.Rename(*req.Name); err != nil {
				writeOpError(w, err)
				return
			}
		}
	} else {
		if _, ok := a.m.store.Member(nodeUUID); !ok {
			writeError(w, http.StatusNotFound, "node is not a member of this cluster")
			return
		}
		if req.Name != nil {
			writeError(w, http.StatusBadRequest, "a node's name is set on that node itself")
			return
		}
		if req.StaticAddress != nil {
			addr := strings.TrimSpace(*req.StaticAddress)
			if addr != "" {
				if _, _, err := net.SplitHostPort(addr); err != nil {
					if strings.ContainsAny(addr, " /") {
						writeError(w, http.StatusBadRequest, "static_address must be host or host:port")
						return
					}
				}
			}
			if _, err := a.m.UpdateSettings(func(s *Settings) {
				if addr == "" {
					delete(s.StaticAddresses, nodeUUID)
				} else {
					s.StaticAddresses[nodeUUID] = addr
				}
			}); err != nil {
				writeOpError(w, err)
				return
			}
		}
	}
	for _, v := range a.m.memberViews(r.Context(), true) {
		if v.UUID == nodeUUID {
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	writeError(w, http.StatusNotFound, "node not found")
}

// HandleNodeRemove is DELETE /api/cluster/nodes/{uuid}.
func (a *adminAPI) HandleNodeRemove(w http.ResponseWriter, r *http.Request) {
	nodeUUID := r.PathValue("uuid")
	if nodeUUID == a.m.identity.UUID {
		writeError(w, http.StatusBadRequest, "use DELETE /api/cluster to leave the cluster from this node")
		return
	}
	if err := a.m.RemoveMember(r.Context(), nodeUUID); err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.m.View(r.Context()))
}

// HandleNodeState is POST /api/cluster/nodes/{uuid}/state.
func (a *adminAPI) HandleNodeState(w http.ResponseWriter, r *http.Request) {
	nodeUUID := r.PathValue("uuid")
	var req struct {
		State NodeState `json:"state"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch req.State {
	case NodeStateActive, NodeStateDrain, NodeStateMaintenance:
	default:
		writeError(w, http.StatusBadRequest, "state must be active, drain or maintenance")
		return
	}
	if nodeUUID != a.m.identity.UUID {
		writeError(w, http.StatusBadRequest, "a node's state is set on that node itself; open its own API or UI")
		return
	}
	s, err := a.m.UpdateSettings(func(s *Settings) { s.State = req.State })
	if err != nil {
		writeOpError(w, err)
		return
	}
	a.m.invalidateLocalStatus()
	a.m.wakeGossip()
	writeJSON(w, http.StatusOK, s)
}

// HandleModels is GET /api/cluster/models.
func (a *adminAPI) HandleModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"models": a.m.Models(r.Context())})
}

// HandleExplain is GET /api/cluster/explain.
func (a *adminAPI) HandleExplain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	model := strings.TrimSpace(q.Get("model"))
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	prompt, _ := strconv.Atoi(q.Get("prompt_tokens"))
	maxTokens, _ := strconv.Atoi(q.Get("max_tokens"))
	writeJSON(w, http.StatusOK, a.m.Explain(r.Context(), model, prompt, maxTokens, strings.TrimSpace(q.Get("affinity_key"))))
}

// HandleRecommendations is GET /api/cluster/recommendations.
func (a *adminAPI) HandleRecommendations(w http.ResponseWriter, r *http.Request) {
	recs := a.m.Recommendations(r.Context())
	if recs == nil {
		recs = []Recommendation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"recommendations": recs})
}

// HandleSettingsUpdate is PUT /api/cluster/settings.
func (a *adminAPI) HandleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AcceptWork       *bool        `json:"accept_work"`
		PreferLocal      *bool        `json:"prefer_local"`
		RoutingMode      *RoutingMode `json:"routing_mode"`
		AffinityMaxQueue *int         `json:"affinity_max_queue"`
		DiskReserveGB    *int         `json:"disk_reserve_gb"`
		Weight           *int         `json:"weight"`
		State            *NodeState   `json:"state"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RoutingMode != nil && *req.RoutingMode != RoutingLocalFirst && *req.RoutingMode != RoutingBalanced {
		writeError(w, http.StatusBadRequest, "routing_mode must be local_first or balanced")
		return
	}
	if req.State != nil && *req.State != NodeStateActive && *req.State != NodeStateDrain && *req.State != NodeStateMaintenance {
		writeError(w, http.StatusBadRequest, "state must be active, drain or maintenance")
		return
	}
	s, err := a.m.UpdateSettings(func(s *Settings) {
		if req.AcceptWork != nil {
			s.AcceptWork = *req.AcceptWork
		}
		if req.PreferLocal != nil {
			s.PreferLocal = *req.PreferLocal
		}
		if req.RoutingMode != nil {
			s.RoutingMode = *req.RoutingMode
		}
		if req.AffinityMaxQueue != nil {
			s.AffinityMaxQueue = *req.AffinityMaxQueue
		}
		if req.DiskReserveGB != nil {
			s.DiskReserveGB = *req.DiskReserveGB
		}
		if req.Weight != nil {
			s.Weight = *req.Weight
		}
		if req.State != nil {
			s.State = *req.State
		}
	})
	if err != nil {
		writeOpError(w, err)
		return
	}
	a.m.invalidateLocalStatus()
	a.m.wakeGossip()
	writeJSON(w, http.StatusOK, s)
}

// HandleModelSync is POST /api/cluster/models/sync: every named node pulls
// the model from its own model source (the on-prem CSGHub in the intended
// deployment).
func (a *adminAPI) HandleModelSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model          string          `json:"model"`
		Nodes          json.RawMessage `json:"nodes"`
		ArtifactSource string          `json:"artifact_source"`
		Revision       string          `json:"revision"`
		Quant          string          `json:"quant"`
		Quants         []string        `json:"quants"`
	}
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Model) == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	var targets []string
	all := false
	if len(req.Nodes) > 0 {
		var list []string
		var word string
		if json.Unmarshal(req.Nodes, &list) == nil {
			targets = list
		} else if json.Unmarshal(req.Nodes, &word) == nil && word == "all" {
			all = true
		} else {
			writeError(w, http.StatusBadRequest, `nodes must be a list of node uuids or "all"`)
			return
		}
	} else {
		all = true
	}
	view := a.m.View(r.Context())
	if view.ModelSourceMixed {
		writeError(w, http.StatusConflict, "members use different model sources; the same model id may not mean the same files. Point every node at the same CSGHub before syncing")
		return
	}
	models := a.m.Models(r.Context())
	holders := map[string]bool{}
	for _, cm := range models {
		if cm.ID == req.Model {
			for _, n := range cm.Nodes {
				holders[n.UUID] = true
			}
		}
	}
	members := a.m.memberViews(r.Context(), true)
	if all {
		for _, v := range members {
			targets = append(targets, v.UUID)
		}
	}
	// Whichever node holds the model knows exactly how to fetch it; fall
	// back to parsing the id when none of them reports it.
	repo, artifactSource := "", ""
	for _, mv := range members {
		if mv.Status == nil {
			continue
		}
		if ms, ok := mv.Status.Model(req.Model); ok && ms.Repo != "" {
			repo, artifactSource = ms.Repo, ms.Source
			break
		}
	}
	if repo == "" {
		repo, artifactSource = a.m.opts.Host.PullSpec(req.Model)
	}
	pull := map[string]any{"model": repo}
	if req.ArtifactSource != "" {
		pull["artifact_source"] = req.ArtifactSource
	} else if artifactSource != "" {
		pull["artifact_source"] = artifactSource
	}
	if req.Revision != "" {
		pull["revision"] = req.Revision
	}
	if req.Quant != "" {
		pull["quant"] = req.Quant
	}
	if len(req.Quants) > 0 {
		pull["quants"] = req.Quants
	}
	body, _ := json.Marshal(pull)
	results := []SyncResult{}
	for _, id := range targets {
		var view *NodeView
		for i := range members {
			if members[i].UUID == id {
				view = &members[i]
			}
		}
		res := SyncResult{NodeUUID: id}
		if view == nil {
			res.Error = "not a member"
			results = append(results, res)
			continue
		}
		res.NodeName = view.Name
		switch {
		case holders[id]:
			res.Skipped = "already present"
		case view.Local:
			rec := &bufferRecorder{header: http.Header{}}
			req2, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "/api/pull/jobs", strings.NewReader(string(body)))
			req2.Header.Set("Content-Type", "application/json")
			a.m.opts.Host.PullHandler().ServeHTTP(rec, req2)
			if rec.status/100 == 2 {
				res.Job = json.RawMessage(rec.body.String())
			} else {
				res.Error = strings.TrimSpace(rec.body.String())
			}
		case !view.Online:
			res.Error = "node is offline"
		default:
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			job, err := a.m.PullOnNode(ctx, id, body)
			cancel()
			if err != nil {
				res.Error = err.Error()
			} else {
				res.Job = job
			}
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"model": req.Model, "results": results})
}

// HandleNodePull is POST /api/cluster/nodes/{uuid}/models/pull.
func (a *adminAPI) HandleNodePull(w http.ResponseWriter, r *http.Request) {
	nodeUUID := r.PathValue("uuid")
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if nodeUUID == a.m.identity.UUID {
		req2, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "/api/pull/jobs", strings.NewReader(string(raw)))
		req2.Header.Set("Content-Type", "application/json")
		a.m.opts.Host.PullHandler().ServeHTTP(w, req2)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	job, err := a.m.PullOnNode(ctx, nodeUUID, json.RawMessage(raw))
	if err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// ExplainString is a short human summary used in logs.
func ExplainString(ex Explain) string { return fmt.Sprintf("%s: %s", ex.Model, summarizeExplain(ex)) }
