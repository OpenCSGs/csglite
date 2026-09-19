// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"encoding/json"
	"time"
)

// ProtocolVersion is carried in discovery records and every handshake. Nodes
// with a different major version see each other but refuse to pair or route.
const ProtocolVersion = 1

// Wire paths on the node-to-node listener.
const (
	peerPathJoin      = "/cluster/v1/join"
	peerPathInvite    = "/cluster/v1/invite"
	peerPathStatus    = "/cluster/v1/status"
	peerPathGossip    = "/cluster/v1/gossip"
	peerPathLeave     = "/cluster/v1/leave"
	peerPathPull      = "/cluster/v1/pull"
	peerPathInference = "/cluster/v1/inference/"
	// peerPathModelBundle and peerPathModelFile let a member copy a model
	// from a peer that already holds it, instead of downloading it again from
	// the model source over the internet.
	peerPathModelBundle = "/cluster/v1/model-bundle"
	peerPathModelFile   = "/cluster/v1/model-file"
	// SHA256Trailer carries the digest of a streamed model file.
	SHA256Trailer = "X-CSGLite-SHA256"
)

// Headers added to routed requests and cluster responses.
const (
	// RoutedHeader marks a request an entry node forwarded; the value is the
	// entry node's UUID. The executing node serves it locally and never
	// routes it again.
	RoutedHeader = "X-CSGLite-Routed"
	// NodeHeader names the node that executed a request.
	NodeHeader = "X-CSGLite-Node"
	// NodeNameHeader is the display name of that node.
	NodeNameHeader = "X-CSGLite-Node-Name"
)

// GPUStatus is one accelerator as the node reports it.
type GPUStatus struct {
	Index       int     `json:"index"`
	Name        string  `json:"name"`
	VRAMTotal   uint64  `json:"vram_total"`
	VRAMUsed    uint64  `json:"vram_used"`
	Util        *int    `json:"util,omitempty"`
	Temperature *int    `json:"temperature,omitempty"`
	PowerDraw   *int    `json:"power_draw,omitempty"`
	PowerLimit  *int    `json:"power_limit,omitempty"`
	Throttled   bool    `json:"throttled"`
	Shared      bool    `json:"shared_memory,omitempty"`
	UsageKnown  bool    `json:"usage_known"`
	Score       float64 `json:"-"`
}

// CPUStatus is the host CPU.
type CPUStatus struct {
	Cores int      `json:"cores"`
	Load1 *float64 `json:"load1,omitempty"`
	Util  *float64 `json:"util,omitempty"`
	Model string   `json:"model,omitempty"`
}

// RAMStatus is host memory.
type RAMStatus struct {
	Total   uint64 `json:"total"`
	Used    uint64 `json:"used"`
	Unified bool   `json:"unified"`
}

// DiskStatus describes the model directory's volume.
type DiskStatus struct {
	Path          string `json:"path"`
	Total         uint64 `json:"total"`
	Free          uint64 `json:"free"`
	ReadMBpsClass string `json:"read_mbps_class,omitempty"`
	ReadMBps      int    `json:"read_mbps,omitempty"`
	IOBusy        bool   `json:"io_busy"`
}

// NetStatus describes the primary interface.
type NetStatus struct {
	LinkMbps int      `json:"link_mbps,omitempty"`
	Addrs    []string `json:"addrs,omitempty"`
}

// JobsStatus lists background work that competes for disk and GPU.
type JobsStatus struct {
	Pulling    []string `json:"pulling"`
	Converting []string `json:"converting"`
}

// ModelPerf is the node's measured performance for one model.
type ModelPerf struct {
	DecodeTPS   float64 `json:"decode_tps,omitempty"`
	PromptTPS   float64 `json:"prompt_tps,omitempty"`
	LoadSeconds float64 `json:"load_seconds,omitempty"`
	Samples     int     `json:"samples,omitempty"`
}

// ModelStatus is one model present on a node.
type ModelStatus struct {
	ID                  string     `json:"id"`
	Size                int64      `json:"size"`
	Format              string     `json:"format,omitempty"`
	PipelineTag         string     `json:"pipeline_tag,omitempty"`
	Category            string     `json:"category,omitempty"`
	Loaded              bool       `json:"loaded"`
	Loading             bool       `json:"loading"`
	Slots               int        `json:"slots,omitempty"`
	Active              int        `json:"active"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	NGPULayers          int        `json:"n_gpu_layers,omitempty"`
	NativeToolStreaming bool       `json:"native_tool_streaming"`
	Perf                *ModelPerf `json:"perf,omitempty"`
}

// ModelSource is where a node downloads models from; members whose sources
// differ may hold different bytes under the same model ID.
type ModelSource struct {
	ServerURL          string `json:"server_url"`
	HFEndpoint         string `json:"hf_endpoint,omitempty"`
	ModelScopeEndpoint string `json:"modelscope_endpoint,omitempty"`
}

// Status is everything a node tells its peers about itself.
type Status struct {
	UUID        string        `json:"uuid"`
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	Protocol    int           `json:"protocol"`
	ClusterUUID string        `json:"cluster_uuid,omitempty"`
	Licensed    bool          `json:"licensed"`
	NodeLimit   int           `json:"node_limit"`
	AcceptWork  bool          `json:"accept_work"`
	State       NodeState     `json:"state"`
	Weight      int           `json:"weight"`
	APIPort     int           `json:"api_port"`
	ClusterPort int           `json:"cluster_port"`
	Hostname    string        `json:"hostname,omitempty"`
	OS          string        `json:"os,omitempty"`
	Arch        string        `json:"arch,omitempty"`
	GPUs        []GPUStatus   `json:"gpus"`
	CPU         CPUStatus     `json:"cpu"`
	RAM         RAMStatus     `json:"ram"`
	Disk        DiskStatus    `json:"disk"`
	Net         NetStatus     `json:"net"`
	Jobs        JobsStatus    `json:"jobs"`
	ModelSource ModelSource   `json:"model_source"`
	Models      []ModelStatus `json:"models"`
	Inflight    int           `json:"inflight"`
	UptimeSec   int64         `json:"uptime_sec"`
	Time        time.Time     `json:"time"`
}

// Model finds a model on the node.
func (s *Status) Model(id string) (ModelStatus, bool) {
	if s == nil {
		return ModelStatus{}, false
	}
	for _, m := range s.Models {
		if m.ID == id {
			return m, true
		}
	}
	return ModelStatus{}, false
}

// VRAMFree sums free memory across GPUs.
func (s *Status) VRAMFree() uint64 {
	if s == nil {
		return 0
	}
	var free uint64
	for _, g := range s.GPUs {
		if g.VRAMTotal > g.VRAMUsed {
			free += g.VRAMTotal - g.VRAMUsed
		}
	}
	return free
}

// VRAMTotal sums memory across GPUs.
func (s *Status) VRAMTotal() uint64 {
	if s == nil {
		return 0
	}
	var total uint64
	for _, g := range s.GPUs {
		total += g.VRAMTotal
	}
	return total
}

// ---- handshake and gossip messages ----

// nodeCard is how a node introduces itself in handshakes and gossip.
type nodeCard struct {
	UUID        string   `json:"uuid"`
	Name        string   `json:"name"`
	CertPEM     string   `json:"cert_pem,omitempty"`
	Fingerprint string   `json:"cert_fingerprint"`
	APIPort     int      `json:"api_port"`
	ClusterPort int      `json:"cluster_port"`
	Addresses   []string `json:"addresses,omitempty"`
	Version     string   `json:"version"`
	Protocol    int      `json:"protocol"`
}

// joinRequest is sent by a node that wants to join, to any member.
type joinRequest struct {
	Node  nodeCard `json:"node"`
	Nonce string   `json:"nonce"`
	TS    int64    `json:"ts"`
	MAC   string   `json:"mac"`
}

// clusterView is the membership a peer hands over.
type clusterView struct {
	Cluster       ClusterInfo `json:"cluster"`
	JoinTokenHash string      `json:"join_token_hash,omitempty"`
	Members       []nodeCard  `json:"members"`
	Tombstones    []Tombstone `json:"tombstones,omitempty"`
}

// joinResponse is the member's answer: the cluster and everyone in it.
type joinResponse struct {
	clusterView
	Responder nodeCard `json:"responder"`
}

// inviteRequest is sent by a member to an unpaired node, authenticated by
// that node's admission code.
type inviteRequest struct {
	clusterView
	Inviter nodeCard `json:"inviter"`
	Nonce   string   `json:"nonce"`
	TS      int64    `json:"ts"`
	MAC     string   `json:"mac"`
}

// inviteResponse is the joining node's card.
type inviteResponse struct {
	Node nodeCard `json:"node"`
}

// gossipMessage is exchanged periodically between members.
type gossipMessage struct {
	ClusterUUID string      `json:"cluster_uuid"`
	Sender      nodeCard    `json:"sender"`
	Members     []nodeCard  `json:"members"`
	Tombstones  []Tombstone `json:"tombstones,omitempty"`
	// Observed is the address the sender reached the receiver on, so the
	// receiver learns which of its addresses peers actually use.
	Observed string `json:"observed,omitempty"`
}

// leaveMessage announces a departure.
type leaveMessage struct {
	UUID string `json:"uuid"`
}

// errorResponse is the JSON error body on the peer listener.
type errorResponse struct {
	Error     string `json:"error"`
	ErrorCode int    `json:"errorCode"`
	Code      string `json:"code,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Current   int    `json:"current,omitempty"`
}

// BundleFile is one file of a complete local model.
type BundleFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// ModelBundle describes a complete local model so a peer can copy it: the
// manifest as stored on disk, the files the manifest lists (what a fresh
// download produces) and any extra artifacts the node derived from them,
// such as a GGUF converted from safetensors. Extras are optional for the
// receiver: it can regenerate them, so on a slow link it skips them.
type ModelBundle struct {
	Dir      string          `json:"-"`
	Manifest json.RawMessage `json:"manifest"`
	Files    []BundleFile    `json:"files"`
	Extras   []BundleFile    `json:"extras,omitempty"`
}
