package asr

import (
	"strings"
	"testing"
)

// pythonBlock returns the source of the def whose header line contains marker,
// up to the next line at the same indentation that starts a new definition or
// decorator. It is enough to reason about one function of the embedded worker
// without shipping a Python parser.
func pythonBlock(t *testing.T, script, marker string) string {
	t.Helper()
	lines := strings.Split(script, "\n")
	start := -1
	indent := 0
	for i, line := range lines {
		if strings.Contains(line, marker) && strings.Contains(line, "def ") {
			start = i
			indent = len(line) - len(strings.TrimLeft(line, " "))
			break
		}
	}
	if start < 0 {
		t.Fatalf("no definition containing %q in the embedded worker", marker)
	}
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		lineIndent := len(line) - len(strings.TrimLeft(line, " "))
		if lineIndent <= indent && (strings.Contains(line, "def ") || strings.HasPrefix(strings.TrimSpace(line), "@") || strings.HasPrefix(strings.TrimSpace(line), "class ")) {
			return strings.Join(lines[start:i], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}

// The worker serves one asyncio loop. Running a transcription on it holds the
// loop for as long as inference takes, which is what left /health unanswered
// and made a second session's WebSocket handshake time out while the first
// session was still talking. Every call that runs a model must therefore go
// through a worker thread.
func TestLiveSessionRunsInferenceOffTheEventLoop(t *testing.T) {
	body := pythonBlock(t, string(asrWorkerScript), "async def transcribe_live")
	for _, call := range []string{"session.finalize", "session.partial", "session.vad_events"} {
		if !strings.Contains(body, "asyncio.to_thread("+call) {
			t.Errorf("%s is never handed to asyncio.to_thread in the live handler", call)
		}
		if strings.Contains(body, call+"(") {
			t.Errorf("%s is called directly on the event loop; it must go through asyncio.to_thread", call)
		}
	}
}

// The single-clip endpoints share the loop with every live session, so they
// must be off it too.
func TestFileTranscriptionRunsOffTheEventLoop(t *testing.T) {
	body := pythonBlock(t, string(asrWorkerScript), "async def transcribe(")
	if !strings.Contains(body, "asyncio.to_thread(_transcribe_locked") {
		t.Error("/transcribe runs the model on the event loop")
	}
	if strings.Contains(body, "ENGINE.transcribe(") {
		t.Error("/transcribe calls the engine directly rather than through a worker thread")
	}
}

// A commit is a question the caller is waiting on. Reporting only `committed`
// leaves silence and a wedged worker indistinguishable, and the client can do
// nothing but time out -- the symptom the report opens with.
func TestCommittedTurnAlwaysReportsATerminalEvent(t *testing.T) {
	body := pythonBlock(t, string(asrWorkerScript), "async def finalize_turn")
	if !strings.Contains(body, `if text or committed:`) {
		t.Error("a committed turn with no text does not emit a completed event")
	}
	if !strings.Contains(body, `{"kind": "failed"`) {
		t.Error("a turn whose transcription raised does not emit a failed event")
	}
	if !strings.Contains(body, `{"kind": "committed"}`) {
		t.Error("the commit acknowledgement is no longer sent")
	}
}

// Partials re-transcribe audio, so their cost has to be bounded twice over: by
// how much audio one partial covers, and by how much audio a turn may hold.
// Without both, a long turn made every partial dearer than the last until the
// worker could no longer keep up with the stream feeding it.
func TestPartialsAreBoundedInSizeAndRate(t *testing.T) {
	script := string(asrWorkerScript)
	body := pythonBlock(t, script, "def partial(self, req)")
	if !strings.Contains(body, "partial_window_seconds") {
		t.Error("a partial still transcribes the whole buffer rather than its tail")
	}
	if !strings.Contains(pythonBlock(t, script, "def append(self, pcm)"), "max_buffer_seconds") {
		t.Error("the turn buffer has no upper bound")
	}
	pump := pythonBlock(t, script, "async def pump_loop")
	if !strings.Contains(pump, "last_partial_end = time.monotonic()") {
		t.Error("partials are not paced from the end of the previous one")
	}
	// A session whose caller has gone away keeps its socket open with a
	// non-empty buffer, and would otherwise re-transcribe the same seconds for
	// as long as the zombie lasted.
	if !strings.Contains(pump, "revision != last_partial_revision") {
		t.Error("a partial can re-run over audio that has not changed")
	}
}

// Reporting ready before the first inference has run hands the caller a worker
// whose next request costs seconds more than every one after it.
func TestWorkerWarmsUpBeforeReportingReady(t *testing.T) {
	script := string(asrWorkerScript)
	main := pythonBlock(t, script, "def main()")
	warm := strings.Index(main, "_warm_up(ENGINE)")
	ready := strings.Index(main, "ASR worker ready")
	if warm < 0 || ready < 0 || warm > ready {
		t.Error("the worker reports ready before it has warmed up")
	}
	if !strings.Contains(main, "pid=") {
		t.Error("the ready line does not name the worker's pid, so a stuck worker cannot be traced back to a process")
	}
}
