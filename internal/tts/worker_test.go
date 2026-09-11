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
