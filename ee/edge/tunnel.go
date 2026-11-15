// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package edge

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/opencsgs/csglite/ee/tunnel"
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
	// ActiveRequests counts the requests this model is serving right now.
	ActiveRequests int `json:"active_requests,omitempty"`
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
	return tunnel.Config(heartbeat)
}

func newWSStream(ws *websocket.Conn) *tunnel.WebSocketStream {
	return tunnel.NewWebSocketStream(ws)
}
