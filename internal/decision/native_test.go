package decision

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeDecisionsTranslatesToSystemOne(t *testing.T) {
	prepared, err := PrepareNativeOpenAI(decodeOpenAIRequest(t, `{"model":"m","input":"Charged twice.","questions":[
	  {"type":"choice","name":"route","instructions":"Which team?","choices":[{"value":"billing","description":"payments"},{"value":"shipping"}]},
	  {"type":"predicate","name":"angry","instructions":"Is the customer angry?"},
	  {"type":"score","instructions":"How urgent?","levels":[{"label":"low"},{"label":"high","description":"today"}]},
	  {"type":"choice","instructions":"Refund?","choices":[{"value":true},{"value":false}]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(prepared.Body(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"state": "Charged twice.",
		"questions": map[string]any{
			"q000": map[string]any{"type": "choice", "instructions": "Which team?", "criteria": map[string]any{"billing": "payments", "shipping": nil}},
			"q001": map[string]any{"type": "noul", "instructions": "Is the customer angry?"},
			"q002": map[string]any{"type": "score", "instructions": "How urgent?", "criteria": []any{"low", "high: today"}},
			"q003": map[string]any{"type": "choice", "instructions": "Refund?", "criteria": map[string]any{"true": nil, "false": nil}},
		},
	}
	gotJSON, _ := json.Marshal(body)
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("body =\n%s\nwant\n%s", gotJSON, wantJSON)
	}

	// Probabilities come back keyed by option; the answer keeps request order.
	resp, err := prepared.Answer([]byte(`{"model":"m","answers":{
	  "q000":{"type":"choice","choice":"shipping","probabilities":{"shipping":0.7,"billing":0.3},"confidence":0.5},
	  "q001":{"type":"noul","noul":0.8},
	  "q002":{"type":"score","score":0.9,"probabilities":{"0":0.1,"1":0.9},"confidence":0.8},
	  "q003":{"type":"choice","choice":"true","probabilities":{"true":0.6,"false":0.4},"confidence":0.2}
	},"usage":{"input_tokens":120,"output_tokens":0}}`), "m")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(resp)
	for _, fragment := range []string{
		`{"type":"choice","name":"route","choice":"shipping","probabilities":[{"value":"billing","probability":0.3},{"value":"shipping","probability":0.7}],"confidence":0.39999999999999997}`,
		`{"type":"predicate","name":"angry","probability":0.8}`,
		`{"type":"score","name":null,"score":0.9,"probabilities":[{"value":0,"label":"low","probability":0.1},{"value":1,"label":"high","probability":0.9}],"confidence":0.8}`,
		`"choice":true,"probabilities":[{"value":true,"probability":0.6},{"value":false,"probability":0.4}]`,
		`"input_tokens":120`,
		`"total_tokens":120`,
	} {
		if !strings.Contains(string(out), fragment) {
			t.Errorf("response missing %s\n%s", fragment, out)
		}
	}

	if _, err := prepared.Answer([]byte(`{"answers":{}}`), "m"); err == nil {
		t.Fatal("a response without answers was accepted")
	}
}

func TestNativeDecisionsRejectsInvalidRequests(t *testing.T) {
	levels := make([]string, 11)
	for i := range levels {
		levels[i] = `{"label":"l"}`
	}
	for name, body := range map[string]string{
		"no input":         `{"questions":[{"type":"predicate","instructions":"q"}]}`,
		"no questions":     `{"input":"x","questions":[]}`,
		"no instructions":  `{"input":"x","questions":[{"type":"predicate"}]}`,
		"one choice":       `{"input":"x","questions":[{"type":"choice","instructions":"q","choices":[{"value":"a"}]}]}`,
		"string/bool keys": `{"input":"x","questions":[{"type":"choice","instructions":"q","choices":[{"value":"true"},{"value":true}]}]}`,
		"too many levels":  `{"input":"x","questions":[{"type":"score","instructions":"q","levels":[` + strings.Join(levels, ",") + `]}]}`,
		"unknown type":     `{"input":"x","questions":[{"type":"rank","instructions":"q"}]}`,
		"image input":      `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:"}]}],"questions":[{"type":"predicate","instructions":"q"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := PrepareNativeOpenAI(decodeOpenAIRequest(t, body))
			var reqErr *RequestError
			if !errors.As(err, &reqErr) {
				t.Fatalf("err = %v, want a RequestError", err)
			}
		})
	}
}

func TestNativeType(t *testing.T) {
	write := func(dir, rel string, data []byte) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	checkpoint := t.TempDir()
	write(checkpoint, "encoder/config.json", []byte("{}"))
	write(checkpoint, "rl_agent_config.json", []byte("{}"))

	gguf := t.TempDir()
	write(gguf, "mmproj-openjev.gguf", testGGUF(t, "clip", ""))
	write(gguf, "Laya-Q8_0.gguf", testGGUF(t, "modern-bert", "laya"))

	// Only the first weight GGUF is read; MTP modules are skipped.
	firstOnly := t.TempDir()
	write(firstOnly, "Laya-BF16.gguf", testGGUF(t, "modern-bert", "laya"))
	write(firstOnly, "Laya-Q8_0.gguf", []byte("not a gguf"))
	write(firstOnly, "Laya-0-mtp-Q8_0.gguf", testGGUF(t, "qwen35-mtp", "openjev"))

	nested := t.TempDir()
	write(nested, "Q4_K_M/OpenJev-Q4_K_M.gguf", testGGUF(t, "qwen35", "openjev"))

	plain := t.TempDir()
	write(plain, "model.gguf", testGGUF(t, "qwen3", ""))

	for dir, want := range map[string]string{checkpoint: "laya", gguf: "laya", firstOnly: "laya", nested: "openjev", plain: "", t.TempDir(): ""} {
		if got := NativeType(dir); got != want {
			t.Errorf("NativeType(%s) = %q, want %q", dir, got, want)
		}
	}
}

// testGGUF builds a GGUF header with string metadata only.
func testGGUF(t *testing.T, arch, decisionType string) []byte {
	t.Helper()
	kv := [][2]string{{"general.architecture", arch}}
	if decisionType != "" {
		kv = append(kv, [2]string{arch + ".decision.type", decisionType})
	}
	var b bytes.Buffer
	b.WriteString("GGUF")
	put := func(v any) {
		if err := binary.Write(&b, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
	}
	putString := func(s string) {
		put(uint64(len(s)))
		b.WriteString(s)
	}
	put(uint32(3))
	put(uint64(0))
	put(uint64(len(kv)))
	for _, entry := range kv {
		putString(entry[0])
		put(uint32(8)) // GGUF string
		putString(entry[1])
	}
	return b.Bytes()
}
