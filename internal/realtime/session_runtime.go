package realtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Transcriber recognises a continuous audio stream. Implementations wrap a
// streaming ASR engine.
type Transcriber interface {
	// Write accepts mono PCM16 at the session's input rate.
	Write(pcm []byte) error
	// Commit ends the current turn and asks for a final transcript.
	Commit() error
	// Reset discards buffered audio without producing a transcript.
	Reset() error
	Close() error
}

// TranscriptEvent is emitted by a Transcriber as recognition progresses.
type TranscriptEvent struct {
	// Kind is "delta", "completed", "failed", "speech_started" or
	// "speech_stopped".
	Kind  string
	Text  string
	Error error
}

// Synthesizer turns text into PCM16 audio, delivering it in pieces so playback
// can start before generation finishes.
//
// The model is passed per response rather than fixed when the session is
// created, because session.update may name the speech model at any point --
// which is how the OpenAI clients configure it -- and a synthesizer bound to
// the initial configuration would never see that.
type Synthesizer interface {
	// Speak streams audio for text using model, which is empty when the
	// session names none and the server's own default applies. It must return
	// promptly when ctx is cancelled, which is how response.cancel stops
	// generation.
	Speak(ctx context.Context, model, text, voice string, onAudio func(pcm []byte) error) (sampleRate int, err error)
	// CanSpeak reports whether the session could synthesise with this model,
	// so a response that has nowhere to go fails before it starts.
	CanSpeak(model string) bool
}

// AudioFrame is one piece of synthesised audio together with the event that
// announces it. The event is stamped by the session, so a transport that sends
// audio as an event carries the same identifiers and ordering as every other
// event rather than inventing its own.
type AudioFrame struct {
	Event      ServerEvent
	PCM        []byte
	SampleRate int
}

// Sender delivers a server event on the transport.
type Sender interface {
	Send(ServerEvent) error
	// SendAudio delivers synthesised audio. The WebSocket transport sends the
	// frame's event with the audio base64-encoded in it; WebRTC writes the PCM
	// to the media track and ignores the event, which is the only place the two
	// transports differ.
	SendAudio(AudioFrame) error
}

// Session drives one realtime conversation.
type Session struct {
	ID     string
	Config SessionConfig

	pipeline Pipeline
	sender   Sender
	sequence atomic.Int64

	mu          sync.Mutex
	transcriber Transcriber
	synth       Synthesizer

	// response holds the in-flight response so cancel can stop it. Guarded by
	// mu; the cancel func is called outside the lock to avoid holding it across
	// synthesis teardown.
	responseID     string
	responseCancel context.CancelFunc
	// audioStopped records whether output_audio_buffer.stopped has been sent for
	// the current response, so cancel and normal completion cannot both send it.
	audioStopped bool
}

// NewSession creates a session and emits session.created.
func NewSession(cfg SessionConfig, sender Sender, transcriber Transcriber, synth Synthesizer) (*Session, error) {
	s := &Session{
		ID:          "sess_" + randomID(),
		Config:      cfg,
		sender:      sender,
		transcriber: transcriber,
		synth:       synth,
	}
	s.pipeline = s.Config.Normalize()
	if err := s.emit(ServerEvent{Type: ServerSessionCreated, Session: &s.Config}); err != nil {
		return nil, err
	}
	return s, nil
}

// Pipeline reports which models this session drives.
func (s *Session) Pipeline() Pipeline { return s.pipeline }

func (s *Session) nextEvent() (string, int64) {
	seq := s.sequence.Add(1)
	return "event_" + randomID(), seq
}

// emit stamps an event with the identifiers every event carries and sends it.
func (s *Session) emit(ev ServerEvent) error {
	id, seq := s.nextEvent()
	ev.EventID = id
	ev.Sequence = seq
	ev.SessionID = s.ID
	ev.Timestamp = time.Now().UnixMilli()
	if ev.Model == "" {
		ev.Model = s.Config.Model
	}
	// The lifecycle events carry a response object, since that is where a
	// client looks for the id and status.
	if ev.Response == nil && ev.ResponseID != "" {
		switch ev.Type {
		case ServerResponseCreated:
			ev.Response = &ResponseInfo{ID: ev.ResponseID, Object: "realtime.response", Status: "in_progress"}
		case ServerResponseDone:
			ev.Response = &ResponseInfo{ID: ev.ResponseID, Object: "realtime.response", Status: ev.Status}
		}
	}
	return s.sender.Send(ev)
}

// EmitError reports a fault to the client without ending the session.
func (s *Session) EmitError(code, message, causedBy string) {
	_ = s.emit(ServerEvent{
		Type:  ServerError,
		Error: &EventError{Type: "invalid_request_error", Code: code, Message: message, EventID: causedBy},
	})
}

// HandleAudio appends caller audio to the current turn.
func (s *Session) HandleAudio(pcm []byte) error {
	if s.transcriber == nil {
		return nil
	}
	return s.transcriber.Write(pcm)
}

// HandleTranscript forwards a Transcriber event to the client.
func (s *Session) HandleTranscript(ev TranscriptEvent) error {
	switch ev.Kind {
	case "speech_started":
		return s.emit(ServerEvent{Type: ServerSpeechStarted})
	case "speech_stopped":
		return s.emit(ServerEvent{Type: ServerSpeechStopped})
	case "delta":
		return s.emit(ServerEvent{Type: ServerTranscriptDelta, Delta: ev.Text, ItemID: "item_" + randomID()})
	case "completed":
		return s.emit(ServerEvent{Type: ServerTranscriptCompleted, Transcript: ev.Text, ItemID: "item_" + randomID()})
	case "failed":
		message := "transcription failed"
		if ev.Error != nil {
			message = ev.Error.Error()
		}
		return s.emit(ServerEvent{
			Type:  ServerTranscriptFailed,
			Error: &EventError{Type: "server_error", Message: message},
		})
	}
	return nil
}

// Commit ends the current turn.
func (s *Session) Commit() error {
	if s.transcriber != nil {
		if err := s.transcriber.Commit(); err != nil {
			return err
		}
	}
	return s.emit(ServerEvent{Type: ServerInputAudioCommitted})
}

// ClearInput discards buffered caller audio.
func (s *Session) ClearInput() error {
	if s.transcriber != nil {
		if err := s.transcriber.Reset(); err != nil {
			return err
		}
	}
	return s.emit(ServerEvent{Type: ServerInputAudioCleared})
}

// UpdateConfig applies a session.update patch and echoes session.updated.
func (s *Session) UpdateConfig(patch []byte) error {
	s.mu.Lock()
	if err := s.Config.Merge(patch); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("invalid session patch: %w", err)
	}
	s.pipeline = s.Config.Normalize()
	cfg := s.Config
	s.mu.Unlock()
	return s.emit(ServerEvent{Type: ServerSessionUpdated, Session: &cfg})
}

func randomID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// A collision only mislabels an event, so a time-based fallback is
		// better than failing the session.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

// Respond synthesises text and streams it to the client. It returns once the
// audio has been delivered or the response was cancelled.
//
// response.cancel and output_audio_buffer.clear are deliberately different:
// cancel stops generation and reports the response as cancelled, while clear
// only discards audio still queued on the server. Both end up here because both
// have to stop the synthesis that is feeding the queue.
func (s *Session) Respond(ctx context.Context, text, voice string) error {
	if s.pipeline == PipelineASROnly {
		s.EmitError("unsupported_operation", "a transcription session cannot generate audio", "")
		return nil
	}
	s.mu.Lock()
	model := s.Config.SpeechModel()
	s.mu.Unlock()
	if s.synth == nil || !s.synth.CanSpeak(model) {
		s.EmitError("no_speech_model", "no text-to-speech model is configured for this session", "")
		return nil
	}
	if text == "" {
		s.EmitError("empty_response", "response.create needs text to speak", "")
		return nil
	}

	// Starting a response cancels any response still running, which is what
	// makes barge-in work.
	s.cancelActive("superseded")

	responseCtx, cancel := context.WithCancel(ctx)
	responseID := "resp_" + randomID()
	s.mu.Lock()
	s.responseID = responseID
	s.responseCancel = cancel
	s.audioStopped = false
	s.mu.Unlock()
	defer cancel()

	if err := s.emit(ServerEvent{Type: ServerResponseCreated, ResponseID: responseID}); err != nil {
		return err
	}

	started := false
	rate, err := s.synth.Speak(responseCtx, model, text, voice, func(pcm []byte) error {
		if !started {
			started = true
			if emitErr := s.emit(ServerEvent{Type: ServerOutputAudioStarted, ResponseID: responseID}); emitErr != nil {
				return emitErr
			}
		}
		return s.sendAudio(responseID, pcm)
	})

	cancelled := responseCtx.Err() != nil
	s.mu.Lock()
	current := s.responseID == responseID
	if current {
		s.responseID = ""
		s.responseCancel = nil
	}
	s.mu.Unlock()

	if !current {
		// A newer response took over; it owns the lifecycle events now.
		return nil
	}
	if cancelled {
		s.finishAudio(responseID)
		return s.emit(ServerEvent{Type: ServerResponseDone, ResponseID: responseID, Status: "cancelled"})
	}
	if err != nil {
		s.finishAudio(responseID)
		_ = s.emit(ServerEvent{
			Type:  ServerError,
			Error: &EventError{Type: "server_error", Message: err.Error()},
		})
		return s.emit(ServerEvent{Type: ServerResponseDone, ResponseID: responseID, Status: "failed"})
	}
	if err := s.emit(ServerEvent{Type: ServerAudioDone, ResponseID: responseID, SampleRate: rate}); err != nil {
		return err
	}
	s.finishAudio(responseID)
	return s.emit(ServerEvent{Type: ServerResponseDone, ResponseID: responseID, Status: "completed"})
}

// sendAudio hands one piece of synthesised audio to the transport, stamped like
// any other event so a client can order it against the rest of the stream.
func (s *Session) sendAudio(responseID string, pcm []byte) error {
	id, seq := s.nextEvent()
	return s.sender.SendAudio(AudioFrame{
		Event: ServerEvent{
			Type:       ServerAudioDelta,
			EventID:    id,
			Sequence:   seq,
			SessionID:  s.ID,
			ResponseID: responseID,
			Timestamp:  time.Now().UnixMilli(),
		},
		PCM: pcm,
	})
}

// finishAudio emits output_audio_buffer.stopped once per response. Cancel and
// normal completion can both reach it, and a client that saw two stops for one
// response would have no way to tell how many responses ran.
func (s *Session) finishAudio(responseID string) {
	s.mu.Lock()
	if s.audioStopped {
		s.mu.Unlock()
		return
	}
	s.audioStopped = true
	s.mu.Unlock()
	_ = s.emit(ServerEvent{Type: ServerOutputAudioStopped, ResponseID: responseID})
}

// cancelActive stops the running response, if any, and reports the id it
// stopped.
func (s *Session) cancelActive(reason string) string {
	s.mu.Lock()
	cancel := s.responseCancel
	id := s.responseID
	s.responseCancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return id
}

// CancelResponse handles response.cancel: stop generating and stop speaking.
func (s *Session) CancelResponse() error {
	if id := s.cancelActive("client"); id == "" {
		// Nothing running. Saying so beats silence, since a client that cancels
		// on a keypress cannot know whether a response was still active.
		s.EmitError("no_active_response", "no response is currently being generated", "")
	}
	return nil
}

// ClearOutputAudio handles output_audio_buffer.clear: drop audio that has not
// been sent yet and stop the synthesis feeding it.
func (s *Session) ClearOutputAudio() error {
	id := s.cancelActive("client")
	if flusher, ok := s.sender.(interface{ DiscardPendingAudio() }); ok {
		flusher.DiscardPendingAudio()
	}
	return s.emit(ServerEvent{Type: ServerOutputAudioCleared, ResponseID: id})
}

// Close releases the session's engines.
func (s *Session) Close() {
	s.cancelActive("closed")
	s.mu.Lock()
	transcriber := s.transcriber
	s.transcriber = nil
	s.mu.Unlock()
	if transcriber != nil {
		_ = transcriber.Close()
	}
}
