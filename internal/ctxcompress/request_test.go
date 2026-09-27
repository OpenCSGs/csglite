package ctxcompress

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func grepOutput(files, matches int) string {
	var b strings.Builder
	for f := 0; f < files; f++ {
		for m := 1; m <= matches; m++ {
			fmt.Fprintf(&b, "internal/pkg/file%d.go:%d:\tvalue := compute(%d)\n", f, m*3, m)
		}
	}
	return b.String()
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCompressRequestAnthropic(t *testing.T) {
	search := grepOutput(3, 10)
	fileText := strings.Repeat("same line\n", 100)
	body := mustJSON(t, map[string]any{
		"model":  "claude",
		"system": []any{map[string]any{"type": "text", "text": search, "cache_control": map[string]any{"type": "ephemeral"}}},
		"messages": []any{
			map[string]any{"role": "user", "content": search},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Grep", "input": map[string]any{}},
				map[string]any{"type": "tool_use", "id": "toolu_2", "name": "Read", "input": map[string]any{}},
				map[string]any{"type": "tool_use", "id": "toolu_3", "name": "Bash", "input": map[string]any{}},
				map[string]any{"type": "tool_use", "id": "toolu_4", "name": "Bash", "input": map[string]any{}},
			}},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": search, "cache_control": map[string]any{"type": "ephemeral"}},
				map[string]any{"type": "tool_result", "tool_use_id": "toolu_2", "content": []any{map[string]any{"type": "text", "text": fileText}}},
				map[string]any{"type": "tool_result", "tool_use_id": "toolu_3", "content": search, "is_error": true},
				map[string]any{"type": "tool_result", "tool_use_id": "toolu_4", "content": search},
			}},
		},
	})

	result, err := CompressRequest(ProtocolAnthropic, body, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Stats.Blocks != 3 || result.Stats.Compressed != 2 {
		t.Fatalf("stats = %+v", result.Stats)
	}
	if result.Stats.ByKind[KindDuplicate].Compressed != 1 || result.Stats.TokensSaved <= 0 {
		t.Fatalf("by kind = %+v", result.Stats.ByKind)
	}

	var got struct {
		System   []map[string]any `json:"system"`
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(result.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.System[0]["text"] != search || got.Messages[0].Content != search {
		t.Fatal("system prompt or user text was rewritten")
	}
	results := got.Messages[2].Content.([]any)
	grep := results[0].(map[string]any)
	if text := grep["content"].(string); !strings.HasPrefix(text, "internal/pkg/file0.go\n  3:") {
		t.Fatalf("grep result not grouped: %q", text[:60])
	}
	if grep["cache_control"] == nil {
		t.Fatal("cache_control marker was dropped")
	}
	read := results[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if read["text"] != fileText {
		t.Fatal("Read result was rewritten")
	}
	if results[2].(map[string]any)["content"] != search {
		t.Fatal("error result was rewritten")
	}
	if dup := results[3].(map[string]any)["content"]; dup != "[Identical to the earlier output of tool call toolu_1.]" {
		t.Fatalf("duplicate = %v", dup)
	}

	again, err := CompressRequest(ProtocolAnthropic, body, Options{})
	if err != nil || string(again.Body) != string(result.Body) {
		t.Fatal("compressing the same request twice gave different bytes")
	}
}

func TestCompressRequestOpenAIChatAndResponses(t *testing.T) {
	search := grepOutput(2, 10)
	chat := mustJSON(t, map[string]any{
		"model": "gpt",
		"messages": []any{
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "grep", "arguments": "{}"}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": search},
		},
	})
	result, err := CompressRequest(ProtocolOpenAIChat, chat, Options{})
	if err != nil || !result.Changed || result.Stats.Compressed != 1 {
		t.Fatalf("chat: %+v %v", result.Stats, err)
	}
	if strings.Contains(string(result.Body), `internal/pkg/file0.go:3:`) {
		t.Fatal("chat tool message not compressed")
	}

	responses := mustJSON(t, map[string]any{
		"model": "gpt",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": search},
			map[string]any{"type": "function_call", "call_id": "call_9", "name": "shell", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "call_9", "output": search},
		},
	})
	result, err = CompressRequest(ProtocolResponses, responses, Options{})
	if err != nil || result.Stats.Blocks != 1 || result.Stats.Compressed != 1 {
		t.Fatalf("responses: %+v %v", result.Stats, err)
	}
	var got struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(result.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Input[0]["content"] != search || got.Input[2]["output"] == search {
		t.Fatal("responses: wrong item rewritten")
	}
}

func TestCompressRequestUnchangedKeepsOriginalBytes(t *testing.T) {
	body := []byte(`{"model":"m",  "messages":[{"role":"user","content":"hi"}], "big": 12345678901234567890}`)
	result, err := CompressRequest(ProtocolOpenAIChat, body, Options{})
	if err != nil || result.Changed || string(result.Body) != string(body) {
		t.Fatalf("unchanged request was rewritten: %s %v", result.Body, err)
	}
	if _, err := CompressRequest(ProtocolAnthropic, []byte("not json"), Options{}); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestCompressRequestPreservesLargeNumbers(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"messages": []any{map[string]any{"role": "tool", "tool_call_id": "c", "content": grepOutput(2, 10)}},
	})
	body = []byte(strings.Replace(string(body), `{"messages"`, `{"seed":12345678901234567890,"temperature":0.10,"messages"`, 1))
	result, err := CompressRequest(ProtocolOpenAIChat, body, Options{})
	if err != nil || !result.Changed {
		t.Fatalf("%v %+v", err, result.Stats)
	}
	if !strings.Contains(string(result.Body), `"seed":12345678901234567890`) || !strings.Contains(string(result.Body), `"temperature":0.10`) {
		t.Fatalf("numbers changed: %s", result.Body[:80])
	}
}

func TestCompressRequestNeverDedupesFileContent(t *testing.T) {
	var read, code strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&read, "%d\tline %d of the file\n", i, i)
	}
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&code, "func f%d() {\n\treturn\n}\n", i)
	}
	result := func(id, text string) map[string]any {
		return map[string]any{"type": "tool_result", "tool_use_id": id, "content": text}
	}
	body := mustJSON(t, map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "t1", "name": "Read", "input": map[string]any{}},
			map[string]any{"type": "tool_use", "id": "t2", "name": "Read", "input": map[string]any{}},
			map[string]any{"type": "tool_use", "id": "t3", "name": "Bash", "input": map[string]any{}},
			map[string]any{"type": "tool_use", "id": "t4", "name": "Bash", "input": map[string]any{}},
			map[string]any{"type": "tool_use", "id": "t5", "name": "Bash", "input": map[string]any{}},
		}},
		map[string]any{"role": "user", "content": []any{
			result("t1", read.String()), result("t2", read.String()),
			result("t3", read.String()), // the same numbered content through a shell
			result("t4", code.String()), result("t5", code.String()),
		}},
	}})
	out, err := CompressRequest(ProtocolAnthropic, body, Options{Sample: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Changed || strings.Contains(string(out.Body), "Identical to the earlier output") {
		t.Fatalf("repeated file content was replaced: %s", out.Body)
	}
}
