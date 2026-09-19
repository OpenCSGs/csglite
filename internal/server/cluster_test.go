package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

// newClusterTestServer builds a server whose cluster manager uses the shared
// in-memory discovery bus and an ephemeral loopback port, then starts it.
func newClusterTestServer(t *testing.T, bus *cluster.MemoryBus) *Server {
	t.Helper()
	clusterTestHooks.discoverer = func() cluster.Discoverer { return bus.NewDiscoverer(netip.MustParseAddr("127.0.0.1")) }
	clusterTestHooks.listenAddr = "127.0.0.1:0"
	t.Cleanup(func() {
		clusterTestHooks.discoverer = nil
		clusterTestHooks.listenAddr = ""
	})
	home := t.TempDir()
	dir := t.TempDir()
	cfg := &config.Config{
		ServerURL:  "https://hub.opencsg.com",
		ListenAddr: "127.0.0.1:11435",
		BoundAddr:  "127.0.0.1:11435",
		ModelDir:   filepath.Join(dir, "models"),
		DatasetDir: filepath.Join(dir, "datasets"),
	}
	_ = home
	s := New(cfg, "test")
	if s.cluster == nil {
		t.Fatal("cluster manager was not created")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.startCluster(ctx)
	if s.cluster.Active() {
		t.Fatal("a fresh node must start dormant")
	}
	// The operator enables the feature on both machines before pairing.
	if err := s.cluster.Activate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		s.shutdownRuntime()
	})
	return s
}

func clusterWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestClusterSummaryWithoutClusterIsEmpty(t *testing.T) {
	t.Setenv(EnvClusterDisabled, "1")
	s := newTestServer(t)
	if s.cluster != nil {
		t.Fatal("cluster should be disabled by env")
	}
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cluster/summary", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("summary %d %s", rec.Code, rec.Body)
	}
	var sum cluster.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil || sum.InCluster || len(sum.Nodes) != 0 || sum.NodeLimit != 2 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	rec = httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cluster", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "cluster_disabled") {
		t.Fatalf("disabled cluster route %d %s", rec.Code, rec.Body)
	}
	// Routing to the cluster with the feature off is a clear 404, not a crash.
	rec = httptest.NewRecorder()
	body := `{"model":"x","source":"cluster","messages":[{"role":"user","content":"hi"}]}`
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cluster source without cluster: %d %s", rec.Code, rec.Body)
	}
}

func TestClusterRoutesRequestToTheNodeHoldingTheModel(t *testing.T) {
	bus := cluster.NewMemoryBus()
	a := newClusterTestServer(t, bus)
	b := newClusterTestServer(t, bus)

	// Node A holds a model with a fake engine already loaded.
	mustSaveLocalModel(t, a.cfg.ModelDir, &model.LocalModel{Namespace: "Qwen", Name: "Qwen3-0.6B-GGUF", Format: model.FormatGGUF, Size: 1 << 30, Files: []string{"model.gguf"}, DownloadedAt: time.Unix(100, 0)})
	storageID := "Qwen/Qwen3-0.6B-GGUF"
	publicID := a.localInferenceModelID(storageID) // "Qwen3-0.6B-GGUF" for an OpenCSG model
	fake := &fakeChatCompletionEngine{resp: api.OpenAIChatResponse{
		ID: "cmpl-1", Object: "chat.completion", Model: publicID,
		Choices: []api.OpenAIChoice{{Index: 0, Message: &api.Message{Role: "assistant", Content: "served by A"}}},
		Usage:   api.OpenAIUsage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
	}}
	a.mu.Lock()
	a.engines[engineCacheKey(storageID, engineModeChat)] = &managedEngine{engine: fake, numParallel: 2, nGPULayers: -1, lastUsed: time.Now(), keepAlive: -1}
	a.mu.Unlock()

	// A founds the cluster; B joins over the HTTP API with the token.
	rec := httptest.NewRecorder()
	a.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/cluster", strings.NewReader(`{"name":"Lab"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	var created struct {
		JoinToken string `json:"join_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	clusterWait(t, "b to discover a", func() bool { return len(b.cluster.DiscoveredNodes()) == 1 })
	rec = httptest.NewRecorder()
	joinBody, _ := json.Marshal(map[string]string{"token": created.JoinToken})
	b.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/cluster/join", bytes.NewReader(joinBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("join %d %s", rec.Code, rec.Body)
	}
	clusterWait(t, "b to see a's status", func() bool {
		rt, ok := b.cluster.Directory().Get(a.cluster.Identity().UUID)
		return ok && rt.Health == cluster.HealthHealthy && rt.Status != nil
	})

	// B has no such model locally; the request is routed to A transparently.
	rec = httptest.NewRecorder()
	chat := `{"model":"` + publicID + `","messages":[{"role":"user","content":"hi"}]}`
	b.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(chat)))
	if rec.Code != http.StatusOK {
		t.Fatalf("routed chat %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "served by A") {
		t.Fatalf("unexpected body %s", rec.Body)
	}
	if got := rec.Header().Get(cluster.NodeHeader); got != a.cluster.Identity().UUID {
		t.Fatalf("%s = %q, want A's uuid", cluster.NodeHeader, got)
	}
	if fake.lastReq == nil || fake.lastReq["source"] != nil {
		// The executing node runs it as a local request; the "source":"local"
		// marker is stripped by the handler before it reaches the engine.
		t.Logf("engine saw request %v", fake.lastReq)
	}

	// The same model requested on A stays local (local-first).
	rec = httptest.NewRecorder()
	a.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(chat)))
	if rec.Code != http.StatusOK || rec.Header().Get(cluster.NodeHeader) != "" {
		t.Fatalf("local request was routed: %d header=%q", rec.Code, rec.Header().Get(cluster.NodeHeader))
	}

	// B's model list includes A's model as a cluster model.
	rec = httptest.NewRecorder()
	b.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tags", nil))
	var tags api.TagsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tags); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range tags.Models {
		if m.Model == publicID && m.Source == cluster.SourceCluster && len(m.Nodes) == 1 && !m.Nodes[0].Local {
			found = true
		}
	}
	if !found {
		t.Fatalf("cluster model missing from /api/tags: %+v", tags.Models)
	}

	// The dashboard summary on B shows both machines.
	rec = httptest.NewRecorder()
	b.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cluster/summary", nil))
	var sum cluster.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil || !sum.InCluster || sum.NodeCount != 2 || sum.OnlineCount != 2 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	// Pinning A explicitly works from B; pinning a stranger is a 404.
	rec = httptest.NewRecorder()
	pinned := `{"model":"` + publicID + `","source":"node:` + a.cluster.Identity().UUID + `","messages":[{"role":"user","content":"hi"}]}`
	b.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(pinned)))
	if rec.Code != http.StatusOK {
		t.Fatalf("pinned %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	stranger := `{"model":"` + publicID + `","source":"node:00000000-0000-0000-0000-000000000000","messages":[]}`
	b.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(stranger)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger pin %d %s", rec.Code, rec.Body)
	}
}

func TestClusterLocalStatusReportsLoadedModels(t *testing.T) {
	bus := cluster.NewMemoryBus()
	s := newClusterTestServer(t, bus)
	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{Namespace: "Qwen", Name: "Qwen3-0.6B-GGUF", Format: model.FormatGGUF, Size: 2 << 30, Files: []string{"model.gguf"}, DownloadedAt: time.Unix(100, 0)})
	storageID := "Qwen/Qwen3-0.6B-GGUF"
	publicID := s.localInferenceModelID(storageID)
	s.mu.Lock()
	s.engines[engineCacheKey(storageID, engineModeChat)] = &managedEngine{engine: &fakeEngine{}, numParallel: 3, activeRequests: 1, nGPULayers: 20, lastUsed: time.Now(), keepAlive: time.Minute}
	s.mu.Unlock()
	st := s.cluster.LocalStatus(context.Background())
	ms, ok := st.Model(publicID)
	if !ok || !ms.Loaded || ms.Slots != 3 || ms.Active != 1 || ms.NGPULayers != 20 || ms.Size != 2<<30 {
		t.Fatalf("model status %+v (found %v)", ms, ok)
	}
	if st.Inflight != 1 || st.CPU.Cores == 0 || st.RAM.Total == 0 {
		t.Fatalf("status %+v", st)
	}
	if st.ModelSource.ServerURL != "https://hub.opencsg.com" {
		t.Fatalf("model source %+v", st.ModelSource)
	}
}

// fakeASREngine answers every transcription with a fixed text.
type fakeASREngine struct{ text string }

func (f *fakeASREngine) Transcribe(context.Context, api.OpenAIAudioTranscriptionRequest) (*api.OpenAIAudioTranscriptionResponse, error) {
	return &api.OpenAIAudioTranscriptionResponse{Text: f.text, Language: "zh"}, nil
}

func (f *fakeASREngine) TranscribeStream(_ context.Context, _ api.OpenAIAudioTranscriptionRequest, onChunk func(api.OpenAIAudioTranscriptionResponse) error) error {
	return onChunk(api.OpenAIAudioTranscriptionResponse{Text: f.text})
}
func (f *fakeASREngine) Close() error      { return nil }
func (f *fakeASREngine) ModelName() string { return "fake-asr" }

func TestClusterRoutesTranscriptionThroughTheSameResolver(t *testing.T) {
	bus := cluster.NewMemoryBus()
	a := newClusterTestServer(t, bus)
	b := newClusterTestServer(t, bus)
	mustSaveLocalModel(t, a.cfg.ModelDir, &model.LocalModel{Namespace: "Qwen", Name: "Qwen3-ASR-0.6B", Format: model.FormatPyTorch, Size: 1 << 30, PipelineTag: "automatic-speech-recognition", Files: []string{"model.safetensors"}, DownloadedAt: time.Unix(100, 0)})
	storageID := "Qwen/Qwen3-ASR-0.6B"
	publicID := a.localInferenceModelID(storageID)
	a.mu.Lock()
	a.asrEngines[storageID] = &managedASREngine{engine: &fakeASREngine{text: "你好 集群"}, lastUsed: time.Now(), keepAlive: -1}
	a.mu.Unlock()

	rec := httptest.NewRecorder()
	a.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/cluster", strings.NewReader(`{"name":"Lab"}`)))
	var created struct {
		JoinToken string `json:"join_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	clusterWait(t, "b to discover a", func() bool { return len(b.cluster.DiscoveredNodes()) == 1 })
	rec = httptest.NewRecorder()
	joinBody, _ := json.Marshal(map[string]string{"token": created.JoinToken})
	b.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/cluster/join", bytes.NewReader(joinBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("join %d %s", rec.Code, rec.Body)
	}
	clusterWait(t, "b to see a's ASR model loaded", func() bool {
		rt, ok := b.cluster.Directory().Get(a.cluster.Identity().UUID)
		if !ok || rt.Status == nil {
			return false
		}
		ms, ok := rt.Status.Model(publicID)
		return ok && ms.Loaded && ms.Slots == 1
	})

	// A multipart upload on B for a model only A holds goes to A.
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	part, _ := mw.CreateFormFile("file", "hello.wav")
	_, _ = part.Write([]byte("RIFF....WAVEfmt fake"))
	_ = mw.WriteField("model", publicID)
	_ = mw.WriteField("response_format", "json")
	_ = mw.Close()
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(form.Bytes()))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	b.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "你好 集群") {
		t.Fatalf("routed transcription %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get(cluster.NodeHeader) != a.cluster.Identity().UUID {
		t.Fatalf("missing node header: %v", rec.Header())
	}
	// Streaming takes the same route.
	_ = mw
	var form2 bytes.Buffer
	mw2 := multipart.NewWriter(&form2)
	part2, _ := mw2.CreateFormFile("file", "hello.wav")
	_, _ = part2.Write([]byte("RIFF....WAVEfmt fake"))
	_ = mw2.WriteField("model", publicID)
	_ = mw2.WriteField("stream", "true")
	_ = mw2.Close()
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(form2.Bytes()))
	req.Header.Set("Content-Type", mw2.FormDataContentType())
	b.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "你好 集群") || !strings.Contains(rec.Body.String(), `"done":true`) {
		t.Fatalf("routed stream %d %s", rec.Code, rec.Body)
	}
	// On A itself the model is local and stays local.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(form.Bytes()))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	a.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get(cluster.NodeHeader) != "" {
		t.Fatalf("local transcription %d header=%q %s", rec.Code, rec.Header().Get(cluster.NodeHeader), rec.Body)
	}
}

func TestClusterPullSpecSplitsRegistryPrefix(t *testing.T) {
	h := &clusterHost{}
	cases := map[string][2]string{
		"modelscope/Qwen/Qwen3.5-2B":   {"Qwen/Qwen3.5-2B", "modelscope"},
		"huggingface/acme/demo":        {"acme/demo", "huggingface"},
		"Qwen/Qwen3-ASR-0.6B":          {"Qwen/Qwen3-ASR-0.6B", ""},
		"Qwen3-Embedding-0.6B":         {"Qwen3-Embedding-0.6B", ""},
		"modelscope/only-two-segments": {"modelscope/only-two-segments", ""},
	}
	for in, want := range cases {
		repo, source := h.PullSpec(in)
		if repo != want[0] || source != want[1] {
			t.Errorf("PullSpec(%q) = %q, %q; want %q, %q", in, repo, source, want[0], want[1])
		}
	}
}

func TestClusterPullJobCopiesModelFromPeer(t *testing.T) {
	bus := cluster.NewMemoryBus()
	a := newClusterTestServer(t, bus)
	b := newClusterTestServer(t, bus)
	// A holds a small "model" with real files.
	mustSaveLocalModel(t, a.cfg.ModelDir, &model.LocalModel{Namespace: "Qwen", Name: "Tiny-GGUF", Format: model.FormatGGUF, Size: 3000, Files: []string{"tiny.gguf", "config.json"}, DownloadedAt: time.Unix(100, 0)})
	dir := model.RegistryModelDir(a.cfg.ModelDir, "opencsg", "Qwen", "Tiny-GGUF")
	payload := bytes.Repeat([]byte("g"), 3000)
	if err := os.WriteFile(filepath.Join(dir, "tiny.gguf"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A derived artifact that is not in the manifest is an optional extra.
	if err := os.WriteFile(filepath.Join(dir, "Tiny-f16.gguf"), bytes.Repeat([]byte("x"), 500), 0o644); err != nil {
		t.Fatal(err)
	}
	publicID := a.localInferenceModelID("Qwen/Tiny-GGUF")
	bundle, err := (&clusterHost{s: a}).ModelBundle(publicID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Files) != 2 || len(bundle.Extras) != 1 || bundle.Extras[0].Path != "Tiny-f16.gguf" {
		t.Fatalf("bundle files %+v extras %+v", bundle.Files, bundle.Extras)
	}

	rec := httptest.NewRecorder()
	a.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/cluster", strings.NewReader(`{"name":"Lab"}`)))
	var created struct {
		JoinToken string `json:"join_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	clusterWait(t, "b to discover a", func() bool { return len(b.cluster.DiscoveredNodes()) == 1 })
	if _, err := b.cluster.Join(context.Background(), created.JoinToken, ""); err != nil {
		t.Fatal(err)
	}
	clusterWait(t, "b to see the model on a", func() bool {
		rt, ok := b.cluster.Directory().Get(a.cluster.Identity().UUID)
		if !ok || rt.Status == nil {
			return false
		}
		_, ok = rt.Status.Model(publicID)
		return ok
	})

	// "Sync to node" on B is a plain pull job; it is satisfied from A.
	job, err := b.createPullJob("model", "Qwen/Tiny-GGUF", "opencsg", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	clusterWait(t, "pull job to finish", func() bool {
		job.mu.Lock()
		defer job.mu.Unlock()
		return job.status == pullJobSucceeded || job.status == pullJobFailed
	})
	job.mu.Lock()
	status, progress, jobErr := job.status, job.progress, job.err
	job.mu.Unlock()
	if status != pullJobSucceeded {
		t.Fatalf("pull job %s: %s %s", status, progress.Status, jobErr)
	}
	// Qwen/Tiny-GGUF exists nowhere but on A, so success proves the copy
	// came from the peer rather than the model source.
	if !b.manager.Exists("Qwen/Tiny-GGUF") {
		t.Fatal("copied model is not installed on b")
	}
	got, err := os.ReadFile(filepath.Join(model.RegistryModelDir(b.cfg.ModelDir, "opencsg", "Qwen", "Tiny-GGUF"), "tiny.gguf"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("copied file differs: %v (%d bytes)", err, len(got))
	}
	lm, err := b.manager.Get("Qwen/Tiny-GGUF")
	if err != nil || lm.Format != model.FormatGGUF || lm.Size != 3000 {
		t.Fatalf("manifest on b: %+v %v", lm, err)
	}
	// The derived extra follows in the background on a fast (loopback) link.
	clusterWait(t, "derived file to arrive", func() bool {
		info, err := os.Stat(filepath.Join(model.RegistryModelDir(b.cfg.ModelDir, "opencsg", "Qwen", "Tiny-GGUF"), "Tiny-f16.gguf"))
		return err == nil && info.Size() == 500
	})
	// B now advertises the model too.
	clusterWait(t, "b to list the copied model", func() bool {
		st := b.cluster.LocalStatus(context.Background())
		_, ok := st.Model(publicID)
		return ok
	})
}
