// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package edge

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

// The wire protocol is shared with the CSGHub AI gateway
// (aigateway/component/edgeserving): a WebSocket carrying a yamux session.
// This node is the yamux server: it opens the control stream and accepts
// one stream per HTTP connection the gateway relay opens.
const (
	headerNodeUUID        = "X-Edge-Node-UUID"
	headerNodeToken       = "X-Edge-Node-Token"
	headerTunnelProtocol  = "X-Edge-Protocol"
	tunnelProtocolVersion = "1"

	// HeaderRequestID is the opaque per-request ID the gateway attaches; it
	// is the only identity of a remote request a node ever sees.
	HeaderRequestID = "X-CSG-Request-ID"
)

const (
	msgStatus              = "status"
	msgDrain               = "drain"
	msgHello               = "hello"
	msgApplicationsChanged = "applications_changed"
)

type controlMessage struct {
	Type string `json:"type"`

	Models         []statusModel `json:"models,omitempty"`
	ActiveRequests int           `json:"active_requests,omitempty"`
	Version        string        `json:"version,omitempty"`

	HeartbeatSeconds int    `json:"heartbeat_seconds,omitempty"`
	NodeID           int64  `json:"node_id,omitempty"`
	Message          string `json:"message,omitempty"`
}

type statusModel struct {
	ID    string `json:"id"`
	Ready bool   `json:"ready"`
	// State is loaded, loading or sleeping (ready but not in memory).
	State string `json:"state,omitempty"`
}

const maxControlLine = 1 << 20

type controlConn struct {
	stream  net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
}

func newControlConn(stream net.Conn) *controlConn {
	return &controlConn{stream: stream, reader: bufio.NewReaderSize(stream, maxControlLine)}
}

func (c *controlConn) read() (controlMessage, error) {
	var msg controlMessage
	line, err := c.reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return msg, fmt.Errorf("control message exceeds %d bytes", maxControlLine)
	}
	if err != nil {
		return msg, err
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return msg, fmt.Errorf("decode control message: %w", err)
	}
	return msg, nil
}

func (c *controlConn) write(msg controlMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = c.stream.Write(append(data, '\n'))
	return err
}

func yamuxConfig(heartbeat time.Duration) *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = heartbeat
	cfg.ConnectionWriteTimeout = 2 * heartbeat
	cfg.MaxStreamWindowSize = 1024 * 1024
	cfg.LogOutput = io.Discard
	return cfg
}

// wsStream adapts a WebSocket to the byte stream yamux runs on.
type wsStream struct {
	ws      *websocket.Conn
	reader  io.Reader
	readMu  sync.Mutex
	writeMu sync.Mutex
}

func newWSStream(ws *websocket.Conn) *wsStream {
	return &wsStream{ws: ws}
}

func (s *wsStream) Read(p []byte) (int, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()
	for {
		if s.reader == nil {
			kind, r, err := s.ws.NextReader()
			if err != nil {
				return 0, err
			}
			if kind != websocket.BinaryMessage {
				continue
			}
			s.reader = r
		}
		n, err := s.reader.Read(p)
		if errors.Is(err, io.EOF) {
			s.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (s *wsStream) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *wsStream) Close() error {
	return s.ws.Close()
}
