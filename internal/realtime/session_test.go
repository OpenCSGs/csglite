package realtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu          sync.Mutex
	events      []ServerEvent
	audio       int
	audioEvents []ServerEvent
	discards    int
}

func (r *recorder) Send(ev ServerEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return nil
}

func (r *recorder) SendAudio(frame AudioFrame) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audio += len(frame.PCM)
	r.audioEvents = append(r.audioEvents, frame.Event)
	return nil
}

func (r *recorder) DiscardPendingAudio() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.discards++
}

func (r *recorder) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, ev := range r.events {
		out = append(out, ev.Type)
	}
	return out
}

func (r *recorder) count(kind string) int {
	n := 0
	for _, t := range r.types() {
		if t == kind {
			n++
		}
	}
	return n
}

// blockingSynth emits one chunk then waits for the context, standing in for a
// model that is still generating when a cancel arrives.
type blockingSynth struct{ started chan struct{} }

func (*blockingSynth) CanSpeak(string) bool { return true }

func (b *blockingSynth) Speak(ctx context.Context, _, _, _ string, onAudio func([]byte) error) (int, error) {
	if err := onAudio([]byte{1, 2, 3, 4}); err != nil {
		return 0, err
	}
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return 24000, ctx.Err()
}

type instantSynth struct{}

func (instantSynth) CanSpeak(string) bool { return true }

func (instantSynth) Speak(_ context.Context, _, _, _ string, onAudio func([]byte) error) (int, error) {
	return 24000, onAudio([]byte{9, 9})
}

// modelSynth records the model each response asked for, so a session.update
// that names a different one can be seen taking effect.
type modelSynth struct {
	mu     sync.Mutex
	models []string
}

func (m *modelSynth) CanSpeak(model string) bool { return model != "" }

func (m *modelSynth) Speak(_ context.Context, model, _, _ string, onAudio func([]byte) error) (int, error) {
	m.mu.Lock()
	m.models = append(m.models, model)
	m.mu.Unlock()
	return 24000, onAudio([]byte{1, 1})
}

func newTestSession(t *testing.T, cfg SessionConfig, synth Synthesizer) (*Session, *recorder) {
	t.Helper()
	rec := &recorder{}
	s, err := NewSession(cfg, rec, nil, synth)
	if err != nil {
		t.Fatal(err)
	}
	return s, rec
}

func TestSessionCreatedCarriesIdentifiers(t *testing.T) {
	s, rec := newTestSession(t, SessionConfig{}, instantSynth{})
	if got := rec.types(); len(got) != 1 || got[0] != ServerSessionCreated {
		t.Fatalf("events = %v, want just session.created", got)
	}
	ev := rec.events[0]
	if ev.SessionID != s.ID || ev.EventID == "" || ev.Sequence != 1 || ev.Timestamp == 0 {
		t.Fatalf("session.created = %+v, want session id, event id, sequence and timestamp", ev)
	}
}

// A response that finishes normally reports completed, and output audio is
// stopped exactly once.
func TestRespondCompletes(t *testing.T) {
	s, rec := newTestSession(t, SessionConfig{}, instantSynth{})
	if err := s.Respond(context.Background(), "hello", ""); err != nil {
		t.Fatal(err)
	}
	if rec.audio == 0 {
		t.Error("no audio was delivered")
	}
	if n := rec.count(ServerOutputAudioStopped); n != 1 {
		t.Errorf("output_audio_buffer.stopped sent %d times, want exactly 1", n)
	}
	last := rec.events[len(rec.events)-1]
	if last.Type != ServerResponseDone || last.Status != "completed" {
		t.Errorf("final event = %s/%s, want response.done/completed", last.Type, last.Status)
	}
}

// response.cancel must stop generation and report cancelled, not completed.
func TestCancelResponseStopsGeneration(t *testing.T) {
	synth := &blockingSynth{started: make(chan struct{}, 1)}
	s, rec := newTestSession(t, SessionConfig{}, synth)

	done := make(chan error, 1)
	go func() { done <- s.Respond(context.Background(), "speak", "") }()
	select {
	case <-synth.started:
	case <-time.After(2 * time.Second):
		t.Fatal("synthesis never started")
	}
	if err := s.CancelResponse(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Respond returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Respond did not return after cancel")
	}

	last := rec.events[len(rec.events)-1]
	if last.Type != ServerResponseDone || last.Status != "cancelled" {
		t.Errorf("final event = %s/%s, want response.done/cancelled", last.Type, last.Status)
	}
	if n := rec.count(ServerOutputAudioStopped); n != 1 {
		t.Errorf("output_audio_buffer.stopped sent %d times, want exactly 1", n)
	}
	if rec.count(ServerError) != 0 {
		t.Error("cancelling an active response must not raise an error event")
	}
}

// output_audio_buffer.clear drops queued audio and is distinct from cancel: it
// reports cleared rather than a cancelled response.
func TestClearOutputAudioDiscardsQueue(t *testing.T) {
	synth := &blockingSynth{started: make(chan struct{}, 1)}
	s, rec := newTestSession(t, SessionConfig{}, synth)
	go func() { _ = s.Respond(context.Background(), "speak", "") }()
	select {
	case <-synth.started:
	case <-time.After(2 * time.Second):
		t.Fatal("synthesis never started")
	}
	if err := s.ClearOutputAudio(); err != nil {
		t.Fatal(err)
	}
	if rec.discards != 1 {
		t.Errorf("DiscardPendingAudio called %d times, want 1", rec.discards)
	}
	if rec.count(ServerOutputAudioCleared) != 1 {
		t.Errorf("events = %v, want one output_audio_buffer.cleared", rec.types())
	}
}

// Cancelling with nothing running tells the client so, since a client that
// cancels on a keypress cannot know whether a response was active.
func TestCancelWithoutActiveResponseReportsError(t *testing.T) {
	s, rec := newTestSession(t, SessionConfig{}, instantSynth{})
	if err := s.CancelResponse(); err != nil {
		t.Fatal(err)
	}
	if rec.count(ServerError) != 1 {
		t.Errorf("events = %v, want one error", rec.types())
	}
}

// A transcription session recognises speech and refuses to speak.
func TestTranscriptionSessionRefusesToSpeak(t *testing.T) {
	s, rec := newTestSession(t, SessionConfig{Type: "transcription"}, instantSynth{})
	if s.Pipeline() != PipelineASROnly {
		t.Fatalf("pipeline = %q, want %q", s.Pipeline(), PipelineASROnly)
	}
	if err := s.Respond(context.Background(), "hello", ""); err != nil {
		t.Fatal(err)
	}
	if rec.audio != 0 {
		t.Error("a transcription session must not produce audio")
	}
	if rec.count(ServerError) != 1 {
		t.Errorf("events = %v, want one error", rec.types())
	}
}

// An empty model selects the speech-to-speech pipeline, where the server speaks
// only what the client asks for instead of inventing replies.
func TestPipelineFromConfig(t *testing.T) {
	cases := map[string]struct {
		cfg  SessionConfig
		want Pipeline
	}{
		"no model":      {SessionConfig{}, PipelineASRTTS},
		"with model":    {SessionConfig{Model: "Qwen/Qwen3-4B"}, PipelineASRLLMTTS},
		"transcription": {SessionConfig{Type: "transcription"}, PipelineASROnly},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := tc.cfg
			if got := cfg.Normalize(); got != tc.want {
				t.Fatalf("Normalize() = %q, want %q", got, tc.want)
			}
		})
	}
}

// session.update is a patch: changing the voice must not clear the rest.
func TestUpdateConfigIsAPatch(t *testing.T) {
	cfg := SessionConfig{
		Model: "Qwen/Qwen3-4B",
		Audio: &SessionAudio{Output: &SessionAudioOutput{Model: "acme/tts", Voice: "Vivian"}},
	}
	s, rec := newTestSession(t, cfg, instantSynth{})
	patch, _ := json.Marshal(map[string]any{
		"audio": map[string]any{"output": map[string]any{"voice": "Serena", "model": "acme/tts"}},
	})
	if err := s.UpdateConfig(patch); err != nil {
		t.Fatal(err)
	}
	if got := s.Config.Voice(); got != "Serena" {
		t.Errorf("voice = %q, want Serena", got)
	}
	if got := s.Config.Model; got != "Qwen/Qwen3-4B" {
		t.Errorf("model = %q, want it preserved by the patch", got)
	}
	if rec.count(ServerSessionUpdated) != 1 {
		t.Errorf("events = %v, want one session.updated", rec.types())
	}
}

// Every event must carry a strictly increasing sequence so a client can order
// them.
func TestSequenceIncreases(t *testing.T) {
	s, rec := newTestSession(t, SessionConfig{}, instantSynth{})
	if err := s.Respond(context.Background(), "hello", ""); err != nil {
		t.Fatal(err)
	}
	var previous int64
	for _, ev := range rec.events {
		if ev.Sequence <= previous {
			t.Fatalf("sequence %d followed %d; must strictly increase", ev.Sequence, previous)
		}
		previous = ev.Sequence
	}
}

// The speech model is normally configured with session.update after the
// session exists -- that is how the OpenAI clients do it -- so the model has to
// be read per response. Binding it when the session was created meant such a
// client could never speak: it got no_speech_model however it configured the
// session.
func TestSpeechModelComesFromTheCurrentConfig(t *testing.T) {
	synth := &modelSynth{}
	s, rec := newTestSession(t, SessionConfig{}, synth)

	// Nothing is configured yet, so a response has nowhere to go.
	if err := s.Respond(context.Background(), "hello", ""); err != nil {
		t.Fatal(err)
	}
	if rec.count(ServerError) != 1 {
		t.Fatalf("events = %v, want an error before a model is configured", rec.types())
	}

	if err := s.UpdateConfig([]byte(`{"audio":{"output":{"model":"acme/voice"}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Respond(context.Background(), "hello", ""); err != nil {
		t.Fatal(err)
	}
	synth.mu.Lock()
	models := append([]string(nil), synth.models...)
	synth.mu.Unlock()
	if len(models) != 1 || models[0] != "acme/voice" {
		t.Fatalf("models = %v, want the model set by session.update", models)
	}

	// A later update switches it again, without a new session.
	if err := s.UpdateConfig([]byte(`{"audio":{"output":{"model":"acme/other"}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Respond(context.Background(), "hello", ""); err != nil {
		t.Fatal(err)
	}
	synth.mu.Lock()
	models = append([]string(nil), synth.models...)
	synth.mu.Unlock()
	if len(models) != 2 || models[1] != "acme/other" {
		t.Fatalf("models = %v, want the second update to take effect", models)
	}
}

// Audio deltas are ordinary events: a client orders them against transcripts
// and lifecycle events by sequence, and acknowledges them by event id. Sending
// them unstamped -- which is what happens when a transport builds the event
// itself -- leaves the client unable to do either.
func TestAudioDeltasCarryIdentifiersAndOrdering(t *testing.T) {
	s, rec := newTestSession(t, SessionConfig{Audio: &SessionAudio{
		Output: &SessionAudioOutput{Model: "acme/voice"},
	}}, instantSynth{})
	if err := s.Respond(context.Background(), "hello", ""); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.audioEvents) == 0 {
		t.Fatal("no audio was delivered")
	}
	for i, ev := range rec.audioEvents {
		if ev.Type != ServerAudioDelta {
			t.Fatalf("audio event %d has type %q", i, ev.Type)
		}
		if ev.EventID == "" || ev.SessionID != s.ID {
			t.Fatalf("audio event %d = %+v, want an event id and the session id", i, ev)
		}
		if ev.Sequence == 0 {
			t.Fatalf("audio event %d has no sequence number", i)
		}
		if ev.ResponseID == "" {
			t.Fatalf("audio event %d does not name its response", i)
		}
	}
	// The sequence has to be shared with the other events, or a client cannot
	// tell whether audio came before or after a transcript.
	last := rec.audioEvents[len(rec.audioEvents)-1].Sequence
	for _, ev := range rec.events {
		if ev.Type == ServerResponseDone && ev.Sequence < last {
			t.Fatal("response.done was numbered before the audio it follows")
		}
	}
}

// A standard client reads the response id and status from the response object,
// not from the top-level fields, so a lifecycle event without it reads as
// statusless however the response actually ended.
func TestResponseLifecycleEventsCarryTheResponseObject(t *testing.T) {
	s, rec := newTestSession(t, SessionConfig{Audio: &SessionAudio{
		Output: &SessionAudioOutput{Model: "acme/voice"},
	}}, instantSynth{})
	if err := s.Respond(context.Background(), "hello", ""); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	seen := map[string]*ResponseInfo{}
	for _, ev := range rec.events {
		if ev.Type == ServerResponseCreated || ev.Type == ServerResponseDone {
			if ev.Response == nil {
				t.Fatalf("%s carries no response object", ev.Type)
			}
			if ev.Response.ID != ev.ResponseID || ev.Response.Object != "realtime.response" {
				t.Fatalf("%s response = %+v, want it to name the response", ev.Type, ev.Response)
			}
			seen[ev.Type] = ev.Response
		}
	}
	if got := seen[ServerResponseCreated]; got == nil || got.Status != "in_progress" {
		t.Fatalf("response.created status = %v, want in_progress", got)
	}
	if got := seen[ServerResponseDone]; got == nil || got.Status != "completed" {
		t.Fatalf("response.done status = %v, want completed", got)
	}
}
