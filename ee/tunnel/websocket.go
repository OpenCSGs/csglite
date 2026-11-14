// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

// Package tunnel supplies the byte transport shared by Lite's outbound tunnels.
package tunnel

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

// Config preserves the CSGHub edge tunnel's multiplexing and keepalive settings.
func Config(heartbeat time.Duration) *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = heartbeat
	cfg.ConnectionWriteTimeout = 2 * heartbeat
	cfg.MaxStreamWindowSize = 1024 * 1024
	cfg.LogOutput = io.Discard
	return cfg
}

// WebSocketStream adapts binary WebSocket messages to a continuous byte stream.
// It permits one reader and one writer concurrently, as required by yamux.
type WebSocketStream struct {
	ws      *websocket.Conn
	reader  io.Reader
	readMu  sync.Mutex
	writeMu sync.Mutex
}

func NewWebSocketStream(ws *websocket.Conn) *WebSocketStream {
	return &WebSocketStream{ws: ws}
}

func (s *WebSocketStream) Read(p []byte) (int, error) {
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

func (s *WebSocketStream) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *WebSocketStream) Close() error { return s.ws.Close() }
