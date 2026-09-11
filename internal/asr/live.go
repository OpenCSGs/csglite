package asr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/opencsgs/csglite/pkg/api"
)

// LiveEvent is a transcript event from a streaming session.
type LiveEvent struct {
	// Kind is "ready", "speech_started", "speech_stopped", "delta",
	// "completed", "committed", "cleared" or "failed".
	Kind  string `json:"kind"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

// LiveStream is a push-based recognition session: audio goes in as it arrives
// and transcripts come out, with no file to upload first.
type LiveStream struct {
	conn   *websocket.Conn
	events chan LiveEvent

	mu     sync.Mutex
	closed bool
}

// StreamingEngine is implemented by engines that can recognise a continuous
// stream. It is separate from Engine so the file-based path keeps working
// unchanged.
type StreamingEngine interface {
	Engine
	OpenLive(ctx context.Context, cfg LiveConfig) (*LiveStream, error)
}

// LiveConfig opens a streaming session.
type LiveConfig struct {
	// SampleRate of the mono PCM16 frames that will be written.
	SampleRate int
	// PartialInterval bounds how often a partial hypothesis is produced. The
	// worker re-transcribes the audio so far to make one, so a short interval
	// costs proportionally more compute.
	PartialInterval time.Duration
	Request         api.OpenAIAudioTranscriptionRequest
}

// OpenLive starts a streaming session on the worker.
func (e *PythonEngine) OpenLive(ctx context.Context, cfg LiveConfig) (*LiveStream, error) {
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 24000
	}
	if cfg.PartialInterval <= 0 {
		cfg.PartialInterval = 600 * time.Millisecond
	}
	endpoint := url.URL{Scheme: "ws", Host: fmt.Sprintf("127.0.0.1:%d", e.port), Path: "/transcribe_live"}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("opening ASR live stream: %w", err)
	}
	start := map[string]interface{}{
		"type":             "start",
		"sample_rate":      cfg.SampleRate,
		"partial_interval": cfg.PartialInterval.Seconds(),
		"request":          cfg.Request,
	}
	if err := conn.WriteJSON(start); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("starting ASR live stream: %w", err)
	}
	stream := &LiveStream{conn: conn, events: make(chan LiveEvent, 32)}
	go stream.readLoop()
	return stream, nil
}

func (s *LiveStream) readLoop() {
	defer close(s.events)
	for {
		var ev LiveEvent
		if err := s.conn.ReadJSON(&ev); err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if !closed {
				s.events <- LiveEvent{Kind: "failed", Error: err.Error()}
			}
			return
		}
		s.events <- ev
	}
}

// Events yields transcript events until the stream closes.
func (s *LiveStream) Events() <-chan LiveEvent { return s.events }

// Write appends mono PCM16 audio.
func (s *LiveStream) Write(pcm []byte) error {
	if len(pcm) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("ASR live stream is closed")
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, pcm)
}

// Commit ends the current turn and asks for a final transcript.
func (s *LiveStream) Commit() error { return s.control("commit") }

// Reset discards buffered audio without transcribing it.
func (s *LiveStream) Reset() error { return s.control("reset") }

func (s *LiveStream) control(kind string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("ASR live stream is closed")
	}
	payload, err := json.Marshal(map[string]string{"type": kind})
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, payload)
}

// Close ends the session.
func (s *LiveStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conn := s.conn
	s.mu.Unlock()
	payload, _ := json.Marshal(map[string]string{"type": "close"})
	_ = conn.WriteMessage(websocket.TextMessage, payload)
	return conn.Close()
}
