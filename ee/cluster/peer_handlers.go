// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/httpjson"
)

// peerAPI serves /cluster/v1/*, the protocol nodes speak to each other over
// mutual TLS. It is separate from the operator API and from the manager
// itself: these handlers answer another machine, not a person, and the rules
// differ, starting with the caller being identified by its pinned certificate
// rather than by a key.
type peerAPI struct{ m *Manager }

// Peers returns this node's node-to-node API.
func (m *Manager) Peers() *peerAPI { return &peerAPI{m: m} }

// handshakeSkew bounds how old a join or invite request may be, to blunt
// replay of a captured handshake.
const handshakeSkew = 5 * time.Minute

// decodePeerJSON reads a node-to-node body. Unlike the operator API a blank
// body is an error here: every peer message has required fields.
func decodePeerJSON(r *http.Request, out any, limit int64) error {
	raw, err := httpjson.ReadBody(r, limit)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// peerMux routes the node-to-node listener.
func (a *peerAPI) peerMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+peerPathJoin, a.handlePeerJoin)
	mux.HandleFunc("POST "+peerPathInvite, a.handlePeerInvite)
	mux.HandleFunc("GET "+peerPathStatus, a.handlePeerStatus)
	mux.HandleFunc("POST "+peerPathGossip, a.requirePeer(a.handlePeerGossip))
	mux.HandleFunc("POST "+peerPathLeave, a.requirePeer(a.handlePeerLeave))
	mux.HandleFunc("POST "+peerPathPull, a.requirePeer(a.handlePeerPull))
	mux.Handle(peerPathInference, a.requirePeer(a.handlePeerInference))
	mux.HandleFunc("GET "+peerPathModelBundle, a.requirePeer(a.handlePeerModelBundle))
	mux.HandleFunc("GET "+peerPathModelFile, a.requirePeer(a.handlePeerModelFile))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeCodedError(w, http.StatusNotFound, "not a cluster endpoint", "not_found")
	})
	return mux
}

// peerPinned answers the transport's pin check from the member table.
func (a *peerAPI) peerPinned(nodeUUID, fingerprint string) bool {
	fp, ok := a.m.store.FingerprintFor(nodeUUID)
	return ok && fp == fingerprint
}

// requirePeer admits only pinned members and records the address they came
// from; the client certificate is the authentication.
func (a *peerAPI) requirePeer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peer := peerFromRequest(r, a.peerPinned)
		if peer == "" {
			writeCodedError(w, http.StatusForbidden, "this node is not a paired member of the cluster", "not_a_member")
			return
		}
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			a.m.dir.LearnAddress(peer, endpoint(host, a.memberClusterPort(peer)), time.Now())
			a.m.dir.RequestSucceeded(peer)
		}
		r.Header.Set("X-CSGLite-Peer", peer)
		next(w, r)
	}
}

func (a *peerAPI) memberClusterPort(nodeUUID string) int {
	if mem, ok := a.m.store.Member(nodeUUID); ok && mem.ClusterPort > 0 {
		return mem.ClusterPort
	}
	return portOf(DefaultListenAddr)
}

// GET /cluster/v1/status
// Members get the full status. Anyone may ask with ?public=1 and receives the
// identity fields only, which is what seed probing and the discovered-node
// list need; hardware and model inventory are cluster data.
func (a *peerAPI) handlePeerStatus(w http.ResponseWriter, r *http.Request) {
	peer := peerFromRequest(r, a.peerPinned)
	if peer == "" {
		if r.URL.Query().Get("public") != "1" {
			writeCodedError(w, http.StatusForbidden, "this node is not a paired member of the cluster", "not_a_member")
			return
		}
		st := a.m.cachedLocalStatus(r.Context())
		writeJSON(w, http.StatusOK, Status{
			UUID: st.UUID, Name: st.Name, Version: st.Version, Protocol: st.Protocol, ClusterUUID: st.ClusterUUID,
			Licensed: st.Licensed, NodeLimit: st.NodeLimit, APIPort: st.APIPort, ClusterPort: st.ClusterPort,
			Hostname: st.Hostname, OS: st.OS, Arch: st.Arch, Time: st.Time, Models: []ModelStatus{}, GPUs: []GPUStatus{},
		})
		return
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		a.m.dir.LearnAddress(peer, endpoint(host, a.memberClusterPort(peer)), time.Now())
	}
	writeJSON(w, http.StatusOK, a.m.cachedLocalStatus(r.Context()))
}

// POST /cluster/v1/join?cluster=<uuid>
func (a *peerAPI) handlePeerJoin(w http.ResponseWriter, r *http.Request) {
	c := a.m.store.Cluster()
	if c == nil {
		writeCodedError(w, http.StatusConflict, "this node is not in a cluster", "not_clustered")
		return
	}
	if r.URL.Query().Get("cluster") != c.UUID {
		writeCodedError(w, http.StatusForbidden, "join token names a different cluster", "wrong_cluster")
		return
	}
	var req joinRequest
	if err := decodePeerJSON(r, &req, 1<<20); err != nil {
		writeCodedError(w, http.StatusBadRequest, "invalid join request", "bad_request")
		return
	}
	if req.Node.Protocol != ProtocolVersion {
		writeCodedError(w, http.StatusBadRequest, "incompatible cluster protocol version", "protocol_mismatch")
		return
	}
	if abs64(time.Now().Unix()-req.TS) > int64(handshakeSkew.Seconds()) {
		writeCodedError(w, http.StatusForbidden, "join request is too old; check the clocks", "stale_handshake")
		return
	}
	mem, err := a.m.memberFromCard(req.Node)
	if err != nil || req.Node.CertPEM == "" {
		writeCodedError(w, http.StatusBadRequest, "join request carries no valid certificate", "bad_certificate")
		return
	}
	if mem.UUID == a.m.identity.UUID {
		writeCodedError(w, http.StatusBadRequest, "a node cannot join itself", "bad_request")
		return
	}
	// The MAC key is the token hash both sides can derive; the secret itself
	// never crosses the network.
	want := HandshakeMAC(a.m.store.JoinTokenHash(), mem.UUID, mem.CertFingerprint, req.Nonce, req.TS)
	if a.m.store.JoinTokenHash() == "" || !hmacEqual(want, req.MAC) {
		writeCodedError(w, http.StatusUnauthorized, "join token is not valid for this cluster", "bad_token")
		return
	}
	if _, already := a.m.store.Member(mem.UUID); !already {
		if !a.m.withinNodeLimit(len(a.m.store.Members()) + 2) {
			writeJSON(w, http.StatusForbidden, errorResponse{
				Error:     "the cluster has reached its licensed node limit",
				ErrorCode: http.StatusForbidden, Code: "feature_not_licensed",
				Limit: a.m.NodeLimit(), Current: len(a.m.store.Members()) + 1,
			})
			return
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		mem.LastAddresses = append([]string{endpoint(host, mem.ClusterPort)}, mem.LastAddresses...)
	}
	if _, err := a.m.store.Upsert(mem, true); err != nil {
		writeCodedError(w, http.StatusInternalServerError, err.Error(), "store_error")
		return
	}
	a.m.dir.Track(mem.UUID, a.m.seedAddressesFor(mem))
	for _, addr := range mem.LastAddresses {
		a.m.dir.LearnAddress(mem.UUID, addr, time.Now())
	}
	a.m.dir.DropDiscovered(mem.UUID)
	a.m.wakeGossip()
	a.m.logf("cluster: node %s (%s) joined via this node", shortUUID(mem.UUID), mem.Name)
	writeJSON(w, http.StatusOK, joinResponse{clusterView: a.m.view(), Responder: a.m.card()})
}

// POST /cluster/v1/invite
func (a *peerAPI) handlePeerInvite(w http.ResponseWriter, r *http.Request) {
	if a.m.store.InCluster() {
		writeCodedError(w, http.StatusConflict, "this node already belongs to a cluster", "already_clustered")
		return
	}
	var req inviteRequest
	if err := decodePeerJSON(r, &req, 4<<20); err != nil {
		writeCodedError(w, http.StatusBadRequest, "invalid invite", "bad_request")
		return
	}
	if req.Inviter.Protocol != ProtocolVersion {
		writeCodedError(w, http.StatusBadRequest, "incompatible cluster protocol version", "protocol_mismatch")
		return
	}
	if abs64(time.Now().Unix()-req.TS) > int64(handshakeSkew.Seconds()) {
		writeCodedError(w, http.StatusForbidden, "invite is too old; check the clocks", "stale_handshake")
		return
	}
	inviter, err := a.m.memberFromCard(req.Inviter)
	if err != nil || req.Inviter.CertPEM == "" {
		writeCodedError(w, http.StatusBadRequest, "invite carries no valid certificate", "bad_certificate")
		return
	}
	// The inviter must be the TLS peer, so the code cannot be relayed.
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || CertFingerprint(r.TLS.PeerCertificates[0]) != inviter.CertFingerprint {
		writeCodedError(w, http.StatusForbidden, "invite must come from the inviting node itself", "bad_certificate")
		return
	}
	code, expires := a.peekNodeCode()
	if code == "" || time.Now().After(expires) {
		writeCodedError(w, http.StatusForbidden, "no admission code is active on this node", "bad_code")
		return
	}
	want := HandshakeMAC(code, inviter.UUID, inviter.CertFingerprint, req.Nonce, req.TS)
	if !hmacEqual(want, req.MAC) {
		writeCodedError(w, http.StatusUnauthorized, "admission code is wrong", "bad_code")
		return
	}
	if req.Cluster.UUID == "" {
		writeCodedError(w, http.StatusBadRequest, "invite names no cluster", "bad_request")
		return
	}
	if !a.m.withinNodeLimit(len(req.Members) + 1) {
		writeJSON(w, http.StatusForbidden, errorResponse{
			Error:     "this node's license does not allow a cluster of that size",
			ErrorCode: http.StatusForbidden, Code: "feature_not_licensed",
			Limit: a.m.NodeLimit(), Current: len(req.Members),
		})
		return
	}
	if !a.m.store.VerifyNodeCode(code) {
		writeCodedError(w, http.StatusUnauthorized, "admission code is no longer valid", "bad_code")
		return
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		inviter.LastAddresses = append([]string{endpoint(host, inviter.ClusterPort)}, inviter.LastAddresses...)
	}
	members := []Member{inviter}
	for _, card := range req.Members {
		if card.UUID == a.m.identity.UUID || card.UUID == inviter.UUID {
			continue
		}
		mem, err := a.m.memberFromCard(card)
		if err != nil {
			continue
		}
		members = append(members, mem)
	}
	if err := a.m.store.Adopt(req.Cluster, req.JoinTokenHash, members, a.m.identity.UUID); err != nil {
		writeCodedError(w, http.StatusInternalServerError, err.Error(), "store_error")
		return
	}
	a.m.dir.Reset()
	for _, mem := range members {
		a.m.dir.Track(mem.UUID, a.m.seedAddressesFor(mem))
		a.m.dir.DropDiscovered(mem.UUID)
	}
	a.m.reannounce()
	a.m.wakeGossip()
	a.m.logf("cluster: joined %q (%s) by invitation from %s", req.Cluster.Name, shortUUID(req.Cluster.UUID), inviter.Name)
	writeJSON(w, http.StatusOK, inviteResponse{Node: a.m.card()})
}

func (a *peerAPI) peekNodeCode() (string, time.Time) {
	a.m.store.mu.RLock()
	defer a.m.store.mu.RUnlock()
	if a.m.store.data.NodeCode == nil {
		return "", time.Time{}
	}
	return a.m.store.data.NodeCode.Code, a.m.store.data.NodeCode.ExpiresAt
}

// POST /cluster/v1/gossip
func (a *peerAPI) handlePeerGossip(w http.ResponseWriter, r *http.Request) {
	c := a.m.store.Cluster()
	if c == nil {
		writeCodedError(w, http.StatusConflict, "this node is not in a cluster", "not_clustered")
		return
	}
	var msg gossipMessage
	if err := decodePeerJSON(r, &msg, 4<<20); err != nil {
		writeCodedError(w, http.StatusBadRequest, "invalid gossip", "bad_request")
		return
	}
	if msg.ClusterUUID != c.UUID {
		writeCodedError(w, http.StatusForbidden, "gossip for another cluster", "wrong_cluster")
		return
	}
	peer := r.Header.Get("X-CSGLite-Peer")
	if msg.Sender.UUID != peer {
		writeCodedError(w, http.StatusForbidden, "gossip sender does not match the connection", "bad_sender")
		return
	}
	from := ""
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		from = endpoint(host, a.memberClusterPort(peer))
	}
	a.m.mergeGossip(msg, from)
	reply := gossipMessage{ClusterUUID: c.UUID, Sender: a.m.card(), Tombstones: a.m.store.Tombstones()}
	for _, mem := range a.m.store.Members() {
		reply.Members = append(reply.Members, a.m.cardFromMember(mem))
	}
	writeJSON(w, http.StatusOK, reply)
}

// POST /cluster/v1/leave
func (a *peerAPI) handlePeerLeave(w http.ResponseWriter, r *http.Request) {
	var msg leaveMessage
	if err := decodePeerJSON(r, &msg, 64<<10); err != nil {
		writeCodedError(w, http.StatusBadRequest, "invalid leave message", "bad_request")
		return
	}
	peer := r.Header.Get("X-CSGLite-Peer")
	switch {
	case msg.UUID == peer:
		// The peer itself is leaving.
		if _, err := a.m.store.Remove(peer); err != nil {
			writeCodedError(w, http.StatusInternalServerError, err.Error(), "store_error")
			return
		}
		a.m.forgetNode(peer)
		a.m.wakeGossip()
		a.m.logf("cluster: node %s left the cluster", shortUUID(peer))
	case msg.UUID == a.m.identity.UUID:
		// A member removed us.
		a.m.logf("cluster: removed from the cluster by %s", shortUUID(peer))
		_ = a.m.leaveLocally()
	default:
		writeCodedError(w, http.StatusForbidden, "a node may only announce its own departure", "bad_request")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /cluster/v1/pull -- create a pull job on this node
func (a *peerAPI) handlePeerPull(w http.ResponseWriter, r *http.Request) {
	a.m.opts.Host.PullHandler().ServeHTTP(w, r)
}

// /cluster/v1/inference/... -- execute a forwarded request locally
func (a *peerAPI) handlePeerInference(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.Header.Get(RoutedHeader)) == "" {
		writeCodedError(w, http.StatusBadRequest, "forwarded requests must carry "+RoutedHeader, "bad_request")
		return
	}
	settings := a.m.store.Settings()
	if !settings.AcceptWork || settings.State != NodeStateActive {
		writeCodedError(w, http.StatusServiceUnavailable, "node is not accepting work ("+string(settings.State)+")", "not_accepting")
		return
	}
	if !a.m.withinNodeLimit(len(a.m.store.Members()) + 1) {
		writeJSON(w, http.StatusForbidden, errorResponse{
			Error: "this node's license does not cover a cluster of this size", ErrorCode: http.StatusForbidden,
			Code: "feature_not_licensed", Limit: a.m.NodeLimit(), Current: len(a.m.store.Members()) + 1,
		})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, strings.TrimSuffix(peerPathInference, "/"))
	if rest == "" {
		rest = "/"
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = rest
	r2.URL.RawPath = ""
	r2.RequestURI = ""
	w.Header().Set(NodeHeader, a.m.identity.UUID)
	w.Header().Set(NodeNameHeader, a.m.identity.DisplayName())
	a.m.opts.Host.InferenceHandler().ServeHTTP(w, r2)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func hmacEqual(a, b string) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// GET /cluster/v1/model-bundle?model=<id> -- manifest and file list of a
// complete local model, for a member that wants to copy it.
func (a *peerAPI) handlePeerModelBundle(w http.ResponseWriter, r *http.Request) {
	modelID := strings.TrimSpace(r.URL.Query().Get("model"))
	if modelID == "" {
		writeCodedError(w, http.StatusBadRequest, "model is required", "bad_request")
		return
	}
	bundle, err := a.m.opts.Host.ModelBundle(modelID)
	if err != nil {
		writeCodedError(w, http.StatusNotFound, err.Error(), "model_not_available")
		return
	}
	writeJSON(w, http.StatusOK, bundle)
}

// GET /cluster/v1/model-file?model=<id>&path=<rel> -- one file of a local
// model, streamed with its SHA-256 in a trailer so the receiver can verify
// it without a second pass here.
func (a *peerAPI) handlePeerModelFile(w http.ResponseWriter, r *http.Request) {
	modelID := strings.TrimSpace(r.URL.Query().Get("model"))
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	if modelID == "" || rel == "" {
		writeCodedError(w, http.StatusBadRequest, "model and path are required", "bad_request")
		return
	}
	bundle, err := a.m.opts.Host.ModelBundle(modelID)
	if err != nil {
		writeCodedError(w, http.StatusNotFound, err.Error(), "model_not_available")
		return
	}
	var size int64 = -1
	for _, f := range append(bundle.Files, bundle.Extras...) {
		if f.Path == rel {
			size = f.Size
		}
	}
	if size < 0 || !safeRelPath(rel) {
		writeCodedError(w, http.StatusNotFound, "no such file in the model", "not_found")
		return
	}
	file, err := os.Open(filepath.Join(bundle.Dir, filepath.FromSlash(rel)))
	if err != nil {
		writeCodedError(w, http.StatusNotFound, "file is not readable", "not_found")
		return
	}
	defer file.Close()
	// No Content-Length on purpose: HTTP/1.1 can only carry the digest
	// trailer with chunked encoding. The receiver knows the size from the
	// bundle and checks it.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-CSGLite-Size", strconv.FormatInt(size, 10))
	w.Header().Set("Trailer", SHA256Trailer)
	w.WriteHeader(http.StatusOK)
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, hasher), io.LimitReader(file, size)); err != nil {
		return
	}
	w.Header().Set(SHA256Trailer, hex.EncodeToString(hasher.Sum(nil)))
}

// safeRelPath accepts only clean, relative, forward-slash paths inside the
// model directory.
func safeRelPath(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") {
		return false
	}
	clean := path.Clean(rel)
	if clean != rel || clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return false
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." || seg == "" {
			return false
		}
	}
	return true
}
