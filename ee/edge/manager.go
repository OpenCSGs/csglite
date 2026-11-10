// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

// Package edge serves this machine's approved local models to the CSGHub AI
// gateway. The node dials an outbound WebSocket to the gateway, so it works
// behind NAT and proxies with no public address; the gateway sends requests
// back over that connection. Only inference paths are served, a remote
// request carries no caller identity, and nothing is served before the
// platform approves the model.
package edge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"github.com/opencsgs/csglite/internal/safefile"
)

// Model run states reported to the gateway. A sleeping model is not in
// memory but still served: the first request loads it.
const (
	ModelLoaded   = "loaded"
	ModelLoading  = "loading"
	ModelSleeping = "sleeping"
)

// Model is a local model as reported to the gateway.
type Model struct {
	// ID is the model's storage ID, the stable key an application binds.
	ID string `json:"id"`
	// Name is the inference ID shown to people; it may change when another
	// model with the same short name is downloaded.
	Name  string `json:"name,omitempty"`
	Ready bool   `json:"ready"`
	// State is ModelLoaded, ModelLoading or ModelSleeping.
	State         string `json:"state,omitempty"`
	Format        string `json:"format,omitempty"`
	ContextLength int64  `json:"context_length,omitempty"`
}

// Host is what the edge connector needs from the CSGLite server.
type Host interface {
	// InferenceHandler serves the requests that arrive through the tunnel.
	InferenceHandler() http.Handler
	// Models lists the local models; Ready ones can be served now.
	Models(ctx context.Context) []Model
	// ActiveRequests counts requests in flight on this machine.
	ActiveRequests() int
	// Cloud returns the gateway base URL and the signed-in user's access
	// token, empty when signed out.
	Cloud() (gatewayURL, token string)
	Version() string
	// Licensed reports whether an Enterprise license enables edge serving;
	// the connector neither connects nor keeps a tunnel open without one.
	Licensed() bool
}

// State values reported by Status.
const (
	StateDisabled   = "disabled"
	StateUnlicensed = "unlicensed"
	StateSignedOut  = "signed_out"
	StateConnecting = "connecting"
	StateConnected  = "connected"
	StateError      = "error"
)

// Status is the connector state shown in the UI.
type Status struct {
	Enabled        bool       `json:"enabled"`
	State          string     `json:"state"`
	NodeUUID       string     `json:"node_uuid,omitempty"`
	NodeName       string     `json:"node_name,omitempty"`
	GatewayURL     string     `json:"gateway_url,omitempty"`
	ConnectedSince *time.Time `json:"connected_since,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	Reconnects     int        `json:"reconnects"`
	// ApplicationsChanged counts review decisions pushed by the gateway, so
	// the UI knows to refresh the application list.
	ApplicationsChanged int `json:"applications_changed"`
}

// persistedState is the connector's own runtime file: the node identity and
// the switch. It lives under the storage root, not in config.json, because
// it is credentials the gateways issued rather than a user setting. The
// gateway itself is not stored here: it is whatever ai_gateway_url the user
// configured, read on every connection.
type persistedState struct {
	Enabled bool `json:"enabled"`
	// NodeUUID is this machine's identity on every gateway, unless a
	// gateway's credential carries its own.
	NodeUUID string `json:"node_uuid"`
	// Gateways holds what each gateway issued, keyed by gateway URL, so
	// switching ai_gateway_url back and forth never re-registers.
	Gateways map[string]*gatewayCredential `json:"gateways,omitempty"`
	// Legacy single-gateway fields, migrated into Gateways on load.
	NodeToken  string `json:"node_token,omitempty"`
	GatewayURL string `json:"gateway_url,omitempty"`
}

// gatewayCredential is this node's registration on one gateway.
type gatewayCredential struct {
	// NodeUUID differs from the machine's identity when that identity was
	// already registered on this gateway by another account.
	NodeUUID  string `json:"node_uuid"`
	NodeToken string `json:"node_token,omitempty"`
	// Account fingerprints the sign-in that registered the node. Signing in
	// as someone else registers again, so the node never keeps serving the
	// previous account's models.
	Account string `json:"account,omitempty"`
}

func gatewayKey(gatewayURL string) string {
	return strings.TrimRight(strings.TrimSpace(gatewayURL), "/")
}

// credential returns this node's registration on a gateway, creating an
// empty one; callers hold m.mu.
func (m *Manager) credential(gatewayURL string) *gatewayCredential {
	if m.state.Gateways == nil {
		m.state.Gateways = map[string]*gatewayCredential{}
	}
	key := gatewayKey(gatewayURL)
	cred := m.state.Gateways[key]
	if cred == nil {
		cred = &gatewayCredential{NodeUUID: m.state.NodeUUID}
		m.state.Gateways[key] = cred
	}
	return cred
}

// nodeUUIDFor is the node identity used on a gateway.
func (m *Manager) nodeUUIDFor(gatewayURL string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.credential(gatewayURL).NodeUUID
}

// Manager runs the edge connector.
type Manager struct {
	host      Host
	stateFile string
	nodeName  string
	client    *http.Client

	mu       sync.Mutex
	state    persistedState
	status   Status
	cancel   context.CancelFunc
	done     chan struct{}
	draining chan struct{}
}

// New creates a manager keeping its state under dir.
func New(host Host, dir, nodeName string) (*Manager, error) {
	m := &Manager{
		host:      host,
		stateFile: filepath.Join(dir, "state.json"),
		nodeName:  nodeName,
		client:    &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}},
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	if m.state.NodeUUID == "" {
		m.state.NodeUUID = newUUID()
		if err := m.save(); err != nil {
			return nil, err
		}
	}
	m.status = Status{Enabled: m.state.Enabled, State: StateDisabled, NodeName: nodeName}
	return m, nil
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read edge state: %w", err)
	}
	if err := json.Unmarshal(data, &m.state); err != nil {
		return fmt.Errorf("decode edge state: %w", err)
	}
	if m.state.NodeToken != "" && m.state.GatewayURL != "" {
		// Older files bound one token to one gateway; keep it, unbound to an
		// account, so the next connection re-registers once and records it.
		cred := m.credential(m.state.GatewayURL)
		if cred.NodeToken == "" {
			cred.NodeToken = m.state.NodeToken
		}
	}
	m.state.NodeToken, m.state.GatewayURL = "", ""
	return nil
}

func (m *Manager) save() error {
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.stateFile), 0o700); err != nil {
		return err
	}
	return safefile.Write(m.stateFile, data, 0o600)
}

// Start resumes serving if it was enabled before.
func (m *Manager) Start() {
	m.mu.Lock()
	enabled := m.state.Enabled
	m.mu.Unlock()
	if enabled {
		m.run()
	}
}

// SetEnabled turns serving on or off and remembers the choice.
func (m *Manager) SetEnabled(enabled bool) error {
	m.mu.Lock()
	m.state.Enabled = enabled
	m.status.Enabled = enabled
	err := m.save()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if enabled {
		m.run()
	} else {
		m.Stop()
	}
	return nil
}

// Status returns the connector state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.status
	// The gateway shown is the one configured now; after a change the
	// connector follows it within one heartbeat.
	gw, _ := m.host.Cloud()
	st.GatewayURL = gw
	st.NodeUUID = m.credential(gw).NodeUUID
	return st
}

func (m *Manager) setStatus(update func(*Status)) {
	m.mu.Lock()
	update(&m.status)
	m.mu.Unlock()
}

func (m *Manager) run() {
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.done = make(chan struct{})
	m.draining = make(chan struct{})
	done := m.done
	m.mu.Unlock()
	go func() {
		defer close(done)
		m.loop(ctx)
	}()
}

// Stop drains and closes the tunnel: the gateway stops routing first, then
// in-flight requests get a short grace period to finish.
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel, done, draining := m.cancel, m.done, m.draining
	m.cancel = nil
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	close(draining)
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		cancel()
		<-done
	}
	cancel()
	m.setStatus(func(s *Status) {
		s.State = StateDisabled
		s.ConnectedSince = nil
	})
}

const (
	minBackoff = time.Second
	maxBackoff = time.Minute
	// stableAfter resets the backoff once a session lasted this long.
	stableAfter = 30 * time.Second
)

func (m *Manager) loop(ctx context.Context) {
	backoff := minBackoff
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.draining:
			return
		default:
		}
		started := time.Now()
		err := m.session(ctx)
		select {
		case <-m.draining:
			return
		default:
		}
		if time.Since(started) > stableAfter {
			backoff = minBackoff
		}
		if errors.Is(err, errGatewayChanged) {
			// Follow the new configuration at once, without backoff.
			backoff = minBackoff
			continue
		}
		state := StateError
		switch {
		case errors.Is(err, errSignedOut):
			state = StateSignedOut
		case errors.Is(err, errNotLicensed):
			state = StateUnlicensed
		}
		m.setStatus(func(s *Status) {
			s.State = state
			s.ConnectedSince = nil
			s.Reconnects++
			if err != nil {
				s.LastError = err.Error()
			}
		})
		if err != nil {
			log.Printf("EDGE: tunnel ended: %v (retry in %s)", err, backoff)
		}
		// Jitter spreads reconnects so a gateway restart is not hit by every
		// node at the same instant.
		wait := backoff + time.Duration(randInt(int64(backoff/2)+1))
		select {
		case <-ctx.Done():
			return
		case <-m.draining:
			return
		case <-time.After(wait):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

var (
	errSignedOut    = errors.New("sign in to OpenCSG to serve models")
	errUnauthorized = errors.New("gateway rejected the node token")
	errNodeDisabled = errors.New("this node was disabled by the platform")
	errNotLicensed  = errors.New("edge serving requires a CSGLite Enterprise license")
	// errGatewayChanged ends a tunnel because the user configured another
	// gateway or signed in as someone else; the connector reconnects at once.
	errGatewayChanged = errors.New("the configured gateway or account changed")
)

// session runs one tunnel until it ends.
func (m *Manager) session(ctx context.Context) error {
	if !m.host.Licensed() {
		return errNotLicensed
	}
	gatewayURL, token := m.host.Cloud()
	if strings.TrimSpace(token) == "" {
		return errSignedOut
	}
	nodeToken, err := m.ensureRegistered(ctx, gatewayURL, token)
	if err != nil {
		return err
	}
	m.setStatus(func(s *Status) {
		s.State = StateConnecting
		s.GatewayURL = gatewayURL
	})

	ws, err := m.dial(ctx, gatewayURL, nodeToken)
	if errors.Is(err, errUnauthorized) {
		// The token was rotated or the node re-registered elsewhere:
		// register again on the next attempt.
		m.mu.Lock()
		m.credential(gatewayURL).NodeToken = ""
		_ = m.save()
		m.mu.Unlock()
	}
	if err != nil {
		return err
	}
	mux, err := yamux.Server(newWSStream(ws), yamuxConfig(5*time.Second))
	if err != nil {
		_ = ws.Close()
		return err
	}
	defer mux.Close()
	stream, err := mux.Open()
	if err != nil {
		return fmt.Errorf("open control stream: %w", err)
	}
	control := newControlConn(stream)
	if err := control.write(m.statusMessage(ctx)); err != nil {
		return fmt.Errorf("send status: %w", err)
	}

	now := time.Now()
	m.setStatus(func(s *Status) {
		s.State = StateConnected
		s.ConnectedSince = &now
		s.LastError = ""
	})
	log.Printf("EDGE: connected to %s as node %s", gatewayURL, m.nodeUUIDFor(gatewayURL))

	heartbeat := make(chan time.Duration, 1)
	var changed atomic.Bool
	go m.readControl(control, heartbeat)
	go m.heartbeat(ctx, mux, control, heartbeat, func() bool {
		gw, tk := m.host.Cloud()
		if gatewayKey(gw) != gatewayKey(gatewayURL) || tk != token {
			changed.Store(true)
			return true
		}
		return false
	})

	server := &http.Server{
		Handler:           guard(m.host.InferenceHandler()),
		ReadHeaderTimeout: 30 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(mux) }()
	select {
	case err = <-serveErr:
	case <-ctx.Done():
		err = ctx.Err()
	case <-m.draining:
		_ = control.write(controlMessage{Type: msgDrain})
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = server.Shutdown(shutdownCtx)
		cancel()
		return nil
	}
	_ = server.Close()
	if changed.Load() {
		log.Printf("EDGE: %v; reconnecting", errGatewayChanged)
		return errGatewayChanged
	}
	if errors.Is(err, yamux.ErrSessionShutdown) || errors.Is(err, http.ErrServerClosed) {
		return errors.New("tunnel closed")
	}
	return err
}

func (m *Manager) dial(ctx context.Context, gatewayURL, nodeToken string) (*websocket.Conn, error) {
	u, err := url.Parse(strings.TrimRight(gatewayURL, "/") + "/v1/edge/connect")
	if err != nil {
		return nil, fmt.Errorf("gateway URL: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	header := http.Header{}
	header.Set(headerNodeUUID, m.nodeUUIDFor(gatewayURL))
	header.Set(headerNodeToken, nodeToken)
	header.Set(headerTunnelProtocol, tunnelProtocolVersion)
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 15 * time.Second,
		ReadBufferSize:   32 * 1024,
		WriteBufferSize:  32 * 1024,
	}
	ws, resp, err := dialer.DialContext(ctx, u.String(), header)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, errUnauthorized
		}
		// A disabled node keeps its token: registering again would not lift
		// the platform's decision, only rotate the credential.
		if resp != nil && resp.StatusCode == http.StatusForbidden {
			return nil, errNodeDisabled
		}
		return nil, fmt.Errorf("connect %s: %w", u.Host, err)
	}
	return ws, nil
}

func (m *Manager) readControl(control *controlConn, heartbeat chan<- time.Duration) {
	for {
		msg, err := control.read()
		if err != nil {
			return
		}
		switch msg.Type {
		case msgHello:
			if msg.HeartbeatSeconds > 0 {
				select {
				case heartbeat <- time.Duration(msg.HeartbeatSeconds) * time.Second:
				default:
				}
			}
		case msgApplicationsChanged:
			log.Printf("EDGE: application status changed: %s", msg.Message)
			m.setStatus(func(s *Status) { s.ApplicationsChanged++ })
		}
	}
}

// heartbeat reports this node's models and load at the interval the
// gateway asked for; missing a few makes the gateway take the node offline.
//
// stale reports whether the configured gateway or sign-in no longer matches
// the tunnel's; the tunnel then drains and closes so the connector follows.
func (m *Manager) heartbeat(ctx context.Context, mux *yamux.Session, control *controlConn, interval <-chan time.Duration, stale func() bool) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-mux.CloseChan():
			return
		case d := <-interval:
			ticker.Reset(d)
		case <-ticker.C:
			// A license that lapses, or a change of gateway or account,
			// ends this tunnel at the next heartbeat.
			if !m.host.Licensed() || stale() {
				_ = control.write(controlMessage{Type: msgDrain})
				_ = mux.Close()
				return
			}
			if err := control.write(m.statusMessage(ctx)); err != nil {
				_ = mux.Close()
				return
			}
		}
	}
}

func (m *Manager) statusMessage(ctx context.Context) controlMessage {
	models := m.host.Models(ctx)
	out := make([]statusModel, 0, len(models))
	for _, mod := range models {
		out = append(out, statusModel{ID: mod.ID, Ready: mod.Ready, State: mod.State})
	}
	return controlMessage{
		Type:           msgStatus,
		Models:         out,
		ActiveRequests: m.host.ActiveRequests(),
		Version:        m.host.Version(),
	}
}

func randInt(n int64) int64 {
	if n <= 0 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		return 0
	}
	return v.Int64()
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
