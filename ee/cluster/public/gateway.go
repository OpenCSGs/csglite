// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package public

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/opencsgs/csglite/ee/tunnel"
)

type GatewayOptions struct {
	// Authorize must check approved membership, token, revocation and licensing.
	// It is invoked for every connection, including requests from loopback.
	Authorize func(context.Context, string, string) error
	// ValidateStatus validates without side effects. Status publication is fenced
	// by session identity so a replaced connection cannot overwrite a new one.
	ValidateStatus   func(string, json.RawMessage) error
	Heartbeat        time.Duration
	HandshakeTimeout time.Duration
	// MaxSessions is a resource ceiling, independent of the host's license cap.
	MaxSessions int
}

type nodeSession struct {
	mux       *yamux.Session
	transport *http.Transport
	status    json.RawMessage
	lastSeen  time.Time
	draining  bool
}

type Gateway struct {
	opts       GatewayOptions
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.RWMutex
	sessions   map[string]*nodeSession
	closed     bool
	handshakes chan struct{}
}

func NewGateway(opts GatewayOptions) (*Gateway, error) {
	if opts.Authorize == nil || opts.ValidateStatus == nil {
		return nil, errors.New("public gateway requires authorization and status validation")
	}
	if opts.Heartbeat == 0 {
		opts.Heartbeat = 5 * time.Second
	}
	if opts.HandshakeTimeout == 0 {
		opts.HandshakeTimeout = 15 * time.Second
	}
	if opts.MaxSessions == 0 {
		opts.MaxSessions = 128
	}
	if opts.Heartbeat < time.Millisecond || opts.HandshakeTimeout <= 0 || opts.MaxSessions < 1 {
		return nil, errors.New("invalid public gateway limits")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Gateway{opts: opts, ctx: ctx, cancel: cancel, sessions: make(map[string]*nodeSession), handshakes: make(chan struct{}, opts.MaxSessions)}, nil
}

// ServeHTTP is only the node connection handler; it exposes no management or
// inference routes and makes no loopback authentication exceptions.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != ConnectPath || r.URL.RawPath != "" {
		http.NotFound(w, r)
		return
	}
	if r.URL.RawQuery != "" {
		http.Error(w, "query parameters are not supported", http.StatusBadRequest)
		return
	}
	if r.Header.Get("Origin") != "" {
		http.Error(w, "browser connections are not supported", http.StatusForbidden)
		return
	}
	if r.Header.Get(HeaderProtocol) != ProtocolVersion {
		http.Error(w, "incompatible cluster protocol", http.StatusConflict)
		return
	}
	node, token := r.Header.Get(HeaderNode), r.Header.Get(HeaderToken)
	if node == "" || len(node) > 128 || token == "" || len(token) > 4096 {
		http.Error(w, "node authentication required", http.StatusUnauthorized)
		return
	}
	if err := g.opts.Authorize(r.Context(), node, token); err != nil {
		if errors.Is(err, ErrUnavailable) {
			http.Error(w, "cluster temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "node authentication refused", http.StatusUnauthorized)
		return
	}
	g.mu.RLock()
	closed := g.closed
	g.mu.RUnlock()
	if closed {
		http.Error(w, "gateway is shutting down", http.StatusServiceUnavailable)
		return
	}
	select {
	case g.handshakes <- struct{}{}:
	default:
		http.Error(w, "too many node handshakes", http.StatusTooManyRequests)
		return
	}
	var releaseOnce sync.Once
	releaseHandshake := func() { releaseOnce.Do(func() { <-g.handshakes }) }
	defer releaseHandshake()
	ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	stopShutdown := context.AfterFunc(g.ctx, func() { _ = ws.Close() })
	defer stopShutdown()
	ws.SetReadLimit(2 << 20)
	cfg := tunnel.Config(g.opts.Heartbeat)
	cfg.StreamOpenTimeout = g.opts.HandshakeTimeout
	mux, err := yamux.Client(tunnel.NewWebSocketStream(ws), cfg)
	if err != nil {
		return
	}
	defer mux.Close()
	ctx, cancel := context.WithTimeout(r.Context(), g.opts.HandshakeTimeout)
	stream, err := acceptControl(ctx, mux)
	cancel()
	if err != nil {
		return
	}
	control := newControl(stream)
	_ = stream.SetReadDeadline(time.Now().Add(g.opts.HandshakeTimeout))
	msg, err := control.read()
	if err != nil || msg.Type != "status" || !json.Valid(msg.Status) || g.opts.ValidateStatus(node, msg.Status) != nil {
		return
	}
	sess := &nodeSession{mux: mux, status: append(json.RawMessage(nil), msg.Status...), lastSeen: time.Now()}
	sess.transport = &http.Transport{
		DialContext:        func(ctx context.Context, _, _ string) (net.Conn, error) { return openStream(ctx, mux) },
		DisableCompression: true,
		// A separate HTTP connection per request makes cancellation close just
		// its logical stream, and avoids implicit connection-reuse retries.
		DisableKeepAlives:     true,
		MaxConnsPerHost:       64,
		ResponseHeaderTimeout: 10 * time.Minute,
	}
	defer sess.transport.CloseIdleConnections()
	if err = control.write(controlMessage{Type: "hello", HeartbeatMS: g.opts.Heartbeat.Milliseconds()}, g.opts.HandshakeTimeout); err != nil {
		return
	}
	g.mu.Lock()
	previous := g.sessions[node]
	if g.closed || (previous == nil && len(g.sessions) >= g.opts.MaxSessions) {
		g.mu.Unlock()
		return
	}
	g.sessions[node] = sess
	g.mu.Unlock()
	releaseHandshake()
	if previous != nil {
		_ = previous.mux.Close()
	}
	defer func() {
		g.mu.Lock()
		if g.sessions[node] == sess {
			delete(g.sessions, node)
		}
		g.mu.Unlock()
	}()
	for {
		_ = stream.SetReadDeadline(time.Now().Add(3 * g.opts.Heartbeat))
		msg, err = control.read()
		if err != nil {
			return
		}
		if msg.Type != "status" && msg.Type != "drain" {
			return
		}
		if msg.Type == "status" && (!json.Valid(msg.Status) || g.opts.ValidateStatus(node, msg.Status) != nil) {
			return
		}
		g.mu.Lock()
		if g.sessions[node] != sess {
			g.mu.Unlock()
			return
		}
		sess.lastSeen = time.Now()
		if msg.Type == "drain" {
			sess.draining = true
		} else {
			sess.status = append(sess.status[:0], msg.Status...)
		}
		g.mu.Unlock()
	}
}

// Snapshot returns a copy; offline and draining nodes are never candidates.
func (g *Gateway) Snapshot(node string) (json.RawMessage, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	sess := g.sessions[node]
	if sess == nil || sess.draining || sess.mux.IsClosed() || time.Since(sess.lastSeen) >= 3*g.opts.Heartbeat {
		return nil, false
	}
	return append(json.RawMessage(nil), sess.status...), true
}

// RoundTrip sends exactly one attempt. The host must first authorize the model
// and reserve its concurrency slot; the worker independently checks sharing.
func (g *Gateway) RoundTrip(node string, r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		defer r.Body.Close()
	}
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	out, err := inferenceRequest(r, nil)
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	g.mu.RLock()
	sess := g.sessions[node]
	ready := sess != nil && !sess.draining && !sess.mux.IsClosed() && time.Since(sess.lastSeen) < 3*g.opts.Heartbeat
	g.mu.RUnlock()
	if !ready {
		return nil, ErrOffline
	}
	return sess.transport.RoundTrip(out)
}

// Disconnect fences the old session immediately. The host must also revoke its
// stored credential when removing a member so it cannot authenticate again.
func (g *Gateway) Disconnect(node string) {
	g.mu.Lock()
	sess := g.sessions[node]
	delete(g.sessions, node)
	g.mu.Unlock()
	if sess != nil {
		_ = sess.mux.Close()
	}
}

func (g *Gateway) Close() error {
	g.mu.Lock()
	g.closed = true
	sessions := g.sessions
	g.sessions = make(map[string]*nodeSession)
	g.mu.Unlock()
	g.cancel()
	for _, sess := range sessions {
		_ = sess.mux.Close()
		sess.transport.CloseIdleConnections()
	}
	return nil
}
