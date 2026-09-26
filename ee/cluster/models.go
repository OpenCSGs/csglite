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

// ModelSync is a download of this model that has not finished on a node.
type ModelSync struct {
	UUID           string `json:"uuid"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	CompletedBytes int64  `json:"completed_bytes,omitempty"`
	TotalBytes     int64  `json:"total_bytes,omitempty"`
	Detail         string `json:"detail,omitempty"`
}

// ClusterModel is one model with the nodes holding it.
type ClusterModel struct {
	ID string `json:"id"`
	// Repository is the library name (namespace/name). Copies from different
	// registries share it, so the distribution view treats them as one model.
	Repository  string      `json:"repository,omitempty"`
	Size        int64       `json:"size"`
	Format      string      `json:"format,omitempty"`
	PipelineTag string      `json:"pipeline_tag,omitempty"`
	Category    string      `json:"category,omitempty"`
	Nodes       []ModelNode `json:"nodes"`
	Syncs       []ModelSync `json:"syncs,omitempty"`
}

// modelGroup collects every registry copy of one library model.
type modelGroup struct {
	model ClusterModel
	ids   map[string]int
	seen  map[string]int
}

// Models returns the union of models on every member, this node included.
// A repository downloaded from more than one registry is one row: a node that
// holds any copy has the model.
func (m *Manager) Models(ctx context.Context) []ClusterModel {
	byKey := map[string]*modelGroup{}
	add := func(uuid, name string, local, online bool, st *Status) {
		if st == nil {
			return
		}
		for _, ms := range st.Models {
			key := modelIdentity(ms)
			if key == "" {
				continue
			}
			group, ok := byKey[key]
			if !ok {
				group = &modelGroup{ids: map[string]int{}, seen: map[string]int{}}
				group.model = ClusterModel{Size: ms.Size, Format: ms.Format, PipelineTag: ms.PipelineTag, Category: ms.Category}
				byKey[key] = group
			}
			if repo := normalizeRepo(ms.Repo); repo != "" {
				group.model.Repository = repo
			}
			if id := strings.TrimSpace(ms.ID); id != "" {
				group.ids[id]++
			}
			if ms.Size > group.model.Size {
				group.model.Size = ms.Size
			}
			if group.model.Format == "" {
				group.model.Format = ms.Format
			}
			if group.model.PipelineTag == "" {
				group.model.PipelineTag = ms.PipelineTag
			}
			if group.model.Category == "" {
				group.model.Category = ms.Category
			}
			if idx, ok := group.seen[uuid]; ok {
				if ms.Loaded {
					group.model.Nodes[idx].Loaded = true
				}
				continue
			}
			group.seen[uuid] = len(group.model.Nodes)
			group.model.Nodes = append(group.model.Nodes, ModelNode{UUID: uuid, Name: name, Loaded: ms.Loaded, Online: online, Local: local})
		}
		attachPulls(byKey, uuid, name, st)
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
	out := make([]ClusterModel, 0, len(byKey))
	for _, group := range byKey {
		group.model.ID = choosePublicID(group.ids)
		if group.model.ID == "" {
			group.model.ID = group.model.Repository
		}
		if group.model.Nodes == nil {
			group.model.Nodes = []ModelNode{}
		}
		sort.Slice(group.model.Nodes, func(i, j int) bool {
			return group.model.Nodes[i].Name+group.model.Nodes[i].UUID < group.model.Nodes[j].Name+group.model.Nodes[j].UUID
		})
		sort.Slice(group.model.Syncs, func(i, j int) bool {
			return group.model.Syncs[i].Name+group.model.Syncs[i].UUID < group.model.Syncs[j].Name+group.model.Syncs[j].UUID
		})
		out = append(out, group.model)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// attachPulls records a download on the row for that repository. A node that
// already holds the model is left as present; the bar is only for a copy
// that has not landed yet.
func attachPulls(byKey map[string]*modelGroup, uuid, name string, st *Status) {
	if st == nil {
		return
	}
	for _, pull := range st.Jobs.Pulls {
		if pull.Status != "queued" && pull.Status != "running" {
			continue
		}
		repo := normalizeRepo(pull.Model)
		if repo == "" {
			continue
		}
		group := modelGroupForPull(byKey, pull)
		if group == nil {
			group = &modelGroup{ids: map[string]int{repo: 1}, seen: map[string]int{}}
			group.model = ClusterModel{Repository: repo}
			byKey[repo] = group
		}
		if _, held := group.seen[uuid]; held {
			continue
		}
		group.model.Syncs = append(group.model.Syncs, ModelSync{
			UUID:           uuid,
			Name:           name,
			Status:         pull.Status,
			CompletedBytes: pull.CompletedBytes,
			TotalBytes:     pull.TotalBytes,
			Detail:         pull.Detail,
		})
	}
}

func modelGroupForPull(byKey map[string]*modelGroup, pull PullStatus) *modelGroup {
	repo := normalizeRepo(pull.Model)
	if repo == "" {
		return nil
	}
	if group, ok := byKey[repo]; ok {
		return group
	}
	source := strings.ToLower(strings.TrimSpace(pull.Source))
	if source != "" && source != "opencsg" {
		if group, ok := byKey[source+"/"+repo]; ok {
			return group
		}
	}
	for _, group := range byKey {
		if group.model.Repository == repo || group.model.ID == repo {
			return group
		}
		if source != "" && source != "opencsg" && group.model.ID == source+"/"+repo {
			return group
		}
	}
	return nil
}

// modelIdentity is the library name when the node reported a repository, and
// the public inference id otherwise.
func modelIdentity(ms ModelStatus) string {
	if repo := normalizeRepo(ms.Repo); repo != "" {
		return repo
	}
	return strings.TrimSpace(ms.ID)
}

func normalizeRepo(repo string) string {
	return strings.Trim(strings.TrimSpace(repo), "/")
}

// choosePublicID picks the inference id most members advertise. A tie prefers
// the shorter id so the row stays stable.
func choosePublicID(counts map[string]int) string {
	best, bestN := "", -1
	for id, n := range counts {
		if n > bestN || (n == bestN && preferPublicID(id, best)) {
			best, bestN = id, n
		}
	}
	return best
}

func preferPublicID(id, current string) bool {
	if strings.Count(id, "/") != strings.Count(current, "/") {
		return strings.Count(id, "/") < strings.Count(current, "/")
	}
	return id < current
}

// modelStatusMatches reports whether a stored copy is the requested model.
// The request may be the public inference id, the library repository, or
// source/namespace/name.
func modelStatusMatches(ms ModelStatus, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if ms.ID == id {
		return true
	}
	repo := normalizeRepo(ms.Repo)
	if repo == "" {
		return false
	}
	if id == repo {
		return true
	}
	// OpenCSG advertises the model under its short name. That name is the
	// repository's last segment, whichever registry the copy came from.
	if !strings.Contains(id, "/") {
		if name := repo[strings.LastIndex(repo, "/")+1:]; name == id {
			return true
		}
	}
	source := strings.ToLower(strings.TrimSpace(ms.Source))
	if source != "" && source != "opencsg" && id == source+"/"+repo {
		return true
	}
	prefix, rest, ok := splitRegistryID(id)
	return ok && prefix != "" && rest == repo
}

// pullSpecForModel returns the repository and artifact source a pull job
// needs. An exact public id wins; otherwise any copy of the same repository
// is enough, because the files are the model the library already shows.
func pullSpecForModel(members []NodeView, modelID string) (string, string) {
	modelID = strings.TrimSpace(modelID)
	repo, source := "", ""
	for _, mv := range members {
		if mv.Status == nil {
			continue
		}
		for _, ms := range mv.Status.Models {
			if !modelStatusMatches(ms, modelID) || normalizeRepo(ms.Repo) == "" {
				continue
			}
			if ms.ID == modelID {
				return normalizeRepo(ms.Repo), strings.TrimSpace(ms.Source)
			}
			if repo == "" {
				repo, source = normalizeRepo(ms.Repo), strings.TrimSpace(ms.Source)
			}
		}
	}
	return repo, source
}

func splitRegistryID(id string) (string, string, bool) {
	slash := strings.Index(id, "/")
	if slash <= 0 || strings.Count(id, "/") < 2 {
		return "", "", false
	}
	prefix := strings.ToLower(id[:slash])
	switch prefix {
	case "huggingface", "modelscope":
		return prefix, id[slash+1:], true
	default:
		return "", "", false
	}
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
