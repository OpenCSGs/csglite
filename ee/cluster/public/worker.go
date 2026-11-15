// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package public

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/opencsgs/csglite/ee/tunnel"
)

type WorkerOptions struct {
	GatewayURL string
	NodeUUID   string
	Token      string
	Status     func(context.Context) (json.RawMessage, error)
	AllowModel func(string) bool
	// ConnectionChanged reports an accepted tunnel and its subsequent closure.
	// Failed TLS/authentication/hello handshakes never report online.
	ConnectionChanged func(bool)
	// StatusUpdates requests an immediate publication. The buffered reply
	// channel is completed after a supporting gateway accepts the snapshot.
	StatusUpdates <-chan chan error
	// Handler must execute locally. It must not route to another cluster/cloud.
	Handler http.Handler
	// DeploymentHandler accepts only a typed assignment on the private tunnel.
	// The host must independently check explicit worker management consent.
	DeploymentHandler  http.Handler
	ModelActionHandler http.Handler
	NodeStateHandler   http.Handler
	// Dialer may supply a private CA or enterprise proxy; TLS verification stays on.
	Dialer *websocket.Dialer
}

type Worker struct {
	opts     WorkerOptions
	endpoint string
}

func NewWorker(opts WorkerOptions) (*Worker, error) {
	if opts.NodeUUID == "" || len(opts.NodeUUID) > 128 || opts.Token == "" || opts.Status == nil || opts.AllowModel == nil || opts.Handler == nil {
		return nil, errors.New("public worker requires identity, credential, status, sharing policy and a local handler")
	}
	u, err := url.Parse(opts.GatewayURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("public gateway must be an HTTPS origin without credentials, path, query or fragment")
	}
	if opts.Dialer != nil && opts.Dialer.TLSClientConfig != nil && opts.Dialer.TLSClientConfig.InsecureSkipVerify {
		return nil, errors.New("public gateway TLS verification cannot be disabled")
	}
	if opts.Dialer != nil {
		copyDialer := *opts.Dialer
		if copyDialer.TLSClientConfig != nil {
			copyDialer.TLSClientConfig = copyDialer.TLSClientConfig.Clone()
		}
		opts.Dialer = &copyDialer
	}
	u.Scheme = "wss"
	u.Path = ConnectPath
	return &Worker{opts: opts, endpoint: u.String()}, nil
}

// Connect establishes one session. Its host owns jittered retry, persists
// credentials and stops retrying revoked identities rather than re-enrolling.
func (w *Worker) Connect(ctx context.Context) error {
	header := http.Header{}
	header.Set(HeaderNode, w.opts.NodeUUID)
	header.Set(HeaderToken, w.opts.Token)
	header.Set(HeaderProtocol, ProtocolVersion)
	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 15 * time.Second}
	if w.opts.Dialer != nil {
		dialer = *w.opts.Dialer
	}
	ws, resp, err := dialer.DialContext(ctx, w.endpoint, header)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return &HandshakeError{Status: resp.StatusCode}
		}
		// Do not include URL or header-bearing errors in logs.
		return errors.New("public gateway connection failed")
	}
	defer ws.Close()
	ws.SetReadLimit(2 << 20)
	cfg := tunnel.Config(5 * time.Second)
	cfg.StreamOpenTimeout = 15 * time.Second
	mux, err := yamux.Server(tunnel.NewWebSocketStream(ws), cfg)
	if err != nil {
		return fmt.Errorf("open public tunnel: %w", err)
	}
	defer mux.Close()
	stop := context.AfterFunc(ctx, func() { _ = mux.Close() })
	defer stop()
	stream, err := mux.OpenStream()
	if err != nil {
		return err
	}
	control := newControl(stream)
	sendStatus := func(sequence uint64) error {
		status, err := w.opts.Status(ctx)
		if err != nil {
			return err
		}
		if !json.Valid(status) {
			return errors.New("invalid local status")
		}
		return control.write(controlMessage{Type: "status", Status: status, Sequence: sequence}, 15*time.Second)
	}
	if err = sendStatus(0); err != nil {
		return err
	}
	_ = stream.SetReadDeadline(time.Now().Add(15 * time.Second))
	hello, err := control.read()
	if err != nil {
		return fmt.Errorf("public tunnel handshake: %w", err)
	}
	if hello.Type != "hello" || hello.HeartbeatMS < 1 || hello.HeartbeatMS > 60000 {
		return errors.New("invalid public gateway handshake")
	}
	heartbeat := time.Duration(hello.HeartbeatMS) * time.Millisecond
	_ = stream.SetReadDeadline(time.Time{})
	acks := make(chan uint64, 1)
	var heartbeatWG sync.WaitGroup
	if hello.StatusAck {
		heartbeatWG.Add(1)
		go func() {
			defer heartbeatWG.Done()
			defer mux.Close()
			for {
				msg, err := control.read()
				if err != nil || msg.Type != "status_ack" {
					return
				}
				select {
				case acks <- msg.Sequence:
				case <-mux.CloseChan():
					return
				}
			}
		}()
	}
	heartbeatWG.Add(1)
	go func() {
		defer heartbeatWG.Done()
		tick := time.NewTicker(heartbeat)
		defer tick.Stop()
		var sequence uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-mux.CloseChan():
				return
			case <-tick.C:
				if sendStatus(0) != nil {
					_ = mux.Close()
					return
				}
			case done := <-w.opts.StatusUpdates:
				sequence++
				seq := uint64(0)
				if hello.StatusAck {
					seq = sequence
				}
				err := sendStatus(seq)
				if err == nil && hello.StatusAck {
					err = waitStatusAck(ctx, mux.CloseChan(), acks, sequence)
				}
				done <- err
				if err != nil {
					_ = mux.Close()
					return
				}
			}
		}
	}()
	defer func() { _ = mux.Close(); heartbeatWG.Wait() }()
	server := &http.Server{Handler: w.handler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 64 << 10}
	defer server.Close()
	if w.opts.ConnectionChanged != nil {
		w.opts.ConnectionChanged(true)
		defer w.opts.ConnectionChanged(false)
	}
	err = server.Serve(mux)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("public tunnel ended: %w", err)
}

func waitStatusAck(ctx context.Context, closed <-chan struct{}, acks <-chan uint64, sequence uint64) error {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case got := <-acks:
		if got != sequence {
			return errors.New("invalid public status acknowledgment")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-closed:
		return ErrOffline
	case <-timer.C:
		return errors.New("public status acknowledgment timed out")
	}
}

type HandshakeError struct{ Status int }

func (e *HandshakeError) Error() string {
	return fmt.Sprintf("public gateway refused connection (HTTP %d)", e.Status)
}

func (w *Worker) handler() http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		controlHandler := w.opts.DeploymentHandler
		if r.URL.Path == ModelActionPath {
			controlHandler = w.opts.ModelActionHandler
		}
		if r.URL.Path == NodeStatePath {
			controlHandler = w.opts.NodeStateHandler
		}
		if (r.URL.Path == DeploymentPath || r.URL.Path == ModelActionPath || r.URL.Path == NodeStatePath) && controlHandler != nil {
			if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.RawPath != "" {
				http.Error(rw, "invalid deployment request", 400)
				return
			}
			r.Body = http.MaxBytesReader(rw, r.Body, 1<<20)
			r.Header = make(http.Header)
			controlHandler.ServeHTTP(rw, r)
			return
		}
		out, err := inferenceRequest(r, w.opts.AllowModel)
		if err != nil {
			status := http.StatusBadRequest
			var re *requestError
			if errors.As(err, &re) {
				status = re.status
			}
			errType := "invalid_request_error"
			code := ""
			if status == http.StatusForbidden {
				errType, code = "permission_error", "cluster_model_not_shared"
			}
			payload := map[string]any{"errorCode": status, "error": map[string]any{"message": strings.TrimSpace(err.Error()), "type": errType}}
			if code != "" {
				payload["code"] = code
			}
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(status)
			_ = json.NewEncoder(rw).Encode(payload)
			return
		}
		defer out.Body.Close()
		// A local request handler must never see the public gateway's virtual host.
		out.URL.Scheme = ""
		out.URL.Host = ""
		out.Host = "localhost"
		out.RequestURI = out.URL.RequestURI()
		// Drop connection-specific tokens even if a future header is allowlisted.
		for _, name := range strings.Split(r.Header.Get("Connection"), ",") {
			out.Header.Del(strings.TrimSpace(name))
		}
		w.opts.Handler.ServeHTTP(rw, out)
	})
}
