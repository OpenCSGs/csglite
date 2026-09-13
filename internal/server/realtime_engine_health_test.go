package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

// probeASREngine stands in for a worker whose liveness can be asked about.
type probeASREngine struct {
	health error
	closed bool
}

func (e *probeASREngine) Transcribe(context.Context, api.OpenAIAudioTranscriptionRequest) (*api.OpenAIAudioTranscriptionResponse, error) {
	return nil, errors.New("not used")
}

func (e *probeASREngine) TranscribeStream(context.Context, api.OpenAIAudioTranscriptionRequest, func(api.OpenAIAudioTranscriptionResponse) error) error {
	return errors.New("not used")
}

func (e *probeASREngine) Health(context.Context) error { return e.health }
func (e *probeASREngine) Close() error                 { e.closed = true; return nil }
func (e *probeASREngine) ModelName() string            { return "test-asr" }

func newSpeechTestServer(t *testing.T) *Server {
	t.Helper()
	return newTestServerWithConfig(t, &config.Config{
		ModelDir:   config.ModelDirForStorage(t.TempDir()),
		DatasetDir: config.DatasetDirForStorage(t.TempDir()),
	})
}

// A cached worker that answers is reused: the probe must not cost a reload.
func TestCachedASREngineIsReusedWhenHealthy(t *testing.T) {
	s := newSpeechTestServer(t)
	modelID := "local-asr"
	engine := &probeASREngine{}
	s.asrEngines[modelID] = &managedASREngine{engine: engine, keepAlive: DefaultSpeechKeepAlive}

	got, err := s.getOrLoadASREngine(context.Background(), modelID)
	if err != nil {
		t.Fatalf("getOrLoadASREngine: %v", err)
	}
	if got != engine {
		t.Fatal("expected the cached engine to be reused")
	}
	if engine.closed {
		t.Fatal("a healthy engine was closed")
	}
}

// A worker still bound to its port but no longer answering was handed to every
// session that followed, which is how one wedged process took recognition down
// for hours. It has to leave the cache so the next session loads a fresh one.
func TestUnhealthyCachedASREngineIsDropped(t *testing.T) {
	s := newSpeechTestServer(t)
	modelID := "local-asr"
	engine := &probeASREngine{health: errors.New("i/o timeout")}
	s.asrEngines[modelID] = &managedASREngine{engine: engine, keepAlive: DefaultSpeechKeepAlive}

	// The model is not installed, so the reload that follows fails; what
	// matters here is that the broken engine was not handed back.
	if got, err := s.getOrLoadASREngine(context.Background(), modelID); err == nil && got == engine {
		t.Fatal("expected the unresponsive engine not to be reused")
	}
	if _, ok := s.asrEngines[modelID]; ok {
		t.Fatal("expected the unresponsive engine to be removed from the cache")
	}
	if !engine.closed {
		t.Fatal("expected the unresponsive engine's worker to be shut down")
	}
}

// dropASREngine must not close an engine that has already been replaced, or a
// second caller racing on the same broken worker kills the healthy successor.
func TestDropASREngineLeavesAReplacementAlone(t *testing.T) {
	s := newSpeechTestServer(t)
	modelID := "local-asr"
	stale := &probeASREngine{}
	fresh := &probeASREngine{}
	staleEntry := &managedASREngine{engine: stale}
	s.asrEngines[modelID] = &managedASREngine{engine: fresh}

	s.dropASREngine(modelID, staleEntry)

	if fresh.closed {
		t.Fatal("the replacement engine was closed")
	}
	if _, ok := s.asrEngines[modelID]; !ok {
		t.Fatal("the replacement engine was removed from the cache")
	}
}

// A call outlasts the idle window, and evicting mid-call kills the worker under
// a live stream: recognition stops for the rest of the call without saying so.
func TestIdleEvictionSkipsRetainedSpeechEngines(t *testing.T) {
	s := newSpeechTestServer(t)
	modelID := "local-asr"
	engine := &probeASREngine{}
	s.asrEngines[modelID] = &managedASREngine{
		engine:    engine,
		lastUsed:  time.Now().Add(-time.Hour),
		keepAlive: time.Minute,
	}

	release := s.retainASREngine(modelID)
	s.evictExpired(time.Now())
	if _, ok := s.asrEngines[modelID]; !ok {
		t.Fatal("an engine held by a live session was evicted")
	}
	if engine.closed {
		t.Fatal("an engine held by a live session had its worker killed")
	}

	// Once released it ages out normally again.
	release()
	s.mu.Lock()
	s.asrEngines[modelID].lastUsed = time.Now().Add(-time.Hour)
	s.mu.Unlock()
	s.evictExpired(time.Now())
	if _, ok := s.asrEngines[modelID]; ok {
		t.Fatal("expected the released engine to be evicted once idle")
	}
}

// Releasing twice must not drive the count negative and leave the engine
// pinned in memory for the life of the process.
func TestReleasingASREngineTwiceIsSafe(t *testing.T) {
	s := newSpeechTestServer(t)
	modelID := "local-asr"
	s.asrEngines[modelID] = &managedASREngine{engine: &probeASREngine{}}

	release := s.retainASREngine(modelID)
	release()
	release()

	s.mu.RLock()
	active := s.asrEngines[modelID].activeRequests
	s.mu.RUnlock()
	if active != 0 {
		t.Fatalf("activeRequests = %d, want 0", active)
	}
}

// Retaining a model with no cached engine must not panic; the engine can be
// dropped between the load and the retain.
func TestRetainingAnAbsentEngineIsSafe(t *testing.T) {
	s := newSpeechTestServer(t)
	release := s.retainASREngine("never-loaded")
	release()
}

// The voice list is fixed for a model, so a second caller must not pay the
// eight-second cold load a dropdown does not need.
func TestTTSVoicesAreServedFromCache(t *testing.T) {
	s := newSpeechTestServer(t)
	modelID := "modelscope/Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice"
	s.rememberTTSVoices(modelID, &api.SpeechVoicesResponse{
		Model:  modelID,
		Voices: []api.SpeechVoice{{ID: "Serena"}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/tts-voices?model="+modelID, nil)
	w := httptest.NewRecorder()
	s.handleTTSVoices(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "Serena") {
		t.Fatalf("expected the cached voice list, got %s", body)
	}
	if _, ok := s.ttsEngines[modelID]; ok {
		t.Fatal("serving a cached voice list loaded the model")
	}
}

// When the engine never arrives, audio must stop accumulating: the session
// otherwise holds thirty seconds of PCM for a call that will never transcribe
// it, and replays it into nothing.
func TestDeferredTranscriberStopsBufferingOnceTheEngineFails(t *testing.T) {
	d := &deferredTranscriber{}
	if err := d.Write(make([]byte, 1024)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(d.pending) == 0 {
		t.Fatal("audio was not buffered while the engine loaded")
	}

	d.fail()
	if len(d.pending) != 0 {
		t.Fatal("buffered audio was kept after the engine failed")
	}
	if err := d.Write(make([]byte, 1024)); err != nil {
		t.Fatalf("Write after fail: %v", err)
	}
	if len(d.pending) != 0 {
		t.Fatal("audio is still being buffered for an engine that will never arrive")
	}
}

// A commit that arrived before the engine did must still be finalised once it
// attaches, or the first turn of every call is lost.
func TestDeferredTranscriberReplaysAPendingCommit(t *testing.T) {
	d := &deferredTranscriber{}
	if err := d.Write([]byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := d.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	engine := &recordingTranscriber{}
	if !d.attach(engine) {
		t.Fatal("attach reported a closed session")
	}
	if engine.written != 4 {
		t.Fatalf("replayed %d bytes, want 4", engine.written)
	}
	if engine.commits != 1 {
		t.Fatalf("replayed %d commits, want 1", engine.commits)
	}
}

type recordingTranscriber struct {
	written int
	commits int
}

func (r *recordingTranscriber) Write(pcm []byte) error { r.written += len(pcm); return nil }
func (r *recordingTranscriber) Commit() error          { r.commits++; return nil }
func (r *recordingTranscriber) Reset() error           { return nil }
func (r *recordingTranscriber) Close() error           { return nil }
