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
)

// handshakeSkew bounds how old a join or invite request may be, to blunt
// replay of a captured handshake.
const handshakeSkew = 5 * time.Minute

func writePeerJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writePeerError(w http.ResponseWriter, status int, msg, code string) {
	writePeerJSON(w, status, errorResponse{Error: msg, ErrorCode: status, Code: code})
}

func decodePeerJSON(r *http.Request, out any, limit int64) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// peerMux routes the node-to-node listener.
func (m *Manager) peerMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+peerPathJoin, m.handlePeerJoin)
	mux.HandleFunc("POST "+peerPathInvite, m.handlePeerInvite)
	mux.HandleFunc("GET "+peerPathStatus, m.handlePeerStatus)
	mux.HandleFunc("POST "+peerPathGossip, m.requirePeer(m.handlePeerGossip))
	mux.HandleFunc("POST "+peerPathLeave, m.requirePeer(m.handlePeerLeave))
	mux.HandleFunc("POST "+peerPathPull, m.requirePeer(m.handlePeerPull))
	mux.Handle(peerPathInference, m.requirePeer(m.handlePeerInference))
	mux.HandleFunc("GET "+peerPathModelBundle, m.requirePeer(m.handlePeerModelBundle))
	mux.HandleFunc("GET "+peerPathModelFile, m.requirePeer(m.handlePeerModelFile))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writePeerError(w, http.StatusNotFound, "not a cluster endpoint", "not_found")
	})
	return mux
}

// peerPinned answers the transport's pin check from the member table.
func (m *Manager) peerPinned(nodeUUID, fingerprint string) bool {
	fp, ok := m.store.FingerprintFor(nodeUUID)
	return ok && fp == fingerprint
}

// requirePeer admits only pinned members and records the address they came
// from; the client certificate is the authentication.
func (m *Manager) requirePeer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peer := peerFromRequest(r, m.peerPinned)
		if peer == "" {
			writePeerError(w, http.StatusForbidden, "this node is not a paired member of the cluster", "not_a_member")
			return
		}
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			m.dir.LearnAddress(peer, net.JoinHostPort(host, portString(m.memberClusterPort(peer))), time.Now())
			m.dir.RequestSucceeded(peer)
		}
		r.Header.Set("X-CSGLite-Peer", peer)
		next(w, r)
	}
}

func (m *Manager) memberClusterPort(nodeUUID string) int {
	if mem, ok := m.store.Member(nodeUUID); ok && mem.ClusterPort > 0 {
		return mem.ClusterPort
	}
	return portOf(DefaultListenAddr)
}

func portString(p int) string {
	if p <= 0 {
		p = portOf(DefaultListenAddr)
	}
	return itoaInt(p)
}

func itoaInt(i int) string { return strconv.Itoa(i) }

// GET /cluster/v1/status
// Members get the full status. Anyone may ask with ?public=1 and receives the
// identity fields only, which is what seed probing and the discovered-node
// list need; hardware and model inventory are cluster data.
func (m *Manager) handlePeerStatus(w http.ResponseWriter, r *http.Request) {
	peer := peerFromRequest(r, m.peerPinned)
	if peer == "" {
		if r.URL.Query().Get("public") != "1" {
			writePeerError(w, http.StatusForbidden, "this node is not a paired member of the cluster", "not_a_member")
			return
		}
		st := m.LocalStatus(r.Context())
		writePeerJSON(w, http.StatusOK, Status{
			UUID: st.UUID, Name: st.Name, Version: st.Version, Protocol: st.Protocol, ClusterUUID: st.ClusterUUID,
			Licensed: st.Licensed, NodeLimit: st.NodeLimit, APIPort: st.APIPort, ClusterPort: st.ClusterPort,
			Hostname: st.Hostname, OS: st.OS, Arch: st.Arch, Time: st.Time, Models: []ModelStatus{}, GPUs: []GPUStatus{},
		})
		return
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		m.dir.LearnAddress(peer, net.JoinHostPort(host, portString(m.memberClusterPort(peer))), time.Now())
	}
	writePeerJSON(w, http.StatusOK, m.LocalStatus(r.Context()))
}

// POST /cluster/v1/join?cluster=<uuid>
func (m *Manager) handlePeerJoin(w http.ResponseWriter, r *http.Request) {
	c := m.store.Cluster()
	if c == nil {
		writePeerError(w, http.StatusConflict, "this node is not in a cluster", "not_clustered")
		return
	}
	if r.URL.Query().Get("cluster") != c.UUID {
		writePeerError(w, http.StatusForbidden, "join token names a different cluster", "wrong_cluster")
		return
	}
	var req joinRequest
	if err := decodePeerJSON(r, &req, 1<<20); err != nil {
		writePeerError(w, http.StatusBadRequest, "invalid join request", "bad_request")
		return
	}
	if req.Node.Protocol != ProtocolVersion {
		writePeerError(w, http.StatusBadRequest, "incompatible cluster protocol version", "protocol_mismatch")
		return
	}
	if abs64(time.Now().Unix()-req.TS) > int64(handshakeSkew.Seconds()) {
		writePeerError(w, http.StatusForbidden, "join request is too old; check the clocks", "stale_handshake")
		return
	}
	mem, err := m.memberFromCard(req.Node)
	if err != nil || req.Node.CertPEM == "" {
		writePeerError(w, http.StatusBadRequest, "join request carries no valid certificate", "bad_certificate")
		return
	}
	if mem.UUID == m.identity.UUID {
		writePeerError(w, http.StatusBadRequest, "a node cannot join itself", "bad_request")
		return
	}
	// The MAC key is the token hash both sides can derive; the secret itself
	// never crosses the network.
	want := HandshakeMAC(m.store.JoinTokenHash(), mem.UUID, mem.CertFingerprint, req.Nonce, req.TS)
	if m.store.JoinTokenHash() == "" || !hmacEqual(want, req.MAC) {
		writePeerError(w, http.StatusUnauthorized, "join token is not valid for this cluster", "bad_token")
		return
	}
	if _, already := m.store.Member(mem.UUID); !already {
		if !m.withinNodeLimit(len(m.store.Members()) + 2) {
			writePeerJSON(w, http.StatusForbidden, errorResponse{
				Error:     "the cluster has reached its licensed node limit",
				ErrorCode: http.StatusForbidden, Code: "feature_not_licensed",
				Limit: m.NodeLimit(), Current: len(m.store.Members()) + 1,
			})
			return
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		port := mem.ClusterPort
		if port == 0 {
			port = portOf(DefaultListenAddr)
		}
		mem.LastAddresses = append([]string{net.JoinHostPort(host, itoaInt(port))}, mem.LastAddresses...)
	}
	if _, err := m.store.Upsert(mem, true); err != nil {
		writePeerError(w, http.StatusInternalServerError, err.Error(), "store_error")
		return
	}
	m.dir.Track(mem.UUID, m.seedAddressesFor(mem))
	for _, addr := range mem.LastAddresses {
		m.dir.LearnAddress(mem.UUID, addr, time.Now())
	}
	m.dir.DropDiscovered(mem.UUID)
	m.wakeGossip()
	m.logf("cluster: node %s (%s) joined via this node", shortUUID(mem.UUID), mem.Name)
	writePeerJSON(w, http.StatusOK, joinResponse{clusterView: m.view(), Responder: m.card()})
}

// POST /cluster/v1/invite
func (m *Manager) handlePeerInvite(w http.ResponseWriter, r *http.Request) {
	if m.store.InCluster() {
		writePeerError(w, http.StatusConflict, "this node already belongs to a cluster", "already_clustered")
		return
	}
	var req inviteRequest
	if err := decodePeerJSON(r, &req, 4<<20); err != nil {
		writePeerError(w, http.StatusBadRequest, "invalid invite", "bad_request")
		return
	}
	if req.Inviter.Protocol != ProtocolVersion {
		writePeerError(w, http.StatusBadRequest, "incompatible cluster protocol version", "protocol_mismatch")
		return
	}
	if abs64(time.Now().Unix()-req.TS) > int64(handshakeSkew.Seconds()) {
		writePeerError(w, http.StatusForbidden, "invite is too old; check the clocks", "stale_handshake")
		return
	}
	inviter, err := m.memberFromCard(req.Inviter)
	if err != nil || req.Inviter.CertPEM == "" {
		writePeerError(w, http.StatusBadRequest, "invite carries no valid certificate", "bad_certificate")
		return
	}
	// The inviter must be the TLS peer, so the code cannot be relayed.
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || CertFingerprint(r.TLS.PeerCertificates[0]) != inviter.CertFingerprint {
		writePeerError(w, http.StatusForbidden, "invite must come from the inviting node itself", "bad_certificate")
		return
	}
	code, expires := m.peekNodeCode()
	if code == "" || time.Now().After(expires) {
		writePeerError(w, http.StatusForbidden, "no admission code is active on this node", "bad_code")
		return
	}
	want := HandshakeMAC(code, inviter.UUID, inviter.CertFingerprint, req.Nonce, req.TS)
	if !hmacEqual(want, req.MAC) {
		writePeerError(w, http.StatusUnauthorized, "admission code is wrong", "bad_code")
		return
	}
	if req.Cluster.UUID == "" {
		writePeerError(w, http.StatusBadRequest, "invite names no cluster", "bad_request")
		return
	}
	if !m.withinNodeLimit(len(req.Members) + 1) {
		writePeerJSON(w, http.StatusForbidden, errorResponse{
			Error:     "this node's license does not allow a cluster of that size",
			ErrorCode: http.StatusForbidden, Code: "feature_not_licensed",
			Limit: m.NodeLimit(), Current: len(req.Members),
		})
		return
	}
	if !m.store.VerifyNodeCode(code) {
		writePeerError(w, http.StatusUnauthorized, "admission code is no longer valid", "bad_code")
		return
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		port := inviter.ClusterPort
		if port == 0 {
			port = portOf(DefaultListenAddr)
		}
		inviter.LastAddresses = append([]string{net.JoinHostPort(host, itoaInt(port))}, inviter.LastAddresses...)
	}
	members := []Member{inviter}
	for _, card := range req.Members {
		if card.UUID == m.identity.UUID || card.UUID == inviter.UUID {
			continue
		}
		mem, err := m.memberFromCard(card)
		if err != nil {
			continue
		}
		members = append(members, mem)
	}
	if err := m.store.Adopt(req.Cluster, req.JoinTokenHash, members, m.identity.UUID); err != nil {
		writePeerError(w, http.StatusInternalServerError, err.Error(), "store_error")
		return
	}
	m.dir.Reset()
	for _, mem := range members {
		m.dir.Track(mem.UUID, m.seedAddressesFor(mem))
		m.dir.DropDiscovered(mem.UUID)
	}
	m.reannounce()
	m.wakeGossip()
	m.logf("cluster: joined %q (%s) by invitation from %s", req.Cluster.Name, shortUUID(req.Cluster.UUID), inviter.Name)
	writePeerJSON(w, http.StatusOK, inviteResponse{Node: m.card()})
}

func (m *Manager) peekNodeCode() (string, time.Time) {
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()
	if m.store.data.NodeCode == nil {
		return "", time.Time{}
	}
	return m.store.data.NodeCode.Code, m.store.data.NodeCode.ExpiresAt
}

// POST /cluster/v1/gossip
func (m *Manager) handlePeerGossip(w http.ResponseWriter, r *http.Request) {
	c := m.store.Cluster()
	if c == nil {
		writePeerError(w, http.StatusConflict, "this node is not in a cluster", "not_clustered")
		return
	}
	var msg gossipMessage
	if err := decodePeerJSON(r, &msg, 4<<20); err != nil {
		writePeerError(w, http.StatusBadRequest, "invalid gossip", "bad_request")
		return
	}
	if msg.ClusterUUID != c.UUID {
		writePeerError(w, http.StatusForbidden, "gossip for another cluster", "wrong_cluster")
		return
	}
	peer := r.Header.Get("X-CSGLite-Peer")
	if msg.Sender.UUID != peer {
		writePeerError(w, http.StatusForbidden, "gossip sender does not match the connection", "bad_sender")
		return
	}
	from := ""
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		from = net.JoinHostPort(host, portString(m.memberClusterPort(peer)))
	}
	m.mergeGossip(msg, from)
	reply := gossipMessage{ClusterUUID: c.UUID, Sender: m.card(), Tombstones: m.store.Tombstones()}
	for _, mem := range m.store.Members() {
		reply.Members = append(reply.Members, m.cardFromMember(mem))
	}
	writePeerJSON(w, http.StatusOK, reply)
}

// POST /cluster/v1/leave
func (m *Manager) handlePeerLeave(w http.ResponseWriter, r *http.Request) {
	var msg leaveMessage
	if err := decodePeerJSON(r, &msg, 64<<10); err != nil {
		writePeerError(w, http.StatusBadRequest, "invalid leave message", "bad_request")
		return
	}
	peer := r.Header.Get("X-CSGLite-Peer")
	switch {
	case msg.UUID == peer:
		// The peer itself is leaving.
		if _, err := m.store.Remove(peer); err != nil {
			writePeerError(w, http.StatusInternalServerError, err.Error(), "store_error")
			return
		}
		m.forgetNode(peer)
		m.wakeGossip()
		m.logf("cluster: node %s left the cluster", shortUUID(peer))
	case msg.UUID == m.identity.UUID:
		// A member removed us.
		m.logf("cluster: removed from the cluster by %s", shortUUID(peer))
		_ = m.leaveLocally()
	default:
		writePeerError(w, http.StatusForbidden, "a node may only announce its own departure", "bad_request")
		return
	}
	writePeerJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /cluster/v1/pull -- create a pull job on this node
func (m *Manager) handlePeerPull(w http.ResponseWriter, r *http.Request) {
	m.opts.Host.PullHandler().ServeHTTP(w, r)
}

// /cluster/v1/inference/... -- execute a forwarded request locally
func (m *Manager) handlePeerInference(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.Header.Get(RoutedHeader)) == "" {
		writePeerError(w, http.StatusBadRequest, "forwarded requests must carry "+RoutedHeader, "bad_request")
		return
	}
	settings := m.store.Settings()
	if !settings.AcceptWork || settings.State != NodeStateActive {
		writePeerError(w, http.StatusServiceUnavailable, "node is not accepting work ("+string(settings.State)+")", "not_accepting")
		return
	}
	if !m.withinNodeLimit(len(m.store.Members()) + 1) {
		writePeerJSON(w, http.StatusForbidden, errorResponse{
			Error: "this node's license does not cover a cluster of this size", ErrorCode: http.StatusForbidden,
			Code: "feature_not_licensed", Limit: m.NodeLimit(), Current: len(m.store.Members()) + 1,
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
	w.Header().Set(NodeHeader, m.identity.UUID)
	w.Header().Set(NodeNameHeader, m.identity.DisplayName())
	m.opts.Host.InferenceHandler().ServeHTTP(w, r2)
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
func (m *Manager) handlePeerModelBundle(w http.ResponseWriter, r *http.Request) {
	modelID := strings.TrimSpace(r.URL.Query().Get("model"))
	if modelID == "" {
		writePeerError(w, http.StatusBadRequest, "model is required", "bad_request")
		return
	}
	bundle, err := m.opts.Host.ModelBundle(modelID)
	if err != nil {
		writePeerError(w, http.StatusNotFound, err.Error(), "model_not_available")
		return
	}
	writePeerJSON(w, http.StatusOK, bundle)
}

// GET /cluster/v1/model-file?model=<id>&path=<rel> -- one file of a local
// model, streamed with its SHA-256 in a trailer so the receiver can verify
// it without a second pass here.
func (m *Manager) handlePeerModelFile(w http.ResponseWriter, r *http.Request) {
	modelID := strings.TrimSpace(r.URL.Query().Get("model"))
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	if modelID == "" || rel == "" {
		writePeerError(w, http.StatusBadRequest, "model and path are required", "bad_request")
		return
	}
	bundle, err := m.opts.Host.ModelBundle(modelID)
	if err != nil {
		writePeerError(w, http.StatusNotFound, err.Error(), "model_not_available")
		return
	}
	var size int64 = -1
	for _, f := range append(bundle.Files, bundle.Extras...) {
		if f.Path == rel {
			size = f.Size
		}
	}
	if size < 0 || !safeRelPath(rel) {
		writePeerError(w, http.StatusNotFound, "no such file in the model", "not_found")
		return
	}
	file, err := os.Open(filepath.Join(bundle.Dir, filepath.FromSlash(rel)))
	if err != nil {
		writePeerError(w, http.StatusNotFound, "file is not readable", "not_found")
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
