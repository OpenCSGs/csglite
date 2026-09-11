package server

import (
	"context"
	"log"
	"sync"

	"github.com/opencsgs/csglite/internal/realtime"
)

// deferredTranscriberBufferBytes caps the audio held while the recognition
// engine loads: 30 seconds of 16kHz mono PCM16. Loading a cold ASR model takes
// seconds, and a caller who starts talking immediately would otherwise lose
// that speech; a cap keeps a client that never stops talking from growing the
// buffer without bound.
const deferredTranscriberBufferBytes = realtime.DefaultInputSampleRate * 2 * 30

// deferredTranscriber accepts audio before the recognition engine is ready and
// replays it once the engine has loaded. Loading the engine inline would block
// the SDP answer -- or the first event of a WebSocket session -- for as long as
// the model takes to load, which for a cold model is seconds.
type deferredTranscriber struct {
	mu      sync.Mutex
	ready   realtime.Transcriber
	pending []byte
	// commitPending records a Commit that arrived before the engine, so the
	// turn is still finalised once the audio has been replayed.
	commitPending bool
	dropped       int
	closed        bool
}

func (d *deferredTranscriber) Write(pcm []byte) error {
	d.mu.Lock()
	if d.ready != nil {
		engine := d.ready
		d.mu.Unlock()
		return engine.Write(pcm)
	}
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	if len(d.pending)+len(pcm) > deferredTranscriberBufferBytes {
		d.dropped += len(pcm)
		d.mu.Unlock()
		return nil
	}
	d.pending = append(d.pending, pcm...)
	d.mu.Unlock()
	return nil
}

func (d *deferredTranscriber) Commit() error {
	d.mu.Lock()
	if d.ready != nil {
		engine := d.ready
		d.mu.Unlock()
		return engine.Commit()
	}
	d.commitPending = true
	d.mu.Unlock()
	return nil
}

func (d *deferredTranscriber) Reset() error {
	d.mu.Lock()
	if d.ready != nil {
		engine := d.ready
		d.mu.Unlock()
		return engine.Reset()
	}
	d.pending = nil
	d.commitPending = false
	d.mu.Unlock()
	return nil
}

func (d *deferredTranscriber) Close() error {
	d.mu.Lock()
	d.closed = true
	d.pending = nil
	engine := d.ready
	d.mu.Unlock()
	if engine != nil {
		return engine.Close()
	}
	return nil
}

// attach installs the loaded engine and replays whatever arrived while it was
// loading. It reports false when the session has already closed, so the caller
// releases the engine it just loaded.
func (d *deferredTranscriber) attach(engine realtime.Transcriber) bool {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return false
	}
	d.ready = engine
	pending := d.pending
	commit := d.commitPending
	dropped := d.dropped
	d.pending = nil
	d.commitPending = false
	d.mu.Unlock()

	if dropped > 0 {
		log.Printf("REALTIME: dropped %d bytes of audio while the recognition engine loaded", dropped)
	}
	if len(pending) > 0 {
		if err := engine.Write(pending); err != nil {
			return true
		}
	}
	if commit {
		_ = engine.Commit()
	}
	return true
}

// newRealtimePipeline builds the recognition half of a session. It returns the
// transcriber to hand to the session and a function that loads the engine in
// the background, or (nil, nil) when the session does not recognise speech.
//
// Both transports use this so neither blocks its handshake on a model load:
// WebRTC must answer the SDP offer promptly, and a WebSocket client waits for
// session.created before it sends anything.
func (s *Server) newRealtimePipeline(ctx context.Context, cfg realtime.SessionConfig) (realtime.Transcriber, func(*realtime.Session)) {
	model := cfg.TranscriptionModel()
	if model == "" {
		model = s.cfg.Realtime.DefaultASRModel
	}
	if model == "" {
		return nil, nil
	}
	deferred := &deferredTranscriber{}
	return deferred, func(session *realtime.Session) {
		go func() {
			engine, transcripts, err := s.newRealtimeTranscriber(ctx, cfg)
			if err != nil {
				log.Printf("REALTIME: transcription unavailable: %v", err)
				session.EmitError("transcription_unavailable", err.Error(), "")
				return
			}
			if engine == nil {
				return
			}
			if !deferred.attach(engine) {
				// The session ended while the engine was loading.
				_ = engine.Close()
				return
			}
			for ev := range transcripts {
				if err := session.HandleTranscript(ev); err != nil {
					return
				}
			}
		}()
	}
}
