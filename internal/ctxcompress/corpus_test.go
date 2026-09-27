package ctxcompress

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestCorpus replays captured traffic through both modes and writes the
// results next to the corpus for token counting. It runs only when
// CTXCOMPRESS_CORPUS_DIR names a directory holding corpus_blocks.jsonl
// ({"tool","text"} per line) and corpus_requests.jsonl ({"id","protocol",
// "body"} per line), such as one exported from the observability store.
func TestCorpus(t *testing.T) {
	dir := os.Getenv("CTXCOMPRESS_CORPUS_DIR")
	if dir == "" {
		t.Skip("CTXCOMPRESS_CORPUS_DIR not set")
	}
	modes := map[string]Options{"safe": {}, "aggressive": {Sample: true}}

	blocksOut, err := os.Create(filepath.Join(dir, "result_blocks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer blocksOut.Close()
	encoder := json.NewEncoder(blocksOut)
	forEachLine(t, filepath.Join(dir, "corpus_blocks.jsonl"), func(line []byte) {
		var block struct{ Tool, Text string }
		if err := json.Unmarshal(line, &block); err != nil {
			t.Fatal(err)
		}
		for mode, opts := range modes {
			out, kind := Text(block.Text, block.Tool, opts)
			if again, _ := Text(out, block.Tool, opts); again != out {
				t.Errorf("%s: not idempotent for a %s block from %s (%d → %d → %d bytes)", mode, kind, block.Tool, len(block.Text), len(out), len(again))
			}
			_ = encoder.Encode(map[string]any{"mode": mode, "tool": block.Tool, "kind": kind, "before": block.Text, "after": out})
		}
	})

	requestsOut, err := os.Create(filepath.Join(dir, "result_requests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer requestsOut.Close()
	encoder = json.NewEncoder(requestsOut)
	forEachLine(t, filepath.Join(dir, "corpus_requests.jsonl"), func(line []byte) {
		var request struct {
			ID       string
			Protocol Protocol
			Body     string
		}
		if err := json.Unmarshal(line, &request); err != nil {
			t.Fatal(err)
		}
		for mode, opts := range modes {
			result, err := CompressRequest(request.Protocol, []byte(request.Body), opts)
			if err != nil {
				t.Errorf("%s: %v", request.ID, err)
				continue
			}
			_ = encoder.Encode(map[string]any{"mode": mode, "id": request.ID, "protocol": request.Protocol, "after": string(result.Body), "stats": result.Stats})
		}
	})
}

func forEachLine(t *testing.T, path string, fn func([]byte)) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		fn(scanner.Bytes())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}
