// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	publictunnel "github.com/opencsgs/csglite/ee/cluster/public"
)

func (m *Manager) preparePublic() error {
	if m.opts.Mode == "lan" {
		m.opts.Mode = ""
	}
	if m.opts.Mode != "" && m.opts.Mode != "gateway" && m.opts.Mode != "worker" {
		return errors.New("cluster mode must be lan, gateway or worker")
	}
	if c := m.store.Cluster(); c != nil && (c.Topology == "public") != (m.opts.Mode != "") {
		return errors.New("cluster topology differs from configured mode; leave the existing cluster before changing mode")
	}
	if m.opts.Mode == "" {
		return nil
	}
	if c := m.store.Cluster(); c != nil && (c.GatewayUUID == m.identity.UUID) != (m.opts.Mode == "gateway") {
		return errors.New("public gateway identity does not match configured role; leave before switching roles")
	}
	if m.opts.AutoFormSecret != "" {
		return errors.New("public mode uses cluster join tokens; unset the LAN cluster secret")
	}
	if m.opts.Mode == "gateway" {
		if m.opts.PublicInference == nil {
			return errors.New("public gateway inference handler is required")
		}
		if m.opts.PublicListenAddr == "" {
			m.opts.PublicListenAddr = "127.0.0.1:11439"
		}
		host, _, err := net.SplitHostPort(m.opts.PublicListenAddr)
		if err != nil {
			return fmt.Errorf("public listener: %w", err)
		}
		if m.opts.TLSCertFile == "" && m.opts.TLSKeyFile == "" {
			ip := net.ParseIP(host)
			if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
				return errors.New("a non-loopback public listener requires TLS cert and key files")
			}
		} else if _, err := tls.LoadX509KeyPair(m.opts.TLSCertFile, m.opts.TLSKeyFile); err != nil {
			return fmt.Errorf("public TLS certificate: %w", err)
		}
	}
	if m.opts.Mode == "worker" {
		u, err := url.Parse(m.opts.GatewayURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return errors.New("worker gateway_url must be an HTTPS origin")
		}
		m.opts.GatewayURL = strings.TrimRight(m.opts.GatewayURL, "/")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if m.opts.TLSCAFile != "" {
		pem, err := os.ReadFile(m.opts.TLSCAFile)
		if err != nil {
			return fmt.Errorf("read public gateway CA: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return errors.New("public gateway CA file contains no certificates")
		}
		tlsConfig.RootCAs = pool
	}
	m.publicClient = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: tlsConfig}, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return nil
}

func (m *Manager) publicHub() *publictunnel.Gateway {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.publicGateway
}

func (m *Manager) activatePublic() error {
	m.mu.Lock()
	if m.active {
		m.mu.Unlock()
		return nil
	}
	if m.ctx == nil {
		m.ctx, m.cancel = context.WithCancel(context.Background())
		m.started = true
	}
	runCtx, runCancel := context.WithCancel(m.ctx)
	var ln net.Listener
	var srv *http.Server
	if m.opts.Mode == "gateway" {
		g, err := publictunnel.NewGateway(publictunnel.GatewayOptions{Authorize: m.authorizePublic, ValidateStatus: m.validatePublicStatus})
		if err != nil {
			runCancel()
			m.mu.Unlock()
			return err
		}
		ln, err = net.Listen("tcp", m.opts.PublicListenAddr)
		if err != nil {
			_ = g.Close()
			runCancel()
			m.mu.Unlock()
			return fmt.Errorf("public listener: %w", err)
		}
		srv = &http.Server{Handler: m.publicRoutes(g), ReadHeaderTimeout: 15 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10}
		m.publicGateway, m.publicHTTP = g, srv
	}
	m.runCtx, m.runCancel, m.active = runCtx, runCancel, true
	m.mu.Unlock()
	if m.opts.Mode == "gateway" {
		go func() {
			var err error
			if m.opts.TLSCertFile != "" {
				err = srv.ServeTLS(ln, m.opts.TLSCertFile, m.opts.TLSKeyFile)
			} else {
				err = srv.Serve(ln)
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				m.logf("cluster: public listener failed: %v", err)
				runCancel()
			}
		}()
		go m.pollLoop()
		m.logf("cluster: public gateway listening on %s", ln.Addr())
	} else {
		go m.publicWorkerLoop(runCtx)
	}
	if token := strings.TrimSpace(m.opts.JoinToken); token != "" && !m.store.InCluster() {
		go m.autoJoin(token)
	}
	return nil
}

func (m *Manager) publicRoutes(g *publictunnel.Gateway) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(publictunnel.ConnectPath, g)
	mux.HandleFunc("POST "+peerPathJoin, func(w http.ResponseWriter, r *http.Request) {
		if c := m.store.Cluster(); c == nil || c.Topology != "public" {
			writeError(w, 409, "create a public cluster on this gateway first")
			return
		}
		if r.Header.Get("Origin") != "" {
			writeError(w, 403, "browser enrollment is not supported")
			return
		}
		// Reuse the same token/HMAC, identity pinning, store and quota checks.
		r = r.Clone(r.Context())
		r.RemoteAddr = ""
		m.Peers().handlePeerJoin(w, r)
	})
	mux.HandleFunc("POST /cluster/v1/public-leave", func(w http.ResponseWriter, r *http.Request) {
		if err := m.authorizePublic(r.Context(), r.Header.Get(publictunnel.HeaderNode), r.Header.Get(publictunnel.HeaderToken)); err != nil {
			writeError(w, 401, "node authentication refused")
			return
		}
		if err := m.RemoveMember(r.Context(), r.Header.Get(publictunnel.HeaderNode)); err != nil {
			writeOpError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"left": true})
	})
	mux.Handle("/", m.opts.PublicInference)
	return mux
}

// Proofs use the node's existing pinned identity, not a second credential DB.
// A fresh signed nonce binds each connection to the cluster and gateway.
type publicProof struct {
	Cluster   string `json:"cluster"`
	Gateway   string `json:"gateway"`
	Node      string `json:"node"`
	TS        int64  `json:"ts"`
	Nonce     string `json:"nonce"`
	Signature []byte `json:"signature,omitempty"`
}

func (m *Manager) publicProof(gateway string) (string, error) {
	c := m.store.Cluster()
	if c == nil {
		return "", ErrNotClustered
	}
	nonce, err := randomNonce()
	if err != nil {
		return "", err
	}
	p := publicProof{Cluster: c.UUID, Gateway: gateway, Node: m.identity.UUID, TS: time.Now().Unix(), Nonce: nonce}
	raw, _ := json.Marshal(p)
	digest := sha256.Sum256(raw)
	key, ok := m.identity.Certificate().PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return "", errors.New("unsupported cluster identity key")
	}
	p.Signature, err = ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	raw, err = json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(raw), err
}

func (m *Manager) authorizePublic(ctx context.Context, node, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c := m.store.Cluster()
	mem, ok := m.store.Member(node)
	if c == nil || !ok {
		return errors.New("not an admitted member")
	}
	if !m.withinNodeLimit(len(m.store.Members()) + 1) {
		return publictunnel.ErrUnavailable
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return err
	}
	var p publicProof
	if json.Unmarshal(raw, &p) != nil || p.Node != node || p.Cluster != c.UUID || p.Gateway != m.identity.UUID || len(p.Nonce) != 32 || abs64(time.Now().Unix()-p.TS) > 60 {
		return errors.New("invalid node proof")
	}
	cert, err := ParseCertPEM(mem.CertPEM, node)
	if err != nil {
		return err
	}
	key, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return errors.New("unsupported member key")
	}
	signature := p.Signature
	p.Signature = nil
	raw, _ = json.Marshal(p)
	digest := sha256.Sum256(raw)
	if !ecdsa.VerifyASN1(key, digest[:], signature) {
		return errors.New("invalid node signature")
	}
	m.publicProofMu.Lock()
	defer m.publicProofMu.Unlock()
	now := time.Now()
	for nonce, expiry := range m.publicProofs {
		if !expiry.After(now) {
			delete(m.publicProofs, nonce)
		}
	}
	proofKey := node + ":" + p.Nonce
	if _, used := m.publicProofs[proofKey]; used || len(m.publicProofs) >= 4096 {
		return errors.New("replayed or excessive node proofs")
	}
	m.publicProofs[proofKey] = now.Add(2 * time.Minute)
	return nil
}

func (m *Manager) validatePublicStatus(node string, raw json.RawMessage) error {
	var st Status
	c := m.store.Cluster()
	if json.Unmarshal(raw, &st) != nil || c == nil || st.UUID != node || st.ClusterUUID != c.UUID || st.Protocol != ProtocolVersion {
		return errors.New("invalid node status")
	}
	if _, ok := m.store.Member(node); !ok {
		return errors.New("member was removed")
	}
	if !m.withinNodeLimit(len(m.store.Members())+1) || (st.NodeLimit > 0 && len(m.store.Members())+1 > st.NodeLimit) {
		return errors.New("cluster exceeds node license quota")
	}
	return nil
}

func (m *Manager) joinPublic(ctx context.Context, token, address string) (ClusterInfo, error) {
	if m.store.InCluster() {
		return ClusterInfo{}, ErrAlreadyClustered
	}
	if address != "" && strings.TrimRight(address, "/") != m.opts.GatewayURL {
		return ClusterInfo{}, errors.New("join address must match the configured gateway_url")
	}
	id, secret, err := ParseJoinToken(token)
	if err != nil {
		return ClusterInfo{}, err
	}
	if err = m.Activate(); err != nil {
		return ClusterInfo{}, err
	}
	nonce, err := randomNonce()
	if err != nil {
		return ClusterInfo{}, err
	}
	card := m.card()
	card.Addresses = nil
	card.ClusterPort = 0
	req := joinRequest{Node: card, Nonce: nonce, TS: time.Now().Unix()}
	req.MAC = HandshakeMAC(hashSecret(secret), m.identity.UUID, m.identity.Fingerprint(), nonce, req.TS)
	data, _ := json.Marshal(req)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, m.opts.GatewayURL+peerPathJoin+"?cluster="+url.QueryEscape(id), bytes.NewReader(data))
	if err != nil {
		return ClusterInfo{}, err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := m.publicClient.Do(r)
	if err != nil {
		return ClusterInfo{}, fmt.Errorf("contact public gateway: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return ClusterInfo{}, err
	}
	if resp.StatusCode != 200 {
		return ClusterInfo{}, peerErrorFrom(resp.StatusCode, raw)
	}
	var reply joinResponse
	if json.Unmarshal(raw, &reply) != nil || reply.Cluster.UUID != id || reply.Cluster.Topology != "public" || reply.Cluster.GatewayUUID != reply.Responder.UUID {
		return ClusterInfo{}, errors.New("gateway returned an invalid cluster")
	}
	var members []Member
	for _, card := range reply.Members {
		if card.UUID == m.identity.UUID {
			continue
		}
		mem, err := m.memberFromCard(card)
		if err != nil {
			return ClusterInfo{}, err
		}
		if mem.UUID == reply.Responder.UUID {
			mem.LastAddresses = []string{m.opts.GatewayURL}
		}
		members = append(members, mem)
	}
	if err = m.store.Adopt(reply.Cluster, reply.JoinTokenHash, members, m.identity.UUID); err != nil {
		return ClusterInfo{}, err
	}
	select {
	case m.publicWake <- struct{}{}:
	default:
	}
	return reply.Cluster, nil
}

func (m *Manager) publicWorkerLoop(ctx context.Context) {
	delay := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		cluster := m.store.Cluster()
		if cluster == nil {
			select {
			case <-ctx.Done():
				return
			case <-m.publicWake:
			}
			continue
		}
		gateway := cluster.GatewayUUID
		if gateway == "" {
			m.logf("cluster: gateway identity is missing; leave and join again")
			return
		}
		proof, err := m.publicProof(gateway)
		if err != nil {
			m.logf("cluster: cannot sign public connection: %v", err)
			return
		}
		transport := m.publicClient.Transport.(*http.Transport)
		worker, err := publictunnel.NewWorker(publictunnel.WorkerOptions{GatewayURL: m.opts.GatewayURL, NodeUUID: m.identity.UUID, Token: proof, ConnectionChanged: m.observePublicGateway(gateway), Status: m.publicWorkerStatus, AllowModel: m.publicModelAllowed, Handler: m.opts.Host.InferenceHandler(), Dialer: &websocket.Dialer{Proxy: http.ProxyFromEnvironment, TLSClientConfig: transport.TLSClientConfig.Clone(), HandshakeTimeout: 15 * time.Second}})
		if err != nil {
			m.logf("cluster: invalid public worker: %v", err)
			return
		}
		started := time.Now()
		err = worker.Connect(ctx)
		if ctx.Err() != nil {
			return
		}
		var refused *publictunnel.HandshakeError
		if errors.As(err, &refused) && (refused.Status == 401 || refused.Status == 403 || refused.Status == 409) {
			m.logf("cluster: gateway refused node; check membership and license before joining again")
			select {
			case <-ctx.Done():
				return
			case <-m.publicWake:
			}
			continue
		}
		if time.Since(started) > 30*time.Second {
			delay = time.Second
		}
		m.logf("cluster: public connection ended; retrying in %s", delay)
		var jitter [1]byte
		_, _ = rand.Read(jitter[:])
		wait := delay + time.Duration(jitter[0])*delay/512
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-m.publicWake:
			timer.Stop()
		case <-timer.C:
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

func (m *Manager) publicModelShared(model string) bool {
	for _, id := range m.store.Settings().SharedModels {
		if id == model {
			return true
		}
	}
	return false
}

func (m *Manager) publicModelAllowed(model string) bool {
	s := m.store.Settings()
	return s.AcceptWork && s.State == NodeStateActive && m.publicModelShared(model)
}

func (m *Manager) publicWorkerStatus(ctx context.Context) (json.RawMessage, error) {
	st := m.LocalStatus(ctx)
	models := make([]ModelStatus, 0)
	for _, model := range st.Models {
		if m.publicModelShared(model.ID) {
			models = append(models, model)
		}
	}
	st.Models = models
	st.Net.Addrs = nil
	return json.Marshal(st)
}

func (m *Manager) publicStatus(node string) (*Status, string, error) {
	g := m.publicHub()
	if g == nil {
		return nil, "", publictunnel.ErrOffline
	}
	raw, ok := g.Snapshot(node)
	if !ok {
		return nil, "", publictunnel.ErrOffline
	}
	if err := m.validatePublicStatus(node, raw); err != nil {
		g.Disconnect(node)
		return nil, "", err
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, "", err
	}
	return &st, "tunnel:" + node, nil
}

func (m *Manager) publicForward(ctx context.Context, node, path string, body []byte, headers http.Header) (*http.Response, error) {
	g := m.publicHub()
	if g == nil {
		return nil, &routeError{status: 503, err: publictunnel.ErrOffline}
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil || payload == nil {
		return nil, &routeError{status: 400, noRetry: true, err: errors.New("invalid inference JSON")}
	}
	payload["source"] = json.RawMessage(`"local"`)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://cluster-node"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header = headers.Clone()
	resp, err := g.RoundTrip(node, r)
	if err != nil {
		return nil, &routeError{status: 502, noRetry: !errors.Is(err, publictunnel.ErrOffline), err: errors.New("public tunnel failed; execution may be uncertain")}
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, &routeError{status: resp.StatusCode, body: string(raw), err: errors.New("public node refused request"), retryAfter: resp.Header.Get("Retry-After"), noRetry: resp.StatusCode != 429}
	}
	return resp, nil
}

func (m *Manager) leavePublic(ctx context.Context) error {
	if !m.store.InCluster() {
		return ErrNotClustered
	}
	if m.opts.Mode == "worker" {
		for _, mem := range m.store.Members() {
			for _, addr := range mem.LastAddresses {
				if addr != m.opts.GatewayURL {
					continue
				}
				proof, err := m.publicProof(mem.UUID)
				if err != nil {
					return err
				}
				r, _ := http.NewRequestWithContext(ctx, http.MethodPost, m.opts.GatewayURL+"/cluster/v1/public-leave", nil)
				r.Header.Set(publictunnel.HeaderNode, m.identity.UUID)
				r.Header.Set(publictunnel.HeaderToken, proof)
				resp, err := m.publicClient.Do(r)
				if err == nil {
					resp.Body.Close()
				}
			}
		}
	}
	if err := m.store.Leave(); err != nil {
		return err
	}
	_, err := m.store.UpdateSettings(func(s *Settings) { s.SharedModels = nil })
	m.deactivate()
	return err
}

// A public worker has no peer polling loop: its authenticated outbound tunnel
// is the source of gateway reachability, not LAN discovery or model telemetry.
type publicGatewayConnection struct {
	gateway  string
	online   bool
	lastSeen time.Time
}

func (m *Manager) observePublicGateway(gateway string) func(bool) {
	m.publicConnectionMu.Lock()
	state := &publicGatewayConnection{gateway: gateway}
	if previous := m.publicConnection; previous != nil && previous.gateway == gateway {
		state.lastSeen = previous.lastSeen
	}
	m.publicConnection = state
	m.publicConnectionMu.Unlock()
	return func(online bool) {
		m.publicConnectionMu.Lock()
		defer m.publicConnectionMu.Unlock()
		// An old session must not change the state of a newer connection.
		if m.publicConnection != state {
			return
		}
		if online || state.online {
			state.lastSeen = time.Now()
		}
		state.online = online
	}
}

func (m *Manager) publicGatewayView(node *NodeView) {
	if m.opts.Mode != "worker" {
		return
	}
	cluster := m.store.Cluster()
	if cluster == nil || cluster.Topology != "public" || node.UUID != cluster.GatewayUUID {
		return
	}
	node.Online, node.Health = false, HealthDown
	node.Addr = m.opts.GatewayURL
	// The tunnel establishes connectivity only. It does not provide the
	// gateway's GPU/model telemetry, so do not invent a Status payload.
	m.publicConnectionMu.Lock()
	defer m.publicConnectionMu.Unlock()
	if state := m.publicConnection; state != nil && state.gateway == node.UUID {
		node.Online = state.online
		if state.online {
			node.Health = HealthHealthy
			node.LastError = ""
		}
		if !state.lastSeen.IsZero() {
			seen := state.lastSeen
			node.LastSeen = &seen
		}
	}
}
