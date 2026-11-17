// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/inference"
)

type toolCapturingHost struct {
	*fakeHost
	mu   sync.Mutex
	body map[string]any
}

func (h *toolCapturingHost) InferenceHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		_ = json.Unmarshal(raw, &h.body)
		h.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_time\",\"arguments\":\"{}\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
}

func startNodeWithHost(t *testing.T, bus *MemoryBus, name string, host Host) *testNode {
	t.Helper()
	m, err := New(Options{Dir: t.TempDir(), Host: host, Discoverer: bus.NewDiscoverer(mustAddr("127.0.0.1")), ListenAddr: "127.0.0.1:0", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := m.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := m.Activate(); err != nil {
		cancel()
		m.Stop()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); m.Stop() })
	_ = m.identity.SaveName(name)
	return &testNode{m: m}
}

func TestColdStartClusterToolStreamingPreservesToolsAndSSE(t *testing.T) {
	bus := NewMemoryBus()
	controller := startNode(t, bus, "controller", &fakeHost{limit: 0, licensed: true})
	workerHost := &toolCapturingHost{fakeHost: &fakeHost{limit: 0, licensed: true, models: []ModelStatus{{
		ID: "cold-model", Size: gb, Loaded: false, NativeToolStreaming: true,
	}}}}
	worker := startNodeWithHost(t, bus, "worker", workerHost)
	_, token, err := controller.m.CreateCluster("Lab")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "worker to discover controller", func() bool { return len(worker.m.dir.Discovered(time.Minute)) >= 1 })
	if _, err := worker.m.Join(context.Background(), token, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "worker status to reach controller", func() bool {
		rt, ok := controller.m.dir.Get(worker.m.identity.UUID)
		return ok && rt.Health == HealthHealthy && rt.Status != nil && len(rt.Status.Models) > 0
	})

	engine, err := controller.m.RoutedEngine(context.Background(), "cold-model", SourceCluster, EngineOptions{Kind: EngineChat})
	if err != nil {
		t.Fatal(err)
	}
	if !inference.SupportsNativeToolStreaming(engine) {
		t.Fatal("cold-start routed engine lost native tool streaming capability")
	}
	proxy, ok := engine.(inference.ChatCompletionProxier)
	if !ok {
		t.Fatal("routed engine cannot proxy chat completions")
	}
	resp, err := proxy.ChatCompletion(context.Background(), map[string]any{
		"model":               "cold-model",
		"stream":              true,
		"messages":            []any{map[string]any{"role": "user", "content": "time?"}},
		"tools":               []any{map[string]any{"type": "function", "function": map[string]any{"name": "get_time"}}},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !containsAll(string(raw), `"tool_calls"`, `"id":"call_1"`, `"name":"get_time"`, `"arguments":"{}"`) {
		t.Fatalf("SSE tool deltas lost through the cluster hop: %s", raw)
	}
	workerHost.mu.Lock()
	sent := workerHost.body
	workerHost.mu.Unlock()
	if sent["tool_choice"] != "auto" || sent["parallel_tool_calls"] != true {
		t.Fatalf("tool fields lost through the cluster hop: %#v", sent)
	}
	if tools, ok := sent["tools"].([]any); !ok || len(tools) != 1 {
		t.Fatalf("tools lost through the cluster hop: %#v", sent["tools"])
	}
	if sent["source"] != "local" {
		t.Fatalf("worker did not receive a local source: %#v", sent["source"])
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !contains(text, part) {
			return false
		}
	}
	return true
}

func contains(text, part string) bool {
	return len(part) == 0 || (len(text) >= len(part) && indexOf(text, part) >= 0)
}

func indexOf(text, part string) int {
	for i := 0; i+len(part) <= len(text); i++ {
		if text[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
