package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/pkg/api"
)

// TestSGLangHelperProcess is not a normal test: it is the subprocess SGLang
// stand-in. It serves /health and /v1/chat/completions so the engine's real
// OpenAI request and streaming logic is exercised without a GPU.
func TestSGLangHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_SGLANG_HELPER") != "1" {
		return
	}
	port := ""
	for i, arg := range os.Args {
		if arg == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
		}
	}
	if port == "" {
		fmt.Fprintln(os.Stderr, "helper: missing --port")
		os.Exit(2)
	}
	if pidFile := os.Getenv("SGLANG_TEST_CHILD_PID_FILE"); pidFile != "" {
		child := exec.Command("sleep", "60")
		if os.Getenv("SGLANG_TEST_MODE") == "stubborn-child" {
			child = exec.Command("sh", "-c", `trap '' TERM; sleep 60`)
		}
		if err := child.Start(); err == nil {
			_ = os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o644)
		}
	}
	switch os.Getenv("SGLANG_TEST_MODE") {
	case "exit":
		fmt.Fprintln(os.Stderr, "synthetic SGLang startup failure")
		os.Exit(2)
	case "hang":
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if path := os.Getenv("SGLANG_TEST_REQUEST"); path != "" {
			data, _ := json.Marshal(body)
			_ = os.WriteFile(path, data, 0o644)
		}
		stream, _ := body["stream"].(bool)
		if !stream {
			_ = json.NewEncoder(w).Encode(api.OpenAIChatResponse{
				ID: "chatcmpl-helper", Object: "chat.completion", Created: 1, Model: "tiny-llama",
				Choices: []api.OpenAIChoice{{
					Index: 0,
					Message: &api.Message{
						Role:    "assistant",
						Content: nil,
						ToolCalls: []api.ToolCall{{
							Index: intPtr(0), ID: "call_1", Type: "function",
							Function: api.ToolFunction{Name: "get_weather", Arguments: `{"city":"Paris"}`},
						}},
					},
					FinishReason: strPtr("tool_calls"),
				}},
				Usage: api.OpenAIUsage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
			})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		if os.Getenv("SGLANG_TEST_MODE") == "slow-stream" {
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"}}]}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(5 * time.Second)
			return
		}
		for _, chunk := range []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":"hello","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"content":" world","tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	server := &http.Server{Addr: "127.0.0.1:" + port, Handler: mux}
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }

func useFakeSGLangRuntime(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("GO_WANT_SGLANG_HELPER", "1")
	t.Setenv("SGLANG_TEST_MODE", mode)
	t.Setenv("CSGHUB_LITE_DISABLE_FILE_LOGGING", "1")
	originalProbe := sglangRuntimeProbe
	sglangRuntimeProbe = func(string) SGLangRuntime {
		return SGLangRuntime{Known: true, Installed: true, Available: true, Python: os.Args[0], Version: "test"}
	}
	ResetSGLangRuntimeCache()
	original := sglangExecCommand
	sglangExecCommand = func(_ string, args ...string) *exec.Cmd {
		command := exec.Command(os.Args[0], append([]string{"-test.run=TestSGLangHelperProcess", "--"}, args...)...)
		return command
	}
	t.Cleanup(func() {
		sglangExecCommand = original
		sglangRuntimeProbe = originalProbe
		ResetSGLangRuntimeCache()
	})
}

func startTestSGLang(t *testing.T, mode string) (*sglangEngine, string) {
	t.Helper()
	useFakeSGLangRuntime(t, mode)
	requestPath := filepath.Join(t.TempDir(), "request.json")
	t.Setenv("SGLANG_TEST_REQUEST", requestPath)
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	engine, err := newSGLangEngine(context.Background(), dir, "tiny-llama", "tiny-llama", t.TempDir(), 0, 1, "", false, nil, "")
	if err != nil {
		t.Fatalf("newSGLangEngine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine, requestPath
}

func TestSGLangEngineChatAndStream(t *testing.T) {
	engine, _ := startTestSGLang(t, "")
	if err := engine.RuntimeError(); err != nil {
		t.Fatalf("runtime error after ready: %v", err)
	}

	text, err := engine.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, Options{}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if text != "" {
		t.Fatalf("non-stream tool response text = %q, want empty content", text)
	}

	var tokens []string
	text, err = engine.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, Options{}, func(token string) {
		tokens = append(tokens, token)
	})
	if err != nil {
		t.Fatalf("stream Chat: %v", err)
	}
	if len(tokens) == 0 || text == "" {
		t.Fatalf("stream produced no tokens: %#v", tokens)
	}
}

func TestSGLangEngineProxiesToolRequestsAndDeltas(t *testing.T) {
	engine, requestPath := startTestSGLang(t, "")
	body := map[string]interface{}{
		"model": "ignored",
		"messages": []map[string]interface{}{
			{"role": "user", "content": "weather?"},
			{"role": "assistant", "tool_calls": []map[string]interface{}{{
				"id": "call_0", "type": "function",
				"function": map[string]interface{}{"name": "get_weather", "arguments": `{"city":"Paris"}`},
			}}},
			{"role": "tool", "tool_call_id": "call_0", "name": "get_weather", "content": "sunny"},
		},
		"tools":               []map[string]interface{}{{"type": "function", "function": map[string]interface{}{"name": "get_weather", "parameters": map[string]interface{}{"type": "object"}}}},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
	}
	resp, err := engine.ChatCompletion(context.Background(), body)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	var response api.OpenAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(response.Choices) != 1 || len(response.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool response = %+v", response)
	}
	call := response.Choices[0].Message.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "get_weather" || call.Function.Arguments != `{"city":"Paris"}` {
		t.Fatalf("tool call = %+v", call)
	}

	captured, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]interface{}
	if err := json.Unmarshal(captured, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["tool_choice"] != "auto" || sent["parallel_tool_calls"] != true {
		t.Fatalf("tool fields not preserved: %v", sent)
	}
	if tools, ok := sent["tools"].([]interface{}); !ok || len(tools) != 1 {
		t.Fatalf("tools not forwarded: %v", sent["tools"])
	}
	messages, _ := sent["messages"].([]interface{})
	if len(messages) != 3 {
		t.Fatalf("messages = %v", sent["messages"])
	}
	assistant, _ := messages[1].(map[string]interface{})
	if assistant["tool_calls"] == nil {
		t.Fatalf("assistant tool_calls dropped: %v", assistant)
	}
	toolMessage, _ := messages[2].(map[string]interface{})
	if toolMessage["tool_call_id"] != "call_0" || toolMessage["role"] != "tool" {
		t.Fatalf("tool result history dropped: %v", toolMessage)
	}
	if sent["model"] != "tiny-llama" {
		t.Fatalf("model = %v, want served model", sent["model"])
	}

	streamBody := map[string]interface{}{"stream": true, "messages": []map[string]interface{}{{"role": "user", "content": "weather?"}}}
	streamResp, err := engine.ChatCompletion(context.Background(), streamBody)
	if err != nil {
		t.Fatalf("stream ChatCompletion: %v", err)
	}
	stream, _ := io.ReadAll(streamResp.Body)
	streamResp.Body.Close()
	text := string(stream)
	for _, want := range []string{`"index":0`, `"id":"call_1"`, `"name":"get_weather"`, `"arguments":"{\"city\":"`, `"arguments":"\"Paris\"}"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %q:\n%s", want, text)
		}
	}
}

func TestSGLangEngineCancellationStopsStream(t *testing.T) {
	engine, _ := startTestSGLang(t, "slow-stream")
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(started)
		cancel()
	}()
	_, err := engine.Chat(ctx, []Message{{Role: "user", Content: "hi"}}, Options{}, func(string) {})
	<-started
	if err == nil {
		t.Fatal("cancelled stream returned no error")
	}
}

func TestSGLangEngineStartupFailureReportsOutput(t *testing.T) {
	useFakeSGLangRuntime(t, "exit")
	t.Setenv(sglangReadyTimeoutEnv, "3s")
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	_, err := newSGLangEngine(context.Background(), dir, "tiny-llama", "tiny-llama", t.TempDir(), 0, 1, "", false, nil, "")
	if err == nil {
		t.Fatal("startup failure returned no error")
	}
	if !strings.Contains(err.Error(), "synthetic SGLang startup failure") {
		t.Fatalf("error does not include process output: %v", err)
	}
}

func TestSGLangEngineReadinessTimeoutKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group assertion is Unix-specific")
	}
	useFakeSGLangRuntime(t, "hang")
	t.Setenv(sglangReadyTimeoutEnv, "1s")
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("SGLANG_TEST_CHILD_PID_FILE", pidFile)
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	_, err := newSGLangEngine(context.Background(), dir, "tiny-llama", "tiny-llama", t.TempDir(), 0, 1, "", false, nil, "")
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error = %v, want readiness timeout", err)
	}
	waitForPIDFile(t, pidFile)
	assertProcessGone(t, readPID(t, pidFile))
}

func TestSGLangEngineCloseReleasesProcessGroupAndPort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group assertion is Unix-specific")
	}
	engine, _ := startTestSGLang(t, "")
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	// The helper writes the child PID after start; re-run with a dedicated
	// engine so this test owns the file.
	_ = engine.Close()
	t.Setenv("SGLANG_TEST_CHILD_PID_FILE", pidFile)
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	engine, err := newSGLangEngine(context.Background(), dir, "tiny-llama", "tiny-llama", t.TempDir(), 0, 1, "", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	waitForPIDFile(t, pidFile)
	child := readPID(t, pidFile)
	port := engine.port
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if err := engine.RuntimeError(); err != nil {
		t.Fatalf("expected close is not a runtime failure: %v", err)
	}
	assertProcessGone(t, child)
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond); err == nil {
		conn.Close()
		t.Fatalf("port %d still accepts connections after Close", port)
	}
}

func waitForPIDFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) != "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("helper child PID file %s was not written", path)
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func TestSGLangEngineCloseReapsWorkerWhenParentExitsFirst(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group assertion is Unix-specific")
	}
	useFakeSGLangRuntime(t, "stubborn-child")
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("SGLANG_TEST_CHILD_PID_FILE", pidFile)
	dir := writeSGLangTestModel(t, tinyLlamaConfig, "model.safetensors")
	engine, err := newSGLangEngine(context.Background(), dir, "tiny-llama", "tiny-llama", t.TempDir(), 0, 1, "", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	waitForPIDFile(t, pidFile)
	child := readPID(t, pidFile)
	done := make(chan error, 1)
	go func() { done <- engine.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Close hung waiting for a worker that inherited stdout")
	}
	assertProcessGone(t, child)
}
