package realtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
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
	// responder writes replies when the session names a conversation model;
	// ctx bounds the replies it starts on its own, since those have no client
	// event to inherit a context from. Both are set by the transport.
	responder Responder
	ctx       context.Context
	// history is the conversation so far, kept only when the session generates
	// replies itself: a client that writes its own replies keeps its own.
	history []Message

	// response holds the in-flight response so cancel can stop it. Guarded by
	// mu; the cancel func is called outside the lock to avoid holding it across
	// synthesis teardown.
	responseID     string
	responseCancel context.CancelFunc
	// audioStopped records whether output_audio_buffer.stopped has been sent for
	// the current response, so cancel and normal completion cannot both send it.
	audioStopped bool
	// commitWatchdog bounds how long a committed turn may go without a final
	// transcript. Guarded by mu.
	commitWatchdog *time.Timer
	// clientCommits counts input_audio_buffer.commit events still waiting for
	// their transcript. A client that committed is owed an answer even when
	// the turn held no words -- it has nothing else to wait on -- whereas a
	// turn the server ended itself and found empty is not worth an event: the
	// client would only have to work out that it means nothing. Guarded by mu.
	clientCommits int
	// speaking is set while synthesised audio is being delivered, so the
	// transport can hold the microphone: without echo cancellation the
	// caller's track carries the reply itself, and recognising that produces
	// turns that are the session's own words.
	speaking atomic.Bool
}

// transcriptCommitTimeout bounds the wait for a final transcript after a
// commit. Recognition can fail in ways that produce no event at all -- a worker
// that stops answering, a stream that dies mid-turn -- and the client is then
// left waiting on a future that will never resolve. Answering with a failure
// inside this window is what lets it fall back to something else while the
// caller is still in the conversation; the SDKs give up at thirty seconds, so
// arriving after that is the same as never arriving.
// It is a variable so tests can shorten the wait rather than sleep through it.
var transcriptCommitTimeout = 15 * time.Second

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

// Speaking reports whether a reply is currently being played to the caller.
func (s *Session) Speaking() bool { return s.speaking.Load() }

// TurnSilenceMS reports turn_detection.silence_duration_ms, zero when the
// session names none. It is read under the lock because session.update may
// rewrite the configuration while a transport is reading it.
func (s *Session) TurnSilenceMS() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Config.Audio == nil || s.Config.Audio.Input == nil || s.Config.Audio.Input.TurnDetection == nil {
		return 0
	}
	return s.Config.Audio.Input.TurnDetection.SilenceDurationMS
}

// SetResponder gives the session a way to write replies. ctx bounds the replies
// the session starts by itself when a turn ends; it should live as long as the
// transport. Without a responder a session that names a conversation model
// keeps the speech-to-speech behaviour and leaves the replies to the client.
func (s *Session) SetResponder(ctx context.Context, r Responder) {
	s.mu.Lock()
	s.responder = r
	s.ctx = ctx
	s.mu.Unlock()
}

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
		s.stopCommitWatchdog()
		owed := s.takeClientCommit()
		if strings.TrimSpace(ev.Text) == "" && !owed {
			// The server ended this turn itself and it held no words: a
			// breath, a click, the tail of a reply. Telling the client about
			// an utterance that was not one only invites it to answer it.
			return nil
		}
		if err := s.emit(ServerEvent{Type: ServerTranscriptCompleted, Transcript: ev.Text, ItemID: "item_" + randomID()}); err != nil {
			return err
		}
		s.turnEnded(ev.Text)
		return nil
	case "failed":
		s.stopCommitWatchdog()
		// This answers the commit, badly but finally: leaving it counted would
		// make the next empty turn the server ended itself look like one the
		// client is still waiting on.
		s.takeClientCommit()
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

// Commit ends the current turn on the client's behalf: input_audio_buffer.commit.
// The transcript that follows is always reported, empty or not.
func (s *Session) Commit() error {
	if s.transcriber != nil {
		// Only a commit that something will transcribe is owed an answer;
		// counting one that nothing will would leave the count standing for
		// the rest of the session.
		s.mu.Lock()
		s.clientCommits++
		s.mu.Unlock()
	}
	return s.commit()
}

// CommitTurn ends the current turn because the server heard it end. A turn
// that then turns out to hold no words is dropped rather than reported.
func (s *Session) CommitTurn() error { return s.commit() }

func (s *Session) commit() error {
	if s.transcriber != nil {
		if err := s.transcriber.Commit(); err != nil {
			return err
		}
		s.armCommitWatchdog()
	}
	return s.emit(ServerEvent{Type: ServerInputAudioCommitted})
}

// armCommitWatchdog starts the wait for this turn's final transcript, replacing
// any wait already running: a second commit supersedes the first.
func (s *Session) armCommitWatchdog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commitWatchdog != nil {
		s.commitWatchdog.Stop()
	}
	s.commitWatchdog = time.AfterFunc(transcriptCommitTimeout, func() {
		s.mu.Lock()
		s.commitWatchdog = nil
		s.mu.Unlock()
		s.takeClientCommit()
		_ = s.emit(ServerEvent{
			Type: ServerTranscriptFailed,
			Error: &EventError{
				Type:    "server_error",
				Code:    "transcription_timeout",
				Message: fmt.Sprintf("no transcript within %s of the commit", transcriptCommitTimeout),
			},
		})
	})
}

// takeClientCommit reports whether a client commit is still waiting for its
// transcript, and consumes it. Every path that answers a commit -- a final
// transcript, a failure, the watchdog -- goes through here, so the count
// follows the commits that are actually outstanding.
func (s *Session) takeClientCommit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clientCommits == 0 {
		return false
	}
	s.clientCommits--
	return true
}

// stopCommitWatchdog cancels the wait because the turn has been answered.
func (s *Session) stopCommitWatchdog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commitWatchdog != nil {
		s.commitWatchdog.Stop()
		s.commitWatchdog = nil
	}
}

// ClearInput discards buffered caller audio.
func (s *Session) ClearInput() error {
	if s.transcriber != nil {
		if err := s.transcriber.Reset(); err != nil {
			return err
		}
		// The turn being waited on has been thrown away, so there is nothing
		// left to report a timeout about.
		s.stopCommitWatchdog()
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

// Respond speaks text the client supplied and streams it to the client. It
// returns once the audio has been delivered or the response was cancelled.
//
// response.cancel and output_audio_buffer.clear are deliberately different:
// cancel stops generation and reports the response as cancelled, while clear
// only discards audio still queued on the server. Both end up here because both
// have to stop the synthesis that is feeding the queue.
func (s *Session) Respond(ctx context.Context, text, voice string) error {
	if text == "" {
		s.EmitError("empty_response", "response.create needs text to speak", "")
		return nil
	}
	return s.respond(ctx, voice, func(_ context.Context, _ string, say func(string) error) error {
		return say(text)
	})
}

// RespondWithModel writes the next reply with the session's conversation model
// and speaks it as it is written. The model and the synthesiser run at the same
// time: each sentence goes to synthesis as soon as it is complete, so the
// caller hears the opening clause while the rest is still being generated.
func (s *Session) RespondWithModel(ctx context.Context) error {
	s.mu.Lock()
	responder := s.responder
	model := strings.TrimSpace(s.Config.Model)
	voice := s.Config.Voice()
	history := s.prompt()
	s.mu.Unlock()
	if responder == nil || model == "" {
		s.EmitError("no_conversation_model", "this session has no conversation model to generate a reply with", "")
		return nil
	}

	var written strings.Builder
	return s.respond(ctx, voice, func(ctx context.Context, responseID string, say func(string) error) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Synthesis runs on its own goroutine so the model is never waiting on
		// it. The channel is what lets the two overlap; its depth only bounds
		// how far the model may run ahead of the voice.
		sentences := make(chan string, 16)
		var speakErr error
		var spoken sync.WaitGroup
		spoken.Add(1)
		go func() {
			defer spoken.Done()
			for text := range sentences {
				if speakErr != nil {
					continue // drain so the producer is never stuck on a full channel
				}
				if err := say(text); err != nil {
					speakErr = err
					cancel()
				}
			}
		}()

		splitter := &sentenceSplitter{}
		queue := func(text string) error {
			select {
			case sentences <- text:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		err := responder.Reply(ctx, model, history, func(piece string) error {
			written.WriteString(piece)
			if err := s.emit(ServerEvent{Type: ServerAudioTranscriptDelta, ResponseID: responseID, Delta: piece}); err != nil {
				return err
			}
			for _, sentence := range splitter.push(piece) {
				if err := queue(sentence); err != nil {
					return err
				}
			}
			return nil
		})
		if err == nil {
			if tail := splitter.flush(); tail != "" {
				err = queue(tail)
			}
		}
		close(sentences)
		spoken.Wait()
		if err == nil {
			err = speakErr
		}
		if err != nil {
			return err
		}
		reply := written.String()
		s.remember("assistant", reply)
		return s.emit(ServerEvent{Type: ServerAudioTranscriptDone, ResponseID: responseID, Transcript: reply})
	})
}

// respond runs one response: it announces it, runs generate -- which speaks by
// calling say, once or many times -- and reports how it ended. Respond and
// RespondWithModel share it, so cancel and the lifecycle events behave the same
// whichever way the text arrived.
func (s *Session) respond(ctx context.Context, voice string, generate func(ctx context.Context, responseID string, say func(text string) error) error) error {
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

	// say is only ever called from one goroutine at a time, so these need no
	// lock; generate returning is what publishes them to this one.
	started := false
	rate := 0
	say := func(text string) error {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		r, err := s.synth.Speak(responseCtx, model, text, voice, func(pcm []byte) error {
			if !started {
				started = true
				s.speaking.Store(true)
				if emitErr := s.emit(ServerEvent{Type: ServerOutputAudioStarted, ResponseID: responseID}); emitErr != nil {
					return emitErr
				}
			}
			return s.sendAudio(responseID, pcm)
		})
		if r > 0 {
			rate = r
		}
		return err
	}
	err := generate(responseCtx, responseID, say)

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

// turnEnded records what the caller said and, when the session writes its own
// replies, starts one. The reply is skipped when the client asked to create
// responses itself with turn_detection.create_response: false.
func (s *Session) turnEnded(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.mu.Lock()
	ctx, responder := s.ctx, s.responder
	generates := s.pipeline == PipelineASRLLMTTS && responder != nil && ctx != nil
	create := s.Config.createsResponses()
	s.mu.Unlock()
	if !generates {
		return
	}
	s.remember("user", text)
	if !create {
		return
	}
	// Errors are already reported to the client as events by respond; there is
	// nobody else to tell.
	go func() { _ = s.RespondWithModel(ctx) }()
}

// AddUserText records a user message the client sent as text
// (conversation.item.create), so the next model reply sees it. Nothing is
// generated until response.create asks for it.
//
// A session with no conversation model has nowhere to put the item and drops
// it in silence. That is deliberate: the OpenAI clients send conversation
// items as a matter of course, and answering one with an error event would
// surface a fault to the user where the session is working exactly as it is
// configured to.
func (s *Session) AddUserText(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.mu.Lock()
	generates := s.pipeline == PipelineASRLLMTTS && s.responder != nil
	s.mu.Unlock()
	if !generates {
		return
	}
	s.remember("user", text)
}

// remember appends one turn to the conversation, dropping the oldest past
// historyLimit.
func (s *Session) remember(role, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	s.mu.Lock()
	s.history = append(s.history, Message{Role: role, Content: content})
	if extra := len(s.history) - historyLimit; extra > 0 {
		s.history = append([]Message(nil), s.history[extra:]...)
	}
	s.mu.Unlock()
}

// prompt is the conversation as the model should see it: the session's
// instructions, then the turns so far. Callers hold s.mu.
func (s *Session) prompt() []Message {
	out := make([]Message, 0, len(s.history)+1)
	if instructions := strings.TrimSpace(s.Config.Instructions); instructions != "" {
		out = append(out, Message{Role: "system", Content: instructions})
	}
	return append(out, s.history...)
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
	s.speaking.Store(false)
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
	s.stopCommitWatchdog()
	s.mu.Lock()
	transcriber := s.transcriber
	s.transcriber = nil
	s.mu.Unlock()
	if transcriber != nil {
		_ = transcriber.Close()
	}
}
