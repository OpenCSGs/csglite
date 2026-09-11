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
