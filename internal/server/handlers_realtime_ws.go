package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/opencsgs/csglite/internal/asr"
	"github.com/opencsgs/csglite/internal/realtime"
	"github.com/opencsgs/csglite/internal/tts"
	"github.com/opencsgs/csglite/pkg/api"
)

// errRealtimeASRNotStreaming is returned when the recognition engine cannot
// accept a continuous stream. The session still runs so it can speak; only
// transcription is unavailable.
var errRealtimeASRNotStreaming = errors.New("this speech recognition engine cannot accept a continuous stream")

// errRealtimeTranscript wraps a transcript failure reported by the worker.
// errRealtimeNoSpeechModel is returned when a response has no model to speak
// with, which the session reports as no_speech_model.
var errRealtimeNoSpeechModel = errors.New("no text-to-speech model is configured for this session")

func errRealtimeTranscript(message string) error {
	if strings.TrimSpace(message) == "" {
		return errors.New("transcription failed")
	}
	return errors.New(message)
}

var realtimeUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 << 10,
	WriteBufferSize: 32 << 10,
	// The realtime endpoints are OpenAI-compatible and meant to be reachable
	// from a browser, so origin checking is left to the API auth middleware
	// rather than refusing cross-origin upgrades here.
	CheckOrigin: func(*http.Request) bool { return true },
}

// wsSender adapts a WebSocket to realtime.Sender. Writes are serialised because
// audio and events are produced concurrently and a gorilla connection allows
// only one writer at a time.
type wsSender struct {
	mu         sync.Mutex
	conn       *websocket.Conn
	sampleRate int
	// generation increments on every discard so audio produced before a
	// output_audio_buffer.clear is dropped rather than delivered late.
	generation uint64
}

func (w *wsSender) Send(ev realtime.ServerEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteJSON(ev)
}

func (w *wsSender) SendAudio(frame realtime.AudioFrame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	rate := frame.SampleRate
	if rate == 0 {
		rate = w.sampleRate
	}
	// On this transport audio travels as an event; WebRTC writes it to the media
	// track instead. The payload goes in "delta", which is where the protocol
	// puts it and where the SDKs look for it.
	ev := frame.Event
	ev.Delta = base64.StdEncoding.EncodeToString(frame.PCM)
	ev.SampleRate = rate
	return w.conn.WriteJSON(ev)
}

// DiscardPendingAudio satisfies the optional interface the session uses for
// output_audio_buffer.clear. Audio is written straight to the socket here, so
// there is no server-side queue to drop; the generation counter exists so a
// later transport with buffering can honour the same contract.
func (w *wsSender) DiscardPendingAudio() {
	w.mu.Lock()
	w.generation++
	w.mu.Unlock()
}

// GET /v1/realtime -- OpenAI-compatible realtime session over a WebSocket.
func (s *Server) handleRealtimeWebSocket(w http.ResponseWriter, r *http.Request) {
	cfg, err := realtimeSessionFromRequest(r)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if err := s.admitRealtimeSession(); err != nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "server_error", err.Error())
		return
	}
	conn, err := realtimeUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade has already written a response.
		log.Printf("REALTIME: websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()
	s.addRealtimeSocket(1)
	defer s.addRealtimeSocket(-1)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sender := &wsSender{conn: conn, sampleRate: realtime.DefaultOutputSampleRate}
	synth := s.newRealtimeSynthesizer()
	// Recognition loads in the background: a client waits for session.created
	// before it speaks, so blocking on a cold model here would stall the whole
	// session. Audio that arrives meanwhile is buffered.
	transcriber, startTranscription := s.newRealtimePipeline(ctx, cfg)

	session, err := realtime.NewSession(cfg, sender, transcriber, synth)
	if err != nil {
		log.Printf("REALTIME: session setup failed: %v", err)
		return
	}
	defer session.Close()
	if startTranscription != nil {
		startTranscription(session)
	}

	s.runRealtimeLoop(ctx, conn, session)
}

// runRealtimeLoop reads client events until the connection ends.
func (s *Server) runRealtimeLoop(ctx context.Context, conn *websocket.Conn, session *realtime.Session) {
	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType == websocket.BinaryMessage {
			// Some clients push raw PCM frames instead of wrapping them in
			// input_audio_buffer.append; accept both.
			if err := session.HandleAudio(payload); err != nil {
				session.EmitError("audio_write_failed", err.Error(), "")
			}
			continue
		}
		var ev realtime.ClientEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			session.EmitError("invalid_event", "event is not valid JSON", "")
			continue
		}
		if err := s.dispatchRealtimeEvent(ctx, session, ev); err != nil {
			return
		}
	}
}

func (s *Server) dispatchRealtimeEvent(ctx context.Context, session *realtime.Session, ev realtime.ClientEvent) error {
	switch ev.Type {
	case realtime.ClientSessionUpdate:
		if err := session.UpdateConfig(ev.Session); err != nil {
			session.EmitError("invalid_session", err.Error(), ev.EventID)
		}
	case realtime.ClientInputAudioAppend:
		pcm, err := base64.StdEncoding.DecodeString(ev.Audio)
		if err != nil {
			session.EmitError("invalid_audio", "audio must be base64-encoded PCM16", ev.EventID)
			return nil
		}
		if err := session.HandleAudio(pcm); err != nil {
			session.EmitError("audio_write_failed", err.Error(), ev.EventID)
		}
	case realtime.ClientInputAudioCommit:
		if err := session.Commit(); err != nil {
			session.EmitError("commit_failed", err.Error(), ev.EventID)
		}
	case realtime.ClientInputAudioClear:
		if err := session.ClearInput(); err != nil {
			session.EmitError("clear_failed", err.Error(), ev.EventID)
		}
	case realtime.ClientResponseCreate:
		text, voice := realtimeResponseText(ev.Response, session)
		// Synthesis runs in its own goroutine so cancel and further audio can be
		// processed while it speaks.
		go func() {
			if err := session.Respond(ctx, text, voice); err != nil {
				log.Printf("REALTIME: respond failed: %v", err)
			}
		}()
	case realtime.ClientResponseCancel:
		if err := session.CancelResponse(); err != nil {
			session.EmitError("cancel_failed", err.Error(), ev.EventID)
		}
	case realtime.ClientOutputAudioClear:
		if err := session.ClearOutputAudio(); err != nil {
			session.EmitError("clear_failed", err.Error(), ev.EventID)
		}
	case realtime.ClientConversationItemAdd:
		// Accepted so a client can inject text, but nothing is generated until
		// response.create asks for it.
	default:
		session.EmitError("unknown_event", "unsupported event type: "+ev.Type, ev.EventID)
	}
	return nil
}

// realtimeResponseText extracts what to speak from a response.create. A session
// without a conversation model speaks the text it is given, which is how the
// speech-to-speech pipeline works: the caller's own model decides what to say.
func realtimeResponseText(raw json.RawMessage, session *realtime.Session) (string, string) {
	voice := session.Config.Voice()
	if len(raw) == 0 {
		return "", voice
	}
	var body struct {
		Instructions string `json:"instructions"`
		Text         string `json:"text"`
		Audio        *struct {
			Voice string `json:"voice"`
		} `json:"audio"`
		Input []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return "", voice
	}
	if body.Audio != nil && strings.TrimSpace(body.Audio.Voice) != "" {
		voice = strings.TrimSpace(body.Audio.Voice)
	}
	if text := strings.TrimSpace(body.Text); text != "" {
		return text, voice
	}
	// Prefer the last text content of the newest input item, which is where a
	// client that follows the OpenAI shape puts the utterance.
	for i := len(body.Input) - 1; i >= 0; i-- {
		for j := len(body.Input[i].Content) - 1; j >= 0; j-- {
			if text := strings.TrimSpace(body.Input[i].Content[j].Text); text != "" {
				return text, voice
			}
		}
	}
	return strings.TrimSpace(body.Instructions), voice
}

// realtimeSessionFromRequest builds the initial session from the query string,
// which is how a browser configures a session it cannot send headers with.
func realtimeSessionFromRequest(r *http.Request) (realtime.SessionConfig, error) {
	var cfg realtime.SessionConfig
	query := r.URL.Query()
	if raw := strings.TrimSpace(query.Get("session")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return cfg, err
		}
	}
	if model := strings.TrimSpace(query.Get("model")); model != "" {
		// A model given in the query names the ASR model for a transcription
		// session and the conversation model otherwise, matching how clients in
		// the issue address the endpoint.
		if strings.EqualFold(cfg.Type, "transcription") || strings.EqualFold(query.Get("intent"), "transcription") {
			cfg.Type = "transcription"
			if cfg.Audio == nil {
				cfg.Audio = &realtime.SessionAudio{}
			}
			if cfg.Audio.Input == nil {
				cfg.Audio.Input = &realtime.SessionAudioInput{}
			}
			if cfg.Audio.Input.Transcription == nil {
				cfg.Audio.Input.Transcription = &realtime.Transcription{}
			}
			if cfg.Audio.Input.Transcription.Model == "" {
				cfg.Audio.Input.Transcription.Model = model
			}
		} else if cfg.TranscriptionModel() == "" && cfg.Model == "" {
			// With no other hint, treat it as the recognition model: the report's
			// clients connect with ?model=<asr model>.
			if cfg.Audio == nil {
				cfg.Audio = &realtime.SessionAudio{}
			}
			if cfg.Audio.Input == nil {
				cfg.Audio.Input = &realtime.SessionAudioInput{}
			}
			cfg.Audio.Input.Transcription = &realtime.Transcription{Model: model}
		}
	}
	return cfg, nil
}

// newRealtimeTranscriber opens a streaming recognition session and returns a
// channel of transcript events.
func (s *Server) newRealtimeTranscriber(ctx context.Context, cfg realtime.SessionConfig) (realtime.Transcriber, <-chan realtime.TranscriptEvent, error) {
	model := cfg.TranscriptionModel()
	if model == "" {
		// Clients commonly name only one half of the pipeline, so the
		// configured default fills in the other.
		model = strings.TrimSpace(s.cfg.Realtime.DefaultASRModel)
	}
	if model == "" {
		return nil, nil, nil
	}
	engine, err := s.getOrLoadASREngine(ctx, model)
	if err != nil {
		return nil, nil, err
	}
	streaming, ok := engine.(asr.StreamingEngine)
	if !ok {
		return nil, nil, errRealtimeASRNotStreaming
	}
	rate := realtime.DefaultInputSampleRate
	if cfg.Audio != nil && cfg.Audio.Input != nil && cfg.Audio.Input.Format != nil && cfg.Audio.Input.Format.Rate > 0 {
		rate = cfg.Audio.Input.Format.Rate
	}
	request := api.OpenAIAudioTranscriptionRequest{Model: model, ResponseFormat: "json"}
	if t := cfg.TranscriptionOptions(); t != nil {
		request.Language = t.Language
		request.Prompt = t.Prompt
		request.Hotwords = t.Hotwords
		request.ITN = t.ITN
	}
	stream, err := streaming.OpenLive(ctx, asr.LiveConfig{SampleRate: rate, Request: request})
	if err != nil {
		return nil, nil, err
	}
	events := make(chan realtime.TranscriptEvent, 32)
	go func() {
		defer close(events)
		for ev := range stream.Events() {
			switch ev.Kind {
			case "ready", "committed", "cleared":
				// Acknowledgements; the session reports these itself.
			case "failed":
				events <- realtime.TranscriptEvent{Kind: "failed", Error: errRealtimeTranscript(ev.Error)}
			default:
				events <- realtime.TranscriptEvent{Kind: ev.Kind, Text: ev.Text}
			}
		}
	}()
	s.touchASREngine(model)
	return stream, events, nil
}

// newRealtimeSynthesizer adapts the text-to-speech engine to the session's
// Synthesizer interface. It resolves the model per response so a session.update
// that names one takes effect, falling back to the configured default.
func (s *Server) newRealtimeSynthesizer() realtime.Synthesizer {
	return &realtimeSynth{server: s}
}

type realtimeSynth struct {
	server *Server
}

// resolveModel picks the model for this response: the session's, else the
// server-wide default.
func (r *realtimeSynth) resolveModel(model string) string {
	if trimmed := strings.TrimSpace(model); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(r.server.cfg.Realtime.DefaultTTSModel)
}

func (r *realtimeSynth) CanSpeak(model string) bool {
	return r.resolveModel(model) != ""
}

func (r *realtimeSynth) Speak(ctx context.Context, model, text, voice string, onAudio func([]byte) error) (int, error) {
	model = r.resolveModel(model)
	if model == "" {
		return 0, errRealtimeNoSpeechModel
	}
	engine, err := r.server.getOrLoadTTSEngine(ctx, model)
	if err != nil {
		return 0, err
	}
	rate := realtime.DefaultOutputSampleRate
	if info, infoErr := engine.Info(ctx); infoErr == nil && info.SampleRate > 0 {
		rate = info.SampleRate
	}
	req := api.OpenAIAudioSpeechRequest{
		Model: model,
		Input: text,
		Voice: voice,
		// PCM keeps the transport free of container framing, which matters when
		// the audio is going onto a media track or into a base64 event.
		ResponseFormat: "pcm",
		SampleRate:     rate,
		Stream:         true,
	}
	err = engine.SpeakStream(ctx, req, func(chunk tts.Chunk) error {
		if len(chunk.Data) == 0 {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return onAudio(chunk.Data)
	})
	r.server.touchTTSEngine(model)
	return rate, err
}
