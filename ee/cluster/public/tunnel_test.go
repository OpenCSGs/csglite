// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package public

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/opencsgs/csglite/ee/tunnel"
)

func newFixture(t *testing.T, heartbeat time.Duration) (*Gateway, *httptest.Server, *websocket.Dialer) {
	t.Helper()
	g, err := NewGateway(GatewayOptions{
		Authorize: func(ctx context.Context, node, token string) error {
			if node != "worker-1" || token != "test-node-secret" {
				return errors.New("secret must not appear in response")
			}
			return nil
		},
		ValidateStatus: func(node string, status json.RawMessage) error {
			var s struct {
				Generation int `json:"generation"`
			}
			return json.Unmarshal(status, &s)
		},
		Heartbeat: heartbeat, HandshakeTimeout: time.Second, MaxSessions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(g)
	dialer := &websocket.Dialer{TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig.Clone(), HandshakeTimeout: time.Second}
	t.Cleanup(func() { _ = g.Close(); server.Close() })
	return g, server, dialer
}

func startWorker(t *testing.T, g *Gateway, server *httptest.Server, dialer *websocket.Dialer, generation int, handler http.HandlerFunc) context.CancelFunc {
	t.Helper()
	w, err := NewWorker(WorkerOptions{GatewayURL: server.URL, NodeUUID: "worker-1", Token: "test-node-secret", Dialer: dialer,
		Status: func(context.Context) (json.RawMessage, error) {
			return json.RawMessage(fmt.Sprintf(`{"generation":%d}`, generation)), nil
		},
		AllowModel: func(model string) bool { return model == "shared-model" }, Handler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Connect(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("worker did not stop")
		}
	})
	eventually(t, func() bool {
		s, ok := g.Snapshot("worker-1")
		return ok && strings.Contains(string(s), fmt.Sprint(generation))
	})
	return cancel
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("condition did not become true")
		}
	}
}

func inference(ctx context.Context) *http.Request {
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://unused.invalid/v1/chat/completions", strings.NewReader(`{"model":"shared-model","stream":true}`))
	return r
}

func TestOutboundTunnelStreamsAndCancelsOneRequest(t *testing.T) {
	g, server, dialer := newFixture(t, 50*time.Millisecond)
	cancelled := make(chan struct{}, 1)
	var calls atomic.Int32
	startWorker(t, g, server, dialer, 1, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get(HeaderToken) != "" || r.Header.Get("X-Api-Key") != "" {
			t.Error("caller credentials reached worker")
		}
		if r.Header.Get("Anthropic-Beta") != "test-beta" || r.Header.Get("X-CSGLite-Trace-ID") != "trace-1" {
			t.Error("protocol or trace header was lost")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["source"] != "local" {
			t.Error("request was not pinned local")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		cancelled <- struct{}{}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := inference(ctx)
	r.Header.Set("Authorization", "Bearer caller-secret")
	r.Header.Set("Cookie", "session=secret")
	r.Header.Set(HeaderToken, "node-secret")
	r.Header.Set("X-Api-Key", "api-secret")
	r.Header.Set("Anthropic-Beta", "test-beta")
	r.Header.Set("X-CSGLite-Trace-ID", "trace-1")
	resp, err := g.RoundTrip("worker-1", r)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(resp.Body)
	first, err := reader.ReadString('\n')
	if err != nil || first != "data: first\n" {
		t.Fatalf("first chunk = %q, %v", first, err)
	}
	// Application heartbeats continue while the inference result is unfinished.
	time.Sleep(180 * time.Millisecond)
	if _, ok := g.Snapshot("worker-1"); !ok {
		t.Fatal("streaming request blocked heartbeats")
	}
	cancel()
	_ = resp.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not reach local handler")
	}
	if _, ok := g.Snapshot("worker-1"); !ok {
		t.Fatal("one request cancellation disconnected node")
	}
	if calls.Load() != 1 {
		t.Fatal("request was replayed")
	}
}

func TestWorkerGuardsModelsPathsSourcesAndCredentials(t *testing.T) {
	var calls int
	w := &Worker{opts: WorkerOptions{AllowModel: func(m string) bool { return m == "shared-model" }, Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { calls++; rw.WriteHeader(http.StatusOK) })}}
	for _, test := range []struct {
		name, path, method, body string
		want                     int
	}{
		{"valid", "/v1/embeddings", "POST", `{"model":"shared-model"}`, 200},
		{"management", "/api/settings", "POST", `{"model":"shared-model"}`, 404},
		{"wrong method", "/v1/embeddings", "GET", `{"model":"shared-model"}`, 404},
		{"query", "/v1/embeddings?source=cloud", "POST", `{"model":"shared-model"}`, 404},
		{"encoded", "/v1/%65mbeddings", "POST", `{"model":"shared-model"}`, 404},
		{"private model", "/v1/embeddings", "POST", `{"model":"private-model"}`, 403},
		{"cloud", "/v1/embeddings", "POST", `{"model":"shared-model","source":"cloud"}`, 400},
		{"onward", "/v1/embeddings", "POST", `{"model":"shared-model","source":"cluster"}`, 400},
		{"bad source", "/v1/embeddings", "POST", `{"model":"shared-model","source":{}}`, 400},
		{"background", "/v1/responses", "POST", `{"model":"shared-model","background":true}`, 400},
		{"stored response", "/v1/responses", "POST", `{"model":"shared-model","previous_response_id":"r1"}`, 400},
		{"not object", "/v1/embeddings", "POST", `[]`, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			rw := httptest.NewRecorder()
			w.handler().ServeHTTP(rw, r)
			if rw.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", rw.Code, test.want, rw.Body.String())
			}
		})
	}
	if calls != 1 {
		t.Fatalf("local calls = %d", calls)
	}
}

func TestGatewayNeverTrustsLoopbackAndHidesCredentialErrors(t *testing.T) {
	g, _, _ := newFixture(t, time.Second)
	r := httptest.NewRequest(http.MethodGet, ConnectPath, nil)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set(HeaderProtocol, ProtocolVersion)
	r.Header.Set(HeaderNode, "worker-1")
	r.Header.Set(HeaderToken, "bad")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 401 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("auth response = %d: %s", w.Code, w.Body.String())
	}
	r.Header.Set("Origin", "https://browser.invalid")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("browser origin = %d", w.Code)
	}
	r.Header.Del("Origin")
	r.Header.Set(HeaderProtocol, "unknown")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatalf("unknown protocol = %d", w.Code)
	}
}

func TestSessionReplacementAndDisconnectCannotResurrectOldSession(t *testing.T) {
	g, server, dialer := newFixture(t, 30*time.Millisecond)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })
	stopOld := startWorker(t, g, server, dialer, 1, handler)
	startWorker(t, g, server, dialer, 2, handler)
	stopOld()
	time.Sleep(100 * time.Millisecond)
	status, ok := g.Snapshot("worker-1")
	if !ok || !strings.Contains(string(status), "2") {
		t.Fatalf("replacement lost: %s, %v", status, ok)
	}
	status[0] = '!'
	if fresh, _ := g.Snapshot("worker-1"); !json.Valid(fresh) {
		t.Fatal("snapshot exposed mutable session state")
	}
	resp, err := g.RoundTrip("worker-1", inference(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	g.Disconnect("worker-1")
	if _, ok := g.Snapshot("worker-1"); ok {
		t.Fatal("disconnected session is still online")
	}
	if _, err = g.RoundTrip("worker-1", inference(context.Background())); !errors.Is(err, ErrOffline) {
		t.Fatalf("disconnected request = %v", err)
	}
}

func TestMissingApplicationHeartbeatsExpireLiveSocket(t *testing.T) {
	g, server, dialer := newFixture(t, 25*time.Millisecond)
	header := http.Header{}
	header.Set(HeaderNode, "worker-1")
	header.Set(HeaderToken, "test-node-secret")
	header.Set(HeaderProtocol, ProtocolVersion)
	ws, _, err := dialer.Dial("wss"+strings.TrimPrefix(server.URL, "https")+ConnectPath, header)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	mux, err := yamux.Server(tunnel.NewWebSocketStream(ws), tunnel.Config(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()
	stream, err := mux.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	control := newControl(stream)
	if err = control.write(controlMessage{Type: "status", Status: json.RawMessage(`{"generation":1}`)}, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err = control.read(); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { _, ok := g.Snapshot("worker-1"); return ok })
	// yamux keepalives remain active, but there are no application heartbeats.
	eventually(t, func() bool { _, ok := g.Snapshot("worker-1"); return !ok })
}

func TestWorkerRequiresVerifiedHTTPSOrigin(t *testing.T) {
	base := WorkerOptions{NodeUUID: "n", Token: "t", Status: func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }, AllowModel: func(string) bool { return true }, Handler: http.NotFoundHandler()}
	for _, endpoint := range []string{"http://gateway.invalid", "https://user:secret@gateway.invalid", "https://gateway.invalid/path", "https://gateway.invalid?token=secret", "https://gateway.invalid#fragment", "https://gateway.invalid?"} {
		base.GatewayURL = endpoint
		if _, err := NewWorker(base); err == nil {
			t.Errorf("accepted invalid gateway %s", endpoint)
		}
	}
	base.GatewayURL = "https://gateway.invalid"
	base.Dialer = &websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // Explicitly rejected below.
	if _, err := NewWorker(base); err == nil {
		t.Fatal("accepted disabled TLS verification")
	}
}

func TestWorkerHandshakeFailureIsTyped(t *testing.T) {
	_, server, dialer := newFixture(t, time.Second)
	changes := 0
	w, err := NewWorker(WorkerOptions{GatewayURL: server.URL, NodeUUID: "worker-1", Token: "wrong-secret", ConnectionChanged: func(bool) { changes++ }, Dialer: dialer, Status: func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }, AllowModel: func(string) bool { return true }, Handler: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	err = w.Connect(context.Background())
	var handshake *HandshakeError
	if !errors.As(err, &handshake) || handshake.Status != 401 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("handshake error = %v", err)
	}
	if changes != 0 {
		t.Fatal("rejected connection reported a state change")
	}
}

func TestConcurrentRequestsAndSharingRevocation(t *testing.T) {
	g, server, dialer := newFixture(t, 100*time.Millisecond)
	var shared atomic.Bool
	shared.Store(true)
	var active, peak atomic.Int32
	startWorker(t, g, server, dialer, 1, func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		time.Sleep(10 * time.Millisecond)
		_, _ = io.WriteString(w, r.Header.Get("X-Request-ID"))
	})
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := inference(context.Background())
			id := fmt.Sprintf("request-%d", i)
			r.Header.Set("X-Request-ID", id)
			resp, err := g.RoundTrip("worker-1", r)
			if err != nil {
				t.Errorf("concurrent round trip: %v", err)
				return
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil || string(data) != id {
				t.Errorf("concurrent response = %q, %v, want %s", data, err, id)
			}
		}()
	}
	wg.Wait()
	if peak.Load() < 2 {
		t.Fatal("requests were not multiplexed concurrently")
	}
	// Check the worker policy again for each request, independently of an old
	// model snapshot that the gateway might still hold.
	w := &Worker{opts: WorkerOptions{AllowModel: func(string) bool { return shared.Load() }, Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { rw.WriteHeader(200) })}}
	shared.Store(false)
	rw := httptest.NewRecorder()
	w.handler().ServeHTTP(rw, inference(context.Background()))
	if rw.Code != http.StatusForbidden {
		t.Fatalf("withdrawn model remained callable: %d", rw.Code)
	}
}

func TestCloseAlsoTerminatesAnUnfinishedHandshake(t *testing.T) {
	g, server, dialer := newFixture(t, time.Second)
	header := http.Header{}
	header.Set(HeaderNode, "worker-1")
	header.Set(HeaderToken, "test-node-secret")
	header.Set(HeaderProtocol, ProtocolVersion)
	ws, _, err := dialer.Dial("wss"+strings.TrimPrefix(server.URL, "https")+ConnectPath, header)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = g.Close()
	_ = ws.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err = ws.ReadMessage()
	var timeout interface{ Timeout() bool }
	if err == nil || (errors.As(err, &timeout) && timeout.Timeout()) {
		t.Fatalf("unfinished handshake did not close promptly: %v", err)
	}
}
