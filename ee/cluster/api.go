// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/opencsgs/csglite/internal/httpjson"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---- API views ----

// NodeView is one member as the management API and UI see it.
type NodeView struct {
	UUID          string     `json:"uuid"`
	Name          string     `json:"name"`
	Local         bool       `json:"local"`
	Health        Health     `json:"health"`
	Online        bool       `json:"online"`
	Addr          string     `json:"addr,omitempty"`
	LastSeen      *time.Time `json:"last_seen,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	JoinedAt      *time.Time `json:"joined_at,omitempty"`
	StaticAddress string     `json:"static_address,omitempty"`
	APIPort       int        `json:"api_port,omitempty"`
	Status        *Status    `json:"status,omitempty"`
}

// SelfView identifies this node.
type SelfView struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	ClusterPort int    `json:"cluster_port"`
	APIPort     int    `json:"api_port"`
}

// ClusterView is GET /api/cluster.
type ClusterView struct {
	Node               SelfView     `json:"node"`
	Cluster            *ClusterInfo `json:"cluster"`
	NodeLimit          int          `json:"node_limit"`
	Licensed           bool         `json:"licensed"`
	JoinTokenAvailable bool         `json:"join_token_available"`
	Settings           Settings     `json:"settings"`
	Members            []NodeView   `json:"members"`
	DiscoveredCount    int          `json:"discovered_count"`
	ModelSourceMixed   bool         `json:"model_source_mixed"`
	// AutoForm is true when this node was provisioned with a shared secret
	// and forms its cluster automatically; AutoFormPaused when an operator
	// left that cluster explicitly.
	AutoForm       bool `json:"auto_form"`
	AutoFormPaused bool `json:"auto_form_paused"`
	// Active is false while the node is dormant: no listener, no
	// multicast, no polling. Creating, joining, inviting or showing the
	// admission code activates it; POST /api/cluster/enable does so
	// explicitly.
	Active bool `json:"active"`
}

// SummaryNode is a compact card for the dashboard.
type SummaryNode struct {
	UUID         string    `json:"uuid"`
	Name         string    `json:"name"`
	Hostname     string    `json:"hostname,omitempty"`
	Local        bool      `json:"local"`
	Online       bool      `json:"online"`
	Health       Health    `json:"health"`
	State        NodeState `json:"state"`
	Licensed     bool      `json:"licensed"`
	Version      string    `json:"version,omitempty"`
	Addr         string    `json:"addr,omitempty"`
	GPUName      string    `json:"gpu_name,omitempty"`
	GPUCount     int       `json:"gpu_count"`
	VRAMTotal    uint64    `json:"vram_total"`
	VRAMUsed     uint64    `json:"vram_used"`
	GPUUtil      *int      `json:"gpu_util,omitempty"`
	CPUCores     int       `json:"cpu_cores"`
	CPUUtil      *float64  `json:"cpu_util,omitempty"`
	RAMTotal     uint64    `json:"ram_total"`
	RAMUsed      uint64    `json:"ram_used"`
	DiskTotal    uint64    `json:"disk_total"`
	DiskFree     uint64    `json:"disk_free"`
	LoadedModels []string  `json:"loaded_models"`
	ModelCount   int       `json:"model_count"`
	Inflight     int       `json:"inflight"`
}

// Summary is GET /api/cluster/summary.
type Summary struct {
	InCluster   bool          `json:"in_cluster"`
	ClusterName string        `json:"cluster_name,omitempty"`
	ClusterUUID string        `json:"cluster_uuid,omitempty"`
	NodeCount   int           `json:"node_count"`
	OnlineCount int           `json:"online_count"`
	NodeLimit   int           `json:"node_limit"`
	Licensed    bool          `json:"licensed"`
	VRAMTotal   uint64        `json:"vram_total"`
	VRAMUsed    uint64        `json:"vram_used"`
	Inflight    int           `json:"inflight"`
	Nodes       []SummaryNode `json:"nodes"`
}

// DiscoveredView is an unpaired node on the LAN.
type DiscoveredView struct {
	UUID        string    `json:"uuid"`
	Name        string    `json:"name"`
	Addr        string    `json:"addr"`
	ClusterUUID string    `json:"cluster_uuid,omitempty"`
	Version     string    `json:"version,omitempty"`
	APIPort     int       `json:"api_port,omitempty"`
	Licensed    bool      `json:"licensed"`
	Seen        time.Time `json:"seen"`
	Source      string    `json:"source"`
}

// Recommendation suggests replicating a model to a node.
type Recommendation struct {
	Model    string `json:"model"`
	NodeUUID string `json:"node_uuid"`
	NodeName string `json:"node_name"`
	Reason   string `json:"reason"`
}

// SyncResult is one node's answer to a sync request.
type SyncResult struct {
	NodeUUID string          `json:"node_uuid"`
	NodeName string          `json:"node_name"`
	Skipped  string          `json:"skipped,omitempty"`
	Error    string          `json:"error,omitempty"`
	Job      json.RawMessage `json:"job,omitempty"`
}

// ---- JSON helpers matching the server's error envelope ----

func writeJSON(w http.ResponseWriter, status int, v any) { httpjson.Write(w, status, v) }

func writeError(w http.ResponseWriter, status int, msg string) {
	httpjson.WriteError(w, status, msg)
}

// writeCodedError adds the machine-readable code peers switch on, such as
// "wrong_cluster", which the operator API does not always carry.
func writeCodedError(w http.ResponseWriter, status int, msg, code string) {
	httpjson.WriteCodedError(w, status, msg, code)
}

func writeLimitError(w http.ResponseWriter, le *LimitError) {
	writeJSON(w, http.StatusForbidden, errorResponse{
		Error:     "the cluster has reached its licensed node limit; import a CSGLite Enterprise license to add more nodes",
		ErrorCode: http.StatusForbidden, Code: "feature_not_licensed", Limit: le.Limit, Current: le.Current,
	})
}

func writeOpError(w http.ResponseWriter, err error) {
	var le *LimitError
	var pe *peerError
	switch {
	case errors.As(err, &le):
		writeLimitError(w, le)
	case errors.As(err, &pe):
		status := pe.Status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		body := pe.Body
		if body.ErrorCode == 0 {
			body.ErrorCode = status
		}
		writeJSON(w, status, body)
	case errors.Is(err, ErrAlreadyClustered):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrNotClustered):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// decodeJSON reads an operator API body. A blank body is accepted: several
// endpoints take only optional fields, so sending nothing is a valid request.
func decodeJSON(r *http.Request, out any) error {
	raw, err := httpjson.ReadBody(r, 1<<20)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// ---- views ----

func (m *Manager) selfView() SelfView {
	return SelfView{UUID: m.identity.UUID, Name: m.identity.DisplayName(), Version: m.opts.Host.Version(), ClusterPort: m.ListenPort(), APIPort: m.opts.Host.APIPort()}
}

func (m *Manager) memberViews(ctx context.Context, withStatus bool) []NodeView {
	local := m.cachedLocalStatus(ctx)
	now := time.Now()
	self := NodeView{UUID: m.identity.UUID, Name: m.identity.DisplayName(), Local: true, Health: HealthHealthy, Online: true, LastSeen: &now, APIPort: m.opts.Host.APIPort()}
	if withStatus {
		self.Status = local
	}
	views := []NodeView{self}
	settings := m.store.Settings()
	runtime := map[string]NodeRuntime{}
	for _, rt := range m.dir.Snapshot() {
		runtime[rt.UUID] = rt
	}
	for _, mem := range m.store.Members() {
		rt := runtime[mem.UUID]
		v := NodeView{UUID: mem.UUID, Name: mem.Name, Health: rt.Health, Online: rt.Online(), Addr: rt.Addr, LastError: rt.LastError, StaticAddress: settings.StaticAddresses[mem.UUID], APIPort: mem.APIPort}
		if v.Health == "" {
			v.Health = HealthUnknown
		}
		if rt.Status != nil && rt.Status.Name != "" {
			v.Name = rt.Status.Name
		}
		if !rt.LastSeen.IsZero() {
			t := rt.LastSeen
			v.LastSeen = &t
		}
		if !mem.JoinedAt.IsZero() {
			t := mem.JoinedAt
			v.JoinedAt = &t
		}
		if withStatus {
			v.Status = rt.Status
		}
		views = append(views, v)
	}
	sort.SliceStable(views[1:], func(i, j int) bool { return views[i+1].Name+views[i+1].UUID < views[j+1].Name+views[j+1].UUID })
	return views
}

// View is GET /api/cluster.
func (m *Manager) View(ctx context.Context) ClusterView {
	members := m.memberViews(ctx, true)
	v := ClusterView{
		Node:               m.selfView(),
		Cluster:            m.store.Cluster(),
		NodeLimit:          m.NodeLimit(),
		Licensed:           m.opts.Host.Licensed(),
		JoinTokenAvailable: m.store.JoinToken() != "",
		Settings:           m.store.Settings(),
		Members:            members,
		DiscoveredCount:    len(m.dir.Discovered(discoveredMaxAge)),
		AutoForm:           m.AutoFormEnabled(),
		AutoFormPaused:     m.AutoFormEnabled() && m.store.Settings().AutoFormPaused,
		Active:             m.Active(),
	}
	var source *ModelSource
	for _, mv := range members {
		if mv.Status == nil {
			continue
		}
		if source == nil {
			s := mv.Status.ModelSource
			source = &s
			continue
		}
		if !sameModelSource(mv.Status.ModelSource, *source) {
			v.ModelSourceMixed = true
		}
	}
	return v
}

// sameModelSource compares the registries that decide which bytes a model
// id resolves to. Hugging Face mirrors serve identical files, so the HF
// endpoint (auto-selected by region) is not part of the comparison.
func sameModelSource(a, b ModelSource) bool {
	return strings.TrimSuffix(a.ServerURL, "/") == strings.TrimSuffix(b.ServerURL, "/") &&
		strings.TrimSuffix(a.ModelScopeEndpoint, "/") == strings.TrimSuffix(b.ModelScopeEndpoint, "/")
}

// Summary is GET /api/cluster/summary.
func (m *Manager) Summary(ctx context.Context) Summary {
	sum := Summary{NodeLimit: m.NodeLimit(), Licensed: m.opts.Host.Licensed(), Nodes: []SummaryNode{}}
	if c := m.store.Cluster(); c != nil {
		sum.InCluster = true
		sum.ClusterName = c.Name
		sum.ClusterUUID = c.UUID
	}
	for _, mv := range m.memberViews(ctx, true) {
		n := SummaryNode{UUID: mv.UUID, Name: mv.Name, Local: mv.Local, Online: mv.Online, Health: mv.Health, Addr: mv.Addr, LoadedModels: []string{}}
		if st := mv.Status; st != nil {
			n.Hostname = st.Hostname
			n.State = st.State
			n.Licensed = st.Licensed
			n.Version = st.Version
			n.GPUCount = len(st.GPUs)
			if len(st.GPUs) > 0 {
				n.GPUName = st.GPUs[0].Name
				n.GPUUtil = st.GPUs[0].Util
			}
			n.VRAMTotal = st.VRAMTotal()
			n.VRAMUsed = st.VRAMTotal() - st.VRAMFree()
			n.CPUCores = st.CPU.Cores
			n.CPUUtil = st.CPU.Util
			n.RAMTotal = st.RAM.Total
			n.RAMUsed = st.RAM.Used
			n.DiskTotal = st.Disk.Total
			n.DiskFree = st.Disk.Free
			n.ModelCount = len(st.Models)
			n.Inflight = st.Inflight
			for _, ms := range st.Models {
				if ms.Loaded || ms.Loading {
					n.LoadedModels = append(n.LoadedModels, ms.ID)
				}
			}
			if mv.Online {
				sum.VRAMTotal += n.VRAMTotal
				sum.VRAMUsed += n.VRAMUsed
				sum.Inflight += n.Inflight
			}
		} else if mv.Local {
			n.State = NodeStateActive
		}
		if n.State == "" {
			n.State = NodeStateActive
		}
		if mv.Online {
			sum.OnlineCount++
		}
		sum.Nodes = append(sum.Nodes, n)
	}
	sum.NodeCount = len(sum.Nodes)
	return sum
}

// Discovered lists unpaired nodes for the UI.
func (m *Manager) DiscoveredNodes() []DiscoveredView {
	out := []DiscoveredView{}
	for _, obs := range m.dir.Discovered(discoveredMaxAge) {
		out = append(out, DiscoveredView{UUID: obs.UUID, Name: obs.Name, Addr: obs.Endpoint(), ClusterUUID: obs.ClusterUUID, Version: obs.Version, APIPort: obs.APIPort, Licensed: obs.Licensed, Seen: obs.Seen, Source: obs.Source})
	}
	return out
}

// Recommendations is a first placement advisor: a model loaded on a single
// node is a single point of failure and gives the scheduler no room, so it
// suggests the member with the most free memory and disk that lacks it.
func (m *Manager) Recommendations(ctx context.Context) []Recommendation {
	models := m.Models(ctx)
	views := m.memberViews(ctx, true)
	reserve := uint64(m.store.Settings().DiskReserveGB) << 30
	var out []Recommendation
	for _, cm := range models {
		if cm.Category != "" && cm.Category != "text-generation" && cm.Category != "chat" && cm.Category != "llm" {
			continue
		}
		holders := map[string]bool{}
		loadedSomewhere := false
		for _, n := range cm.Nodes {
			holders[n.UUID] = true
			if n.Loaded {
				loadedSomewhere = true
			}
		}
		if !loadedSomewhere || len(holders) > 1 {
			continue
		}
		var best *NodeView
		var bestFree uint64
		for i := range views {
			v := &views[i]
			if holders[v.UUID] || !v.Online || v.Status == nil {
				continue
			}
			st := v.Status
			free, known := freeMemoryForModel(st)
			if known && free < uint64(float64(cm.Size)*vramHeadroomFactor) {
				continue
			}
			if st.Disk.Total > 0 && st.Disk.Free < uint64(cm.Size)+reserve {
				continue
			}
			if best == nil || free > bestFree {
				best, bestFree = v, free
			}
		}
		if best != nil {
			out = append(out, Recommendation{Model: cm.ID, NodeUUID: best.UUID, NodeName: best.Name, Reason: "model is served by one node only; a second replica lets the scheduler balance and fail over"})
		}
	}
	return out
}

// ---- HTTP handlers (mounted by the server under /api/cluster) ----

// HandleGet is GET /api/cluster.
func (m *Manager) HandleGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, m.View(r.Context()))
}

// HandleSummary is GET /api/cluster/summary.
func (m *Manager) HandleSummary(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, m.Summary(r.Context()))
}

// HandleCreate is POST /api/cluster.
func (m *Manager) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	info, token, err := m.CreateCluster(req.Name)
	if err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"cluster": info, "join_token": token})
}

// HandleLeave is DELETE /api/cluster.
func (m *Manager) HandleLeave(w http.ResponseWriter, r *http.Request) {
	if err := m.Leave(r.Context()); err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m.View(r.Context()))
}

// HandleJoin is POST /api/cluster/join.
func (m *Manager) HandleJoin(w http.ResponseWriter, r *http.Request) {
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
	if _, err := m.Join(ctx, req.Token, req.Address); err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m.View(r.Context()))
}

// HandleToken is GET /api/cluster/token.
func (m *Manager) HandleToken(w http.ResponseWriter, r *http.Request) {
	if !m.store.InCluster() {
		writeOpError(w, ErrNotClustered)
		return
	}
	token := m.store.JoinToken()
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "available": token != ""})
}

// HandleTokenRotate is POST /api/cluster/token/rotate.
func (m *Manager) HandleTokenRotate(w http.ResponseWriter, r *http.Request) {
	token, err := m.JoinToken(true)
	if err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "available": true})
}

// HandleEnable is POST /api/cluster/enable: start listening and discovering
// without joining anything yet, so unpaired nodes show up.
func (m *Manager) HandleEnable(w http.ResponseWriter, r *http.Request) {
	if err := m.Activate(); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.View(r.Context()))
}

// HandleDisable is POST /api/cluster/disable: back to dormant (not while a
// member; leave first).
func (m *Manager) HandleDisable(w http.ResponseWriter, r *http.Request) {
	if err := m.Deactivate(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.View(r.Context()))
}

// HandleCode is GET /api/cluster/code.
func (m *Manager) HandleCode(w http.ResponseWriter, r *http.Request) {
	// Showing the code means an invite is expected, which needs the
	// listener up.
	if err := m.Activate(); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	code, expires, err := m.store.NodeCode()
	if err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "expires_at": expires})
}

// HandleDiscovered is GET /api/cluster/discovered. A dormant node lists
// nothing and says so; reading must not switch networking on.
func (m *Manager) HandleDiscovered(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"nodes": m.DiscoveredNodes(), "active": m.Active()})
}

// HandleInvite is POST /api/cluster/invite.
func (m *Manager) HandleInvite(w http.ResponseWriter, r *http.Request) {
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
	mem, err := m.Invite(ctx, strings.TrimSpace(req.UUID), req.Code, req.Address)
	if err != nil {
		writeOpError(w, err)
		return
	}
	for _, v := range m.memberViews(r.Context(), false) {
		if v.UUID == mem.UUID {
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	writeJSON(w, http.StatusOK, NodeView{UUID: mem.UUID, Name: mem.Name, Health: HealthUnknown})
}

// HandleNodeUpdate is PUT /api/cluster/nodes/{uuid}.
func (m *Manager) HandleNodeUpdate(w http.ResponseWriter, r *http.Request) {
	nodeUUID := r.PathValue("uuid")
	var req struct {
		Name          *string `json:"name"`
		StaticAddress *string `json:"static_address"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if nodeUUID == m.identity.UUID {
		if req.Name != nil {
			if err := m.Rename(*req.Name); err != nil {
				writeOpError(w, err)
				return
			}
		}
	} else {
		if _, ok := m.store.Member(nodeUUID); !ok {
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
			if _, err := m.UpdateSettings(func(s *Settings) {
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
	for _, v := range m.memberViews(r.Context(), true) {
		if v.UUID == nodeUUID {
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	writeError(w, http.StatusNotFound, "node not found")
}

// HandleNodeRemove is DELETE /api/cluster/nodes/{uuid}.
func (m *Manager) HandleNodeRemove(w http.ResponseWriter, r *http.Request) {
	nodeUUID := r.PathValue("uuid")
	if nodeUUID == m.identity.UUID {
		writeError(w, http.StatusBadRequest, "use DELETE /api/cluster to leave the cluster from this node")
		return
	}
	if err := m.RemoveMember(r.Context(), nodeUUID); err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m.View(r.Context()))
}

// HandleNodeState is POST /api/cluster/nodes/{uuid}/state.
func (m *Manager) HandleNodeState(w http.ResponseWriter, r *http.Request) {
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
	if nodeUUID != m.identity.UUID {
		writeError(w, http.StatusBadRequest, "a node's state is set on that node itself; open its own API or UI")
		return
	}
	s, err := m.UpdateSettings(func(s *Settings) { s.State = req.State })
	if err != nil {
		writeOpError(w, err)
		return
	}
	m.invalidateLocalStatus()
	m.wakeGossip()
	writeJSON(w, http.StatusOK, s)
}

// HandleModels is GET /api/cluster/models.
func (m *Manager) HandleModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"models": m.Models(r.Context())})
}

// HandleExplain is GET /api/cluster/explain.
func (m *Manager) HandleExplain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	model := strings.TrimSpace(q.Get("model"))
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	prompt, _ := strconv.Atoi(q.Get("prompt_tokens"))
	maxTokens, _ := strconv.Atoi(q.Get("max_tokens"))
	writeJSON(w, http.StatusOK, m.Explain(r.Context(), model, prompt, maxTokens, strings.TrimSpace(q.Get("affinity_key"))))
}

// HandleRecommendations is GET /api/cluster/recommendations.
func (m *Manager) HandleRecommendations(w http.ResponseWriter, r *http.Request) {
	recs := m.Recommendations(r.Context())
	if recs == nil {
		recs = []Recommendation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"recommendations": recs})
}

// HandleSettingsUpdate is PUT /api/cluster/settings.
func (m *Manager) HandleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
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
	s, err := m.UpdateSettings(func(s *Settings) {
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
	m.invalidateLocalStatus()
	m.wakeGossip()
	writeJSON(w, http.StatusOK, s)
}

func (m *Manager) invalidateLocalStatus() {
	m.statusMu.Lock()
	m.statusCache = nil
	m.statusMu.Unlock()
}

// HandleModelSync is POST /api/cluster/models/sync: every named node pulls
// the model from its own model source (the on-prem CSGHub in the intended
// deployment).
func (m *Manager) HandleModelSync(w http.ResponseWriter, r *http.Request) {
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
	view := m.View(r.Context())
	if view.ModelSourceMixed {
		writeError(w, http.StatusConflict, "members use different model sources; the same model id may not mean the same files. Point every node at the same CSGHub before syncing")
		return
	}
	models := m.Models(r.Context())
	holders := map[string]bool{}
	for _, cm := range models {
		if cm.ID == req.Model {
			for _, n := range cm.Nodes {
				holders[n.UUID] = true
			}
		}
	}
	members := m.memberViews(r.Context(), true)
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
		repo, artifactSource = m.opts.Host.PullSpec(req.Model)
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
			m.opts.Host.PullHandler().ServeHTTP(rec, req2)
			if rec.status/100 == 2 {
				res.Job = json.RawMessage(rec.body.String())
			} else {
				res.Error = strings.TrimSpace(rec.body.String())
			}
		case !view.Online:
			res.Error = "node is offline"
		default:
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			job, err := m.PullOnNode(ctx, id, body)
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
func (m *Manager) HandleNodePull(w http.ResponseWriter, r *http.Request) {
	nodeUUID := r.PathValue("uuid")
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if nodeUUID == m.identity.UUID {
		req2, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "/api/pull/jobs", strings.NewReader(string(raw)))
		req2.Header.Set("Content-Type", "application/json")
		m.opts.Host.PullHandler().ServeHTTP(w, req2)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	job, err := m.PullOnNode(ctx, nodeUUID, json.RawMessage(raw))
	if err != nil {
		writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// bufferRecorder captures a handler's response in memory.
type bufferRecorder struct {
	header http.Header
	status int
	body   strings.Builder
}

func (b *bufferRecorder) Header() http.Header  { return b.header }
func (b *bufferRecorder) WriteHeader(code int) { b.status = code }
func (b *bufferRecorder) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

// ExplainString is a short human summary used in logs.
func ExplainString(ex Explain) string { return fmt.Sprintf("%s: %s", ex.Model, summarizeExplain(ex)) }
