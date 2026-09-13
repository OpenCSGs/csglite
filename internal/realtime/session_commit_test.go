package realtime

import (
	"testing"
	"time"
)

// stubTranscriber accepts everything and never reports a transcript, which is
// how a wedged recognition worker behaves from the session's point of view.
type stubTranscriber struct {
	commits int
	resets  int
}

func (t *stubTranscriber) Write([]byte) error { return nil }
func (t *stubTranscriber) Commit() error      { t.commits++; return nil }
func (t *stubTranscriber) Reset() error       { t.resets++; return nil }
func (t *stubTranscriber) Close() error       { return nil }

func withShortCommitTimeout(t *testing.T) {
	t.Helper()
	previous := transcriptCommitTimeout
	transcriptCommitTimeout = 30 * time.Millisecond
	t.Cleanup(func() { transcriptCommitTimeout = previous })
}

func newCommitSession(t *testing.T) (*Session, *recorder, *stubTranscriber) {
	t.Helper()
	rec := &recorder{}
	transcriber := &stubTranscriber{}
	session, err := NewSession(SessionConfig{}, rec, transcriber, nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session, rec, transcriber
}

// A commit that never produces a transcript must still answer the client. It
// is the case the report was about: recognition went quiet and the caller had
// nothing to wait on but its own thirty-second timeout.
func TestCommitWithoutTranscriptReportsFailure(t *testing.T) {
	withShortCommitTimeout(t)
	session, rec, _ := newCommitSession(t)

	if err := session.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := rec.count(ServerTranscriptFailed); got != 0 {
		t.Fatalf("failure reported before the timeout elapsed: %d", got)
	}

	waitFor(t, func() bool { return rec.count(ServerTranscriptFailed) == 1 })

	for _, ev := range rec.events {
		if ev.Type != ServerTranscriptFailed {
			continue
		}
		if ev.Error == nil || ev.Error.Code != "transcription_timeout" {
			t.Fatalf("expected a transcription_timeout error, got %+v", ev.Error)
		}
	}
}

// The watchdog exists only for turns that go unanswered; a turn that completes
// normally must not also be reported as failed.
func TestCompletedTranscriptCancelsTheWatchdog(t *testing.T) {
	withShortCommitTimeout(t)
	session, rec, _ := newCommitSession(t)

	if err := session.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := session.HandleTranscript(TranscriptEvent{Kind: "completed", Text: "hello"}); err != nil {
		t.Fatalf("HandleTranscript: %v", err)
	}

	time.Sleep(4 * transcriptCommitTimeout)
	if got := rec.count(ServerTranscriptFailed); got != 0 {
		t.Fatalf("a completed turn was also reported as failed %d time(s)", got)
	}
	if got := rec.count(ServerTranscriptCompleted); got != 1 {
		t.Fatalf("expected one completed event, got %d", got)
	}
}

// An empty transcript is an answer too: silence is a legitimate result and the
// client is waiting for it, so it must stop the watchdog like any other.
func TestEmptyTranscriptCountsAsAnAnswer(t *testing.T) {
	withShortCommitTimeout(t)
	session, rec, _ := newCommitSession(t)

	if err := session.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := session.HandleTranscript(TranscriptEvent{Kind: "completed"}); err != nil {
		t.Fatalf("HandleTranscript: %v", err)
	}

	time.Sleep(4 * transcriptCommitTimeout)
	if got := rec.count(ServerTranscriptFailed); got != 0 {
		t.Fatalf("an empty transcript was reported as a failure %d time(s)", got)
	}
}

// Clearing the input throws the turn away, so there is no longer anything for
// the watchdog to report on.
func TestClearInputCancelsTheWatchdog(t *testing.T) {
	withShortCommitTimeout(t)
	session, rec, transcriber := newCommitSession(t)

	if err := session.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := session.ClearInput(); err != nil {
		t.Fatalf("ClearInput: %v", err)
	}
	if transcriber.resets != 1 {
		t.Fatalf("expected the transcriber to be reset once, got %d", transcriber.resets)
	}

	time.Sleep(4 * transcriptCommitTimeout)
	if got := rec.count(ServerTranscriptFailed); got != 0 {
		t.Fatalf("a cleared turn was reported as failed %d time(s)", got)
	}
}

// Closing the session must not leave a timer that reports on a conversation
// nobody is in any more.
func TestCloseCancelsTheWatchdog(t *testing.T) {
	withShortCommitTimeout(t)
	session, rec, _ := newCommitSession(t)

	if err := session.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	session.Close()

	time.Sleep(4 * transcriptCommitTimeout)
	if got := rec.count(ServerTranscriptFailed); got != 0 {
		t.Fatalf("a closed session reported a failure %d time(s)", got)
	}
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition was not met before the deadline")
}
