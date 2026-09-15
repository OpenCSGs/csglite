package realtime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// scriptedResponder plays back pieces of text. gate, when set, is waited on
// before the pieces after the first: it stands for a model that is still
// writing while the first sentence is already being spoken.
type scriptedResponder struct {
	pieces []string
	gate   chan struct{}
	err    error

	mu      sync.Mutex
	history [][]Message
}

func (r *scriptedResponder) Reply(ctx context.Context, _ string, history []Message, onText func(string) error) error {
	r.mu.Lock()
	r.history = append(r.history, append([]Message(nil), history...))
	r.mu.Unlock()
	for i, piece := range r.pieces {
		if i == 1 && r.gate != nil {
			select {
			case <-r.gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := onText(piece); err != nil {
			return err
		}
	}
	return r.err
}

// waitForEvent polls until the recorder has seen kind: replies run on their
// own goroutine, so the test has to wait for them.
func waitForEvent(t *testing.T, rec *recorder, kind string) {
	t.Helper()
	waitFor(t, func() bool { return rec.count(kind) > 0 })
}

func TestSentenceSplitterReleasesSentences(t *testing.T) {
	p := &sentenceSplitter{}
	var got []string
	for _, piece := range []string{"好的，我来", "看看。这个功能", "已经实现了！", "圆周率是3.14。", "Sure. Done"} {
		got = append(got, p.push(piece)...)
	}
	got = append(got, p.flush())
	want := []string{"好的，", "我来看看。", "这个功能已经实现了！", "圆周率是3.14。", "Sure.", "Done"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pieces = %q, want %q", got, want)
	}
}

func TestSentenceSplitterBreaksAtAClauseOnlyForTheOpening(t *testing.T) {
	p := &sentenceSplitter{}
	got := p.push("第一句还没完，但是先说。然后这一句，有个逗号，不该切。")
	want := []string{"第一句还没完，", "但是先说。", "然后这一句，有个逗号，不该切。"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pieces = %q, want %q", got, want)
	}
}

func TestSentenceSplitterBoundsUnpunctuatedText(t *testing.T) {
	p := &sentenceSplitter{}
	got := p.push(strings.Repeat("字", sentenceMax+5))
	if len(got) != 1 || len([]rune(got[0])) != sentenceMax {
		t.Fatalf("got %d pieces of %d runes, want one of %d", len(got), len([]rune(strings.Join(got, ""))), sentenceMax)
	}
}

func TestFinishedTurnIsAnsweredByTheModel(t *testing.T) {
	cfg := SessionConfig{Model: "chat", Instructions: "简短回答"}
	s, rec := newTestSession(t, cfg, instantSynth{})
	responder := &scriptedResponder{pieces: []string{"好的，", "已经", "帮你查到了。"}}
	s.SetResponder(context.Background(), responder)

	if err := s.HandleTranscript(TranscriptEvent{Kind: "completed", Text: "帮我查一下"}); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, rec, ServerResponseDone)

	if rec.count(ServerResponseCreated) != 1 || rec.audio == 0 {
		t.Fatalf("no spoken reply; events: %v", rec.types())
	}
	var transcript string
	for _, ev := range rec.events {
		if ev.Type == ServerAudioTranscriptDone {
			transcript = ev.Transcript
		}
	}
	if transcript != "好的，已经帮你查到了。" {
		t.Fatalf("spoken transcript = %q", transcript)
	}
	if n := rec.count(ServerAudioTranscriptDelta); n != 3 {
		t.Fatalf("%d transcript deltas, want one per piece (3)", n)
	}

	// The model saw the instructions and the caller's words, and the reply is
	// kept for the next turn.
	want := []Message{{Role: "system", Content: "简短回答"}, {Role: "user", Content: "帮我查一下"}}
	if len(responder.history) != 1 || !reflect.DeepEqual(responder.history[0], want) {
		t.Fatalf("model was given %+v, want %+v", responder.history, want)
	}
	s.mu.Lock()
	history := append([]Message(nil), s.history...)
	s.mu.Unlock()
	wantHistory := []Message{{Role: "user", Content: "帮我查一下"}, {Role: "assistant", Content: "好的，已经帮你查到了。"}}
	if !reflect.DeepEqual(history, wantHistory) {
		t.Fatalf("history = %+v, want %+v", history, wantHistory)
	}
}

// The reason the model runs in the session: speech starts on the first
// sentence while the model is still writing. The responder here will not
// release its second piece until audio for the first has been heard, so a
// session that waited for the whole reply before speaking would deadlock and
// the test would time out.
func TestModelReplyIsSpokenWhileStillBeingWritten(t *testing.T) {
	s, rec := newTestSession(t, SessionConfig{Model: "chat"}, instantSynth{})
	gate := make(chan struct{})
	responder := &scriptedResponder{pieces: []string{"第一句说完了。", "第二句。"}, gate: gate}
	s.SetResponder(context.Background(), responder)

	if err := s.HandleTranscript(TranscriptEvent{Kind: "completed", Text: "你好"}); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, rec, ServerOutputAudioStarted)
	close(gate)
	waitForEvent(t, rec, ServerResponseDone)

	last := rec.events[len(rec.events)-1]
	if last.Type != ServerResponseDone || last.Status != "completed" {
		t.Fatalf("final event = %s/%s, want response.done/completed", last.Type, last.Status)
	}
}

func TestClientMayKeepCreatingResponsesItself(t *testing.T) {
	no := false
	cfg := SessionConfig{Model: "chat", Audio: &SessionAudio{Input: &SessionAudioInput{
		TurnDetection: &TurnDetection{Type: "server_vad", CreateResponse: &no},
	}}}
	s, rec := newTestSession(t, cfg, instantSynth{})
	responder := &scriptedResponder{pieces: []string{"不该自动说这个。"}}
	s.SetResponder(context.Background(), responder)

	if err := s.HandleTranscript(TranscriptEvent{Kind: "completed", Text: "你好"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if rec.count(ServerResponseCreated) != 0 {
		t.Fatalf("a response was created although create_response is false; events: %v", rec.types())
	}

	// The turn is still remembered, so a reply the client asks for later has
	// the context.
	if err := s.RespondWithModel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(responder.history) != 1 || len(responder.history[0]) != 1 || responder.history[0][0].Content != "你好" {
		t.Fatalf("model was given %+v, want the caller's turn", responder.history)
	}
}

func TestSpeechToSpeechSessionLeavesRepliesToTheClient(t *testing.T) {
	// No conversation model: what the caller says is transcribed and nothing
	// more, as before. The client writes the reply and sends response.create.
	s, rec := newTestSession(t, SessionConfig{}, instantSynth{})
	s.SetResponder(context.Background(), &scriptedResponder{pieces: []string{"不该说"}})

	if err := s.HandleTranscript(TranscriptEvent{Kind: "completed", Text: "你好"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if rec.count(ServerResponseCreated) != 0 {
		t.Fatalf("a speech-to-speech session invented a reply; events: %v", rec.types())
	}
	if err := s.RespondWithModel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.count(ServerError) != 1 {
		t.Fatalf("asking a model-less session for a model reply should be an error; events: %v", rec.types())
	}
}

func TestModelFailureEndsTheResponseAsFailed(t *testing.T) {
	s, rec := newTestSession(t, SessionConfig{Model: "chat"}, instantSynth{})
	s.SetResponder(context.Background(), &scriptedResponder{pieces: []string{"开头，"}, err: errors.New("upstream 502")})

	if err := s.RespondWithModel(context.Background()); err != nil {
		t.Fatal(err)
	}
	last := rec.events[len(rec.events)-1]
	if last.Type != ServerResponseDone || last.Status != "failed" {
		t.Fatalf("final event = %s/%s, want response.done/failed", last.Type, last.Status)
	}
	if n := rec.count(ServerOutputAudioStopped); n != 1 {
		t.Fatalf("output_audio_buffer.stopped sent %d times, want exactly 1", n)
	}
}

func TestHistoryIsBounded(t *testing.T) {
	s, _ := newTestSession(t, SessionConfig{Model: "chat"}, instantSynth{})
	for i := 0; i < historyLimit+7; i++ {
		s.remember("user", strings.Repeat("a", i+1))
	}
	s.mu.Lock()
	n := len(s.history)
	first := s.history[0].Content
	s.mu.Unlock()
	if n != historyLimit || len(first) != 8 {
		t.Fatalf("kept %d turns starting at %d chars, want %d starting at 8", n, len(first), historyLimit)
	}
}

// A turn the server ended and found empty is not reported: the client would
// only answer it. A turn the client committed is always answered, since it has
// nothing else to wait on.
func TestEmptyTurnIsReportedOnlyWhenTheClientCommittedIt(t *testing.T) {
	s, rec := newTestSession(t, SessionConfig{}, instantSynth{})
	// A commit only means anything when something is recognising the audio.
	s.transcriber = &stubTranscriber{}

	if err := s.CommitTurn(); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleTranscript(TranscriptEvent{Kind: "completed", Text: "  "}); err != nil {
		t.Fatal(err)
	}
	if n := rec.count(ServerTranscriptCompleted); n != 0 {
		t.Fatalf("an empty server-ended turn was reported %d time(s)", n)
	}

	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleTranscript(TranscriptEvent{Kind: "completed", Text: ""}); err != nil {
		t.Fatal(err)
	}
	if n := rec.count(ServerTranscriptCompleted); n != 1 {
		t.Fatalf("a client commit got %d completed events, want exactly 1", n)
	}

	// Words are always reported, whoever ended the turn.
	if err := s.CommitTurn(); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleTranscript(TranscriptEvent{Kind: "completed", Text: "你好"}); err != nil {
		t.Fatal(err)
	}
	if n := rec.count(ServerTranscriptCompleted); n != 2 {
		t.Fatalf("a spoken server-ended turn was not reported (%d completed events)", n)
	}
}

func TestSpeakingFollowsTheReplyAudio(t *testing.T) {
	s, rec := newTestSession(t, SessionConfig{}, &blockingSynth{started: make(chan struct{}, 1)})
	if s.Speaking() {
		t.Fatal("speaking before any reply")
	}
	done := make(chan error, 1)
	go func() { done <- s.Respond(context.Background(), "hello", "") }()
	waitForEvent(t, rec, ServerOutputAudioStarted)
	if !s.Speaking() {
		t.Fatal("not speaking while audio is being delivered")
	}
	if err := s.CancelResponse(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.Speaking() {
		t.Fatal("still speaking after the reply stopped")
	}
}

func TestTypedUserMessageJoinsTheConversation(t *testing.T) {
	s, rec := newTestSession(t, SessionConfig{Model: "chat"}, instantSynth{})
	responder := &scriptedResponder{pieces: []string{"收到。"}}
	s.SetResponder(context.Background(), responder)

	s.AddUserText("  帮我查天气 ")
	if err := s.RespondWithModel(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []Message{{Role: "user", Content: "帮我查天气"}}
	if len(responder.history) != 1 || !reflect.DeepEqual(responder.history[0], want) {
		t.Fatalf("model was given %+v, want %+v", responder.history, want)
	}
	if rec.count(ServerError) != 0 {
		t.Fatalf("unexpected errors: %v", rec.types())
	}

	// A session with no conversation model has nowhere to put the item. It is
	// dropped without an error: the OpenAI clients send conversation items as a
	// matter of course, and faulting one would report a problem that is not one.
	plain, plainRec := newTestSession(t, SessionConfig{}, instantSynth{})
	plain.AddUserText("你好")
	if plainRec.count(ServerError) != 0 {
		t.Fatalf("a conversation item on a speech-to-speech session raised an error: %v", plainRec.types())
	}
}

// A commit whose transcript never arrives must not leave the session believing
// a client is still waiting: the next turn the server ends itself would then be
// reported empty, which is the event this whole mechanism exists to suppress.
func TestAnAnsweredCommitIsNotCountedTwice(t *testing.T) {
	for _, answer := range []struct {
		name string
		emit func(*Session) error
	}{
		{"failed transcript", func(s *Session) error {
			return s.HandleTranscript(TranscriptEvent{Kind: "failed", Error: errors.New("worker died")})
		}},
		{"empty transcript", func(s *Session) error {
			return s.HandleTranscript(TranscriptEvent{Kind: "completed", Text: ""})
		}},
	} {
		t.Run(answer.name, func(t *testing.T) {
			s, rec := newTestSession(t, SessionConfig{}, instantSynth{})
			s.transcriber = &stubTranscriber{}
			if err := s.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := answer.emit(s); err != nil {
				t.Fatal(err)
			}
			before := rec.count(ServerTranscriptCompleted)

			// A later turn the server ended itself, with nothing in it.
			if err := s.CommitTurn(); err != nil {
				t.Fatal(err)
			}
			if err := s.HandleTranscript(TranscriptEvent{Kind: "completed", Text: ""}); err != nil {
				t.Fatal(err)
			}
			if got := rec.count(ServerTranscriptCompleted); got != before {
				t.Fatalf("the commit was answered twice: %d completed events, want %d", got, before)
			}
		})
	}
}

// With nothing to transcribe a commit is answered by no one, so it must not be
// counted as outstanding either.
func TestACommitWithoutRecognitionIsNotCounted(t *testing.T) {
	s, rec := newTestSession(t, SessionConfig{}, instantSynth{})
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitTurn(); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleTranscript(TranscriptEvent{Kind: "completed", Text: ""}); err != nil {
		t.Fatal(err)
	}
	if got := rec.count(ServerTranscriptCompleted); got != 0 {
		t.Fatalf("%d completed events for a session with no recognition, want 0", got)
	}
}
