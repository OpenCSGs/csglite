package realtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu       sync.Mutex
	events   []ServerEvent
	audio    int
	discards int
}

func (r *recorder) Send(ev ServerEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return nil
}

func (r *recorder) SendAudio(pcm []byte, _ int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audio += len(pcm)
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

func (b *blockingSynth) Speak(ctx context.Context, _, _ string, onAudio func([]byte) error) (int, error) {
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

func (instantSynth) Speak(_ context.Context, _, _ string, onAudio func([]byte) error) (int, error) {
	return 24000, onAudio([]byte{9, 9})
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
