package server

import (
	"sync"
	"testing"
)

// fakeTranscriber records what the deferred transcriber replays into it.
type fakeTranscriber struct {
	mu      sync.Mutex
	written []byte
	commits int
	resets  int
	closed  bool
}

func (f *fakeTranscriber) Write(pcm []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.written = append(f.written, pcm...)
	return nil
}

func (f *fakeTranscriber) Commit() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commits++
	return nil
}

func (f *fakeTranscriber) Reset() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resets++
	return nil
}

func (f *fakeTranscriber) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// A caller who starts speaking before the recognition engine has loaded must
// not lose that speech: loading a cold model takes seconds, and the first words
// are usually the important ones.
func TestDeferredTranscriberReplaysBufferedAudio(t *testing.T) {
	deferred := &deferredTranscriber{}
	if err := deferred.Write([]byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := deferred.Commit(); err != nil {
		t.Fatal(err)
	}

	engine := &fakeTranscriber{}
	if !deferred.attach(engine) {
		t.Fatal("attach reported a closed session")
	}
	engine.mu.Lock()
	written, commits := string(engine.written), engine.commits
	engine.mu.Unlock()
	if written != "\x01\x02\x03\x04" {
		t.Fatalf("engine received %q, want the buffered audio", written)
	}
	if commits != 1 {
		t.Fatalf("commits = %d, want the buffered commit to be replayed", commits)
	}

	// Once attached, audio goes straight through.
	if err := deferred.Write([]byte{5}); err != nil {
		t.Fatal(err)
	}
	engine.mu.Lock()
	written = string(engine.written)
	engine.mu.Unlock()
	if written != "\x01\x02\x03\x04\x05" {
		t.Fatalf("engine received %q after attach", written)
	}
}

// A client that never stops talking must not grow the buffer without bound
// while the engine loads.
func TestDeferredTranscriberCapsTheBuffer(t *testing.T) {
	deferred := &deferredTranscriber{}
	chunk := make([]byte, 64*1024)
	for i := 0; i < 40; i++ {
		if err := deferred.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if len(deferred.pending) > deferredTranscriberBufferBytes {
		t.Fatalf("buffered %d bytes, want at most %d", len(deferred.pending), deferredTranscriberBufferBytes)
	}
	if deferred.dropped == 0 {
		t.Fatal("nothing was reported as dropped, so the cap did not engage")
	}
}

// input_audio_buffer.clear before the engine is ready must discard the buffer
// rather than replay it later.
func TestDeferredTranscriberResetDiscardsBufferedAudio(t *testing.T) {
	deferred := &deferredTranscriber{}
	_ = deferred.Write([]byte{1, 2, 3, 4})
	_ = deferred.Commit()
	if err := deferred.Reset(); err != nil {
		t.Fatal(err)
	}

	engine := &fakeTranscriber{}
	deferred.attach(engine)
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if len(engine.written) != 0 {
		t.Fatalf("engine received %q, want nothing after a reset", engine.written)
	}
	if engine.commits != 0 {
		t.Fatalf("commits = %d, want the cleared commit to be dropped", engine.commits)
	}
}

// A session that ends while the engine is still loading must release the
// engine, not hand it audio.
func TestDeferredTranscriberAttachAfterCloseReleasesTheEngine(t *testing.T) {
	deferred := &deferredTranscriber{}
	_ = deferred.Write([]byte{1, 2})
	if err := deferred.Close(); err != nil {
		t.Fatal(err)
	}
	if deferred.attach(&fakeTranscriber{}) {
		t.Fatal("attach accepted an engine for a closed session")
	}
	// Writes after close are dropped rather than buffered forever.
	if err := deferred.Write([]byte{3}); err != nil {
		t.Fatal(err)
	}
	if len(deferred.pending) != 0 {
		t.Fatalf("pending = %d bytes after close, want 0", len(deferred.pending))
	}
}

// Close must reach the loaded engine, since it holds the worker session.
func TestDeferredTranscriberCloseClosesTheEngine(t *testing.T) {
	deferred := &deferredTranscriber{}
	engine := &fakeTranscriber{}
	deferred.attach(engine)
	if err := deferred.Close(); err != nil {
		t.Fatal(err)
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if !engine.closed {
		t.Fatal("the engine was not closed")
	}
}
