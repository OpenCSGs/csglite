// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/httpjson"
)

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
	// Spans are the models this node runs across several machines, and
	// SpanWorker says it is lending its memory to another node's. Both are
	// reported here so the whole cluster can be seen from any node.
	Spans      []SpanView `json:"spans,omitempty"`
	SpanWorker bool       `json:"span_worker,omitempty"`
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
			n.Spans = st.Spans
			n.SpanWorker = st.SpanWorker
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

// bufferRecorder captures a handler's response in memory.
type bufferRecorder struct {
	header http.Header
	status int
	body   strings.Builder
}

func (b *bufferRecorder) Header() http.Header { return b.header }

func (b *bufferRecorder) WriteHeader(code int) { b.status = code }

func (b *bufferRecorder) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}
