package tts

import "testing"

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
