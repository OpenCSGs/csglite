// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/inference"
)

// ModelNode says where a model lives.
type ModelNode struct {
	UUID   string `json:"uuid"`
	Name   string `json:"name"`
	Loaded bool   `json:"loaded"`
	Online bool   `json:"online"`
	Local  bool   `json:"local"`
}

// ClusterModel is one model with the nodes holding it.
type ClusterModel struct {
	ID          string      `json:"id"`
	Size        int64       `json:"size"`
	Format      string      `json:"format,omitempty"`
	PipelineTag string      `json:"pipeline_tag,omitempty"`
	Category    string      `json:"category,omitempty"`
	Nodes       []ModelNode `json:"nodes"`
}

// Models returns the union of models on every member, this node included.
func (m *Manager) Models(ctx context.Context) []ClusterModel {
	byID := map[string]*ClusterModel{}
	add := func(uuid, name string, local, online bool, st *Status) {
		if st == nil {
			return
		}
		for _, ms := range st.Models {
			cm, ok := byID[ms.ID]
			if !ok {
				cm = &ClusterModel{ID: ms.ID, Size: ms.Size, Format: ms.Format, PipelineTag: ms.PipelineTag, Category: ms.Category}
				byID[ms.ID] = cm
			}
			cm.Nodes = append(cm.Nodes, ModelNode{UUID: uuid, Name: name, Loaded: ms.Loaded, Online: online, Local: local})
		}
	}
	add(m.identity.UUID, m.identity.DisplayName(), true, true, m.cachedLocalStatus(ctx))
	for _, rt := range m.dir.Snapshot() {
		mem, ok := m.store.Member(rt.UUID)
		if !ok {
			continue
		}
		name := mem.Name
		if rt.Status != nil && rt.Status.Name != "" {
			name = rt.Status.Name
		}
		add(rt.UUID, name, false, rt.Online(), rt.Status)
	}
	out := make([]ClusterModel, 0, len(byID))
	for _, cm := range byID {
		sort.Slice(cm.Nodes, func(i, j int) bool { return cm.Nodes[i].Name+cm.Nodes[i].UUID < cm.Nodes[j].Name+cm.Nodes[j].UUID })
		out = append(out, *cm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RemoteHolders lists online members (not this node) that hold a model and
// currently accept work.
func (m *Manager) RemoteHolders(model string) []string {
	var out []string
	for _, rt := range m.dir.Snapshot() {
		if !rt.Online() || rt.Status == nil || !rt.Status.AcceptWork || !rt.Status.Licensed {
			continue
		}
		if _, ok := m.store.Member(rt.UUID); !ok {
			continue
		}
		if _, ok := rt.Status.Model(model); ok && !m.dir.ModelBroken(rt.UUID, model) {
			out = append(out, rt.UUID)
		}
	}
	sort.Strings(out)
	return out
}

// RemoteHasModel reports whether any online member other than this node
// holds the model at all, whatever its admission state. It decides whether a
// request belongs to the cluster router; the scheduler then explains why a
// holder may still be unusable.
func (m *Manager) RemoteHasModel(model string) bool {
	for _, rt := range m.dir.Snapshot() {
		if !rt.Online() || rt.Status == nil {
			continue
		}
		if _, ok := m.store.Member(rt.UUID); !ok {
			continue
		}
		if _, ok := rt.Status.Model(model); ok {
			return true
		}
	}
	return false
}

// Explain runs the scheduler for a hypothetical request.
func (m *Manager) Explain(ctx context.Context, model string, promptTokens, maxTokens int, affinityKey string) Explain {
	e := &clusterEngine{m: m, model: model, affinity: affinityKey}
	_, ex := e.rank(promptTokens, maxTokens)
	return ex
}

// PeerModel is a complete copy of a model on a member, ready to be copied.
type PeerModel struct {
	Node     Member
	Addr     string
	Manifest json.RawMessage
	Files    []BundleFile
	Extras   []BundleFile
}

// TotalSize sums the required files.
func (p *PeerModel) TotalSize() int64 {
	var total int64
	for _, f := range p.Files {
		total += f.Size
	}
	return total
}

// ExtrasSize sums the optional derived files.
func (p *PeerModel) ExtrasSize() int64 {
	var total int64
	for _, f := range p.Extras {
		total += f.Size
	}
	return total
}

// FindPeerModel looks for an online member that holds modelID completely and
// returns its bundle. Members are tried in scheduler order (idle, fast disk
// first); nil is returned when no member has the model.
func (m *Manager) FindPeerModel(ctx context.Context, modelID string) (*PeerModel, error) {
	var lastErr error
	for _, rt := range m.dir.Snapshot() {
		if !rt.Online() || rt.Status == nil {
			continue
		}
		ms, ok := rt.Status.Model(modelID)
		if !ok || ms.Loading {
			continue
		}
		mem, ok := m.store.Member(rt.UUID)
		if !ok {
			continue
		}
		for _, addr := range m.dir.Candidates(rt.UUID)[:min(2, len(m.dir.Candidates(rt.UUID)))] {
			var bundle ModelBundle
			attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := m.peerJSON(attempt, mem, addr, http.MethodGet, peerPathModelBundle+"?model="+url.QueryEscape(modelID), nil, &bundle)
			cancel()
			if err != nil {
				lastErr = err
				continue
			}
			if len(bundle.Files) == 0 {
				lastErr = fmt.Errorf("%s reports an empty model", mem.Name)
				continue
			}
			// A bundle is data from another machine, and the paths in it
			// become file names on this one. A member that is compromised or
			// simply running something else could name "../../etc/x" and have
			// us write outside the model directory, so the whole bundle is
			// rejected unless every path is a plain relative one. The peer
			// that serves a file applies the same rule, but a caller cannot
			// rely on the other end to check on its behalf.
			if err := checkBundlePaths(bundle); err != nil {
				lastErr = fmt.Errorf("%s offered %s: %w", mem.Name, modelID, err)
				m.logf("cluster: refusing the copy of %s from %s: %v", modelID, mem.Name, err)
				continue
			}
			return &PeerModel{Node: mem, Addr: addr, Manifest: bundle.Manifest, Files: bundle.Files, Extras: bundle.Extras}, nil
		}
	}
	return nil, lastErr
}

// OpenPeerFile streams one file of a peer model. The response body is the
// file; after it is read to EOF, resp.Trailer.Get(SHA256Trailer) holds the
// digest the peer computed while sending.
func (m *Manager) OpenPeerFile(ctx context.Context, pm *PeerModel, rel string) (*http.Response, error) {
	client := m.peers.get(pm.Node.UUID, pm.Node.CertFingerprint)
	q := url.Values{"model": {modelIDOfManifest(pm)}, "path": {rel}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+pm.Addr+peerPathModelFile+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("%s answered %d for %s: %s", pm.Node.Name, resp.StatusCode, rel, strings.TrimSpace(string(raw)))
	}
	m.dir.RequestSucceeded(pm.Node.UUID)
	return resp, nil
}

// modelIDOfManifest recovers the cluster model id a peer bundle was requested
// under; it is stored on the PeerModel by FindPeerModel's caller through the
// manifest, so we read it back from there.
func modelIDOfManifest(pm *PeerModel) string {
	var manifest struct {
		Namespace      string `json:"namespace"`
		Name           string `json:"name"`
		ArtifactSource string `json:"artifact_source"`
		Repository     string `json:"repository"`
	}
	_ = json.Unmarshal(pm.Manifest, &manifest)
	repo := strings.Trim(manifest.Repository, "/")
	if repo == "" {
		repo = manifest.Namespace + "/" + manifest.Name
	}
	source := strings.ToLower(strings.TrimSpace(manifest.ArtifactSource))
	if source == "" || source == "opencsg" {
		return repo
	}
	return source + "/" + repo
}

// RouteRaw places a request whose body is not JSON chat (audio uploads,
// speech synthesis) on the best member and returns the member's raw response.
// It returns ErrServeLocally when this node is the best choice, so the caller
// runs its normal handler; the response of a remote node is streamed back
// as-is with the node headers set.
func (m *Manager) RouteRaw(ctx context.Context, modelID, source, path string, body []byte, headers http.Header) (*http.Response, error) {
	pinned := NodeUUIDFromSource(source)
	if pinned != "" && pinned == m.identity.UUID {
		return nil, &LocalChoice{release: m.dir.Reserve(m.identity.UUID)}
	}
	if pinned != "" {
		if _, ok := m.store.Member(pinned); !ok {
			return nil, inference.NewHTTPStatusError(http.StatusNotFound, fmt.Sprintf("node %s is not a member of this cluster", shortUUID(pinned)))
		}
	}
	if !m.store.InCluster() && pinned == "" {
		return nil, inference.NewHTTPStatusError(http.StatusNotFound, "this node is not part of a cluster")
	}
	e := &clusterEngine{m: m, model: modelID, pinned: pinned, affinity: AffinityKeyFromContext(ctx)}
	return e.dispatch(ctx, len(body)/4, 1, func(target Ranked) (*http.Response, error) {
		if target.Local {
			return nil, ErrServeLocally
		}
		mem, ok := m.store.Member(target.UUID)
		if !ok {
			return nil, &routeError{status: 0, err: fmt.Errorf("node %s is no longer a member", shortUUID(target.UUID))}
		}
		return m.newNodeEngine(mem, modelID, false).forward(ctx, path, body, headers)
	})
}
