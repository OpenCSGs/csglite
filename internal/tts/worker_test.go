package tts

import (
	"regexp"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/model"
)

// A worker that dies during startup must report why: the reason it prints is
// something the caller has to act on, such as a model that emits audio codec
// tokens but ships no decoder.
func TestTailBufferLastError(t *testing.T) {
	cases := map[string]struct {
		written string
		want    string
	}{
		"python traceback": {
			written: "Traceback (most recent call last):\n" +
				"  File \"/tmp/tts_worker.py\", line 300, in load_engine\n" +
				"    raise RuntimeError(\n" +
				"RuntimeError: model x generates audio codec tokens but ships no codec decoder\n",
			want: "model x generates audio codec tokens but ships no codec decoder",
		},
		"missing module": {
			written: "ModuleNotFoundError: No module named 'ordered_set'\n",
			want:    "No module named 'ordered_set'",
		},
		"plain line": {
			written: "could not open model directory\n",
			want:    "could not open model directory",
		},
		"empty": {written: "", want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			buf := newTailBuffer(1 << 10)
			if _, err := buf.Write([]byte(tc.written)); err != nil {
				t.Fatal(err)
			}
			if got := buf.lastError(); got != tc.want {
				t.Fatalf("lastError() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The buffer must stay bounded: a chatty worker can print megabytes.
func TestTailBufferIsBounded(t *testing.T) {
	buf := newTailBuffer(64)
	for i := 0; i < 100; i++ {
		if _, err := buf.Write([]byte("0123456789")); err != nil {
			t.Fatal(err)
		}
	}
	buf.mu.Lock()
	size := len(buf.buf)
	buf.mu.Unlock()
	if size > 64 {
		t.Fatalf("buffer grew to %d bytes, want at most 64", size)
	}
}

// A fault the worker blames on the request must stay a 4xx so the caller sees
// its own mistake rather than a server error.
func TestWorkerErrorClassifiesFaults(t *testing.T) {
	cases := []struct {
		status      int
		body        string
		wantMessage string
		wantClient  bool
	}{
		{400, `{"error":"unknown voice: NoSuchVoice (available: Vivian, Ryan)"}`,
			"unknown voice: NoSuchVoice (available: Vivian, Ryan)", true},
		{400, `{"error":"input is required"}`, "input is required", true},
		{500, `{"error":"ffmpeg failed"}`, "ffmpeg failed", false},
		{503, `not json at all`, "not json at all", false},
	}
	for _, tc := range cases {
		err := workerError(tc.status, []byte(tc.body))
		if err.Message != tc.wantMessage {
			t.Errorf("status %d: message = %q, want %q", tc.status, err.Message, tc.wantMessage)
		}
		if err.ClientFault() != tc.wantClient {
			t.Errorf("status %d: ClientFault() = %v, want %v", tc.status, err.ClientFault(), tc.wantClient)
		}
	}
}

// model.TTSBackendFor decides what the library advertises; load_engine in
// tts_worker.py decides what actually runs. They must stay in step, so assert
// every architecture the worker dispatches on is one the Go side recognises.
func TestWorkerArchitecturesMatchGoDetection(t *testing.T) {
	script := string(ttsWorkerScript)
	start := strings.Index(script, "def _transformers_tts_class")
	if start < 0 {
		t.Fatal("_transformers_tts_class not found in the embedded worker")
	}
	end := strings.Index(script[start:], "\ndef ")
	if end < 0 {
		t.Fatal("could not find the end of _transformers_tts_class")
	}
	body := script[start : start+end]

	markers := regexp.MustCompile(`\("([A-Za-z0-9]+)",\s*"[A-Za-z0-9]+"\)`).FindAllStringSubmatch(body, -1)
	if len(markers) == 0 {
		t.Fatal("no architecture markers parsed from the worker")
	}
	for _, m := range markers {
		marker := m[1]
		// The worker matches on a substring, so probe with the marker itself.
		if !model.IsTTSArchitecture(marker) {
			t.Errorf("worker dispatches on architecture %q but model.IsTTSArchitecture(%q) is false; the two lists have drifted", marker, marker)
		}
	}
	t.Logf("checked %d architecture markers", len(markers))
}

// Every backend the worker dispatches on must be a backend name the Go side can
// report, otherwise the library and the runtime disagree about what is
// supported.
func TestWorkerBackendNamesMatchGoConstants(t *testing.T) {
	script := string(ttsWorkerScript)
	names := regexp.MustCompile(`(?m)^\s+backend = "([a-z0-9-]+)"`).FindAllStringSubmatch(script, -1)
	if len(names) == 0 {
		t.Fatal("no backend names parsed from the embedded worker")
	}
	known := map[string]bool{
		model.TTSBackendQwen3:        true,
		model.TTSBackendKokoro:       true,
		model.TTSBackendTransformers: true,
		model.TTSBackendVoxCPM:       true,
	}
	for _, m := range names {
		if !known[m[1]] {
			t.Errorf("worker declares backend %q with no matching Go constant", m[1])
		}
	}
	t.Logf("checked %d worker backends", len(names))
}

// The streaming path splits the text at sentence boundaries so the first packet
// costs one sentence rather than the whole clip. The embedded worker carries
// that logic, so assert the pieces it depends on are present: losing either the
// splitter or the concurrent feed silently restores the old behaviour, where the
// first byte arrived no earlier than the last.
func TestWorkerStreamsSentenceBySentence(t *testing.T) {
	script := string(ttsWorkerScript)
	for _, needle := range []string{
		"_split_for_streaming",
		// the feed thread is what lets encoded bytes leave while synthesis runs
		"threading.Thread(target=feed",
		// read1 returns on first available data where read would block
		"proc.stdout.read1(",
	} {
		if !strings.Contains(script, needle) {
			t.Errorf("embedded worker no longer contains %q; streaming would serialise again", needle)
		}
	}
	// The splitter must run on the streaming path only: a plain request keeps
	// whole-text synthesis, which preserves intonation across sentences.
	streamIdx := strings.Index(script, "async def speak_stream")
	plainIdx := strings.Index(script, "async def speak(")
	splitIdx := strings.LastIndex(script, "_split_for_streaming(text)")
	if streamIdx < 0 || plainIdx < 0 || splitIdx < 0 {
		t.Fatal("could not locate the speak handlers or the split call")
	}
	if splitIdx < streamIdx {
		t.Error("_split_for_streaming is called outside speak_stream; a plain request must synthesise the text whole")
	}
}

// pythonBlock returns the source of the def whose header line contains marker,
// up to the next definition at the same indentation.
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
		trimmed := strings.TrimSpace(line)
		if lineIndent <= indent && (strings.Contains(line, "def ") || strings.HasPrefix(trimmed, "@") || strings.HasPrefix(trimmed, "class ")) {
			return strings.Join(lines[start:i], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}

// Synthesis of a paragraph runs for seconds. Running it on the worker's single
// event loop makes /health and any streaming response a realtime session
// depends on unreachable for that whole time, which is how a Web UI test
// request could stall the call happening alongside it.
func TestSpeakRunsOffTheEventLoop(t *testing.T) {
	body := pythonBlock(t, string(ttsWorkerScript), "async def speak(")
	if !strings.Contains(body, "asyncio.to_thread(_synthesize_locked") {
		t.Error("/speak generates audio on the event loop")
	}
	if strings.Contains(body, "ENGINE.iter_pcm(") {
		t.Error("/speak calls the engine directly rather than through a worker thread")
	}
}

// Time to first audio is the cost of synthesising the opening chunk, so that
// chunk has its own ceiling: an opening sentence the caller hears as several
// seconds of silence is the complaint the report is about.
func TestStreamingShortensTheOpeningChunk(t *testing.T) {
	script := string(ttsWorkerScript)
	if !strings.Contains(script, "_STREAM_FIRST_CHUNK_MAX") {
		t.Fatal("no ceiling on the opening chunk")
	}
	if !strings.Contains(pythonBlock(t, script, "def _split_for_streaming"), "_shorten_first_chunk(") {
		t.Error("the splitter does not shorten the opening chunk")
	}
	// Cutting mid-phrase to save a second would be audible, so only clause
	// boundaries may be used.
	shorten := pythonBlock(t, script, "def _shorten_first_chunk")
	if !strings.Contains(shorten, `"，,、;；"`) {
		t.Error("the opening chunk is cut somewhere other than a clause boundary")
	}
	if !strings.Contains(shorten, "min_len") {
		t.Error("the opening chunk may be cut into a fragment shorter than a phrase")
	}
}

// Reporting ready before the first generation has run puts several seconds of
// one-off cost into the first thing a caller hears.
func TestTTSWorkerWarmsUpBeforeReportingReady(t *testing.T) {
	main := pythonBlock(t, string(ttsWorkerScript), "def main()")
	warm := strings.Index(main, "_warm_up(ENGINE)")
	ready := strings.Index(main, "TTS worker ready")
	if warm < 0 || ready < 0 || warm > ready {
		t.Error("the worker reports ready before it has warmed up")
	}
	if !strings.Contains(main, "pid=") {
		t.Error("the ready line does not name the worker's pid")
	}
}

// Without the generated audio's duration next to the time it took, there is no
// way to tell a slow model from a long piece of text -- which is exactly why
// the report could not be quantified from the server log.
func TestSynthesisIsLoggedWithEnoughToComputeRealTimeFactor(t *testing.T) {
	body := pythonBlock(t, string(ttsWorkerScript), "def _log_synthesis")
	for _, field := range []string{"chars=", "audio=", "generate=", "rtf="} {
		if !strings.Contains(body, field) {
			t.Errorf("synthesis log is missing %s", field)
		}
	}
	if !strings.Contains(pythonBlock(t, string(ttsWorkerScript), "def _log_ttfb"), "ttfb=") {
		t.Error("the streaming path does not report time to first audio")
	}
}
