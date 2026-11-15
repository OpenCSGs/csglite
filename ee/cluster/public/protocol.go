// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

// Package public implements the authenticated outbound transport for public
// clusters. Membership, licensing and scheduling belong to its host; this
// package never listens on a worker's network interfaces.
package public

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

const (
	ConnectPath     = "/cluster/v1/connect"
	ProtocolVersion = "1"
	HeaderNode      = "X-CSGLite-Node-UUID"
	HeaderToken     = "X-CSGLite-Node-Token"
	HeaderProtocol  = "X-CSGLite-Cluster-Protocol"
	maxControlBytes = 1 << 20
	maxRequestBytes = 32 << 20
)

var ErrOffline = errors.New("public cluster node is offline")

// ErrUnavailable lets an admitted node retry a temporary gateway/license outage
// without treating its still-valid identity as revoked.
var ErrUnavailable = errors.New("public cluster is temporarily unavailable")

type controlMessage struct {
	Type        string          `json:"type"`
	Status      json.RawMessage `json:"status,omitempty"`
	HeartbeatMS int64           `json:"heartbeat_ms,omitempty"`
}

type controlStream struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
}

func newControl(conn net.Conn) *controlStream {
	return &controlStream{conn: conn, reader: bufio.NewReaderSize(conn, maxControlBytes)}
}

func (c *controlStream) read() (controlMessage, error) {
	var msg controlMessage
	line, err := c.reader.ReadSlice('\n')
	if err != nil {
		return msg, err
	}
	err = json.Unmarshal(line, &msg)
	return msg, err
}

func (c *controlStream) write(msg controlMessage, timeout time.Duration) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(data)+1 > maxControlBytes {
		return errors.New("public cluster control message too large")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err = c.conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	_, err = c.conn.Write(append(data, '\n'))
	return err
}

// acceptControl must close the session on timeout so its accept goroutine exits.
func acceptControl(ctx context.Context, mux *yamux.Session) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	done := make(chan result, 1)
	go func() { conn, err := mux.AcceptStream(); done <- result{conn, err} }()
	select {
	case result := <-done:
		return result.conn, result.err
	case <-ctx.Done():
		_ = mux.Close()
		result := <-done
		if result.conn != nil {
			_ = result.conn.Close()
		}
		return nil, ctx.Err()
	}
}

// openStream propagates dial cancellation without closing the whole session.
func openStream(ctx context.Context, mux *yamux.Session) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	done := make(chan result)
	go func() {
		conn, err := mux.OpenStream()
		select {
		case done <- result{conn, err}:
		case <-ctx.Done():
			if conn != nil {
				_ = conn.Close()
			}
		}
	}()
	select {
	case result := <-done:
		return result.conn, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Both ends enforce this list. No URL supplied by a caller is ever dialed.
var inferencePaths = map[string]bool{
	"/v1/chat/completions":      true,
	"/v1/embeddings":            true,
	"/v1/responses":             true,
	"/v1/messages":              true,
	"/v1/messages/count_tokens": true,
	"/api/chat":                 true,
	"/api/generate":             true,
}

var forwardedHeaders = []string{
	"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta", "OpenAI-Beta",
	"X-Request-ID", "X-CSGLite-Trace-ID", "X-CSGLite-Thread-ID",
}

type requestError struct {
	status  int
	message string
}

func (e *requestError) Error() string { return e.message }

func inferenceRequest(req *http.Request, allowModel func(string) bool) (*http.Request, error) {
	if req.Method != http.MethodPost || !inferencePaths[req.URL.Path] || req.URL.RawQuery != "" || req.URL.RawPath != "" {
		return nil, &requestError{http.StatusNotFound, "not a public cluster inference path"}
	}
	if req.Body == nil {
		return nil, &requestError{http.StatusBadRequest, "a JSON inference body is required"}
	}
	data, err := io.ReadAll(io.LimitReader(req.Body, maxRequestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read inference request: %w", err)
	}
	if len(data) > maxRequestBytes {
		return nil, &requestError{http.StatusRequestEntityTooLarge, "inference request too large"}
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(data, &body) != nil || body == nil {
		return nil, &requestError{http.StatusBadRequest, "a JSON object is required"}
	}
	var model, source string
	if json.Unmarshal(body["model"], &model) != nil || model == "" {
		return nil, &requestError{http.StatusBadRequest, "model is required"}
	}
	if allowModel != nil && !allowModel(model) {
		return nil, &requestError{http.StatusForbidden, "model is not shared by this node"}
	}
	if value, ok := body["source"]; ok {
		if json.Unmarshal(value, &source) != nil || (source != "" && source != "local") {
			return nil, &requestError{http.StatusBadRequest, "a tunneled request must execute locally"}
		}
	}
	if req.URL.Path == "/v1/responses" {
		var background bool
		if value, ok := body["background"]; ok && (json.Unmarshal(value, &background) != nil || background) {
			return nil, &requestError{http.StatusBadRequest, "background responses are not supported"}
		}
		if _, ok := body["previous_response_id"]; ok {
			return nil, &requestError{http.StatusBadRequest, "stored response continuation is not supported"}
		}
	}
	body["source"] = json.RawMessage(`"local"`)
	data, err = json.Marshal(body)
	if err != nil {
		return nil, err
	}
	out := req.Clone(req.Context())
	out.URL.Scheme = "http"
	out.URL.Host = "cluster-node"
	out.Host = "cluster-node"
	out.RequestURI = ""
	out.Header = make(http.Header)
	for _, name := range forwardedHeaders {
		if values := req.Header.Values(name); len(values) > 0 {
			out.Header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
	out.Header.Set("Content-Type", "application/json")
	out.Trailer = nil
	out.TransferEncoding = nil
	out.Body = io.NopCloser(bytes.NewReader(data))
	out.ContentLength = int64(len(data))
	out.GetBody = nil // Execution is never automatically replayed.
	return out, nil
}
