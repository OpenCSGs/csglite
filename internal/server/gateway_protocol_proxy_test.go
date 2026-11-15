package server

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/internal/tts"
	"github.com/opencsgs/csglite/pkg/api"
)

type gatewaySpeechEngine struct{}

func (*gatewaySpeechEngine) Speak(_ context.Context, req api.OpenAIAudioSpeechRequest) (*tts.Audio, error) {
	return &tts.Audio{Data: []byte("RIFF\x00\xffaudio"), Format: req.ResponseFormat, SampleRate: 24000}, nil
}
func (e *gatewaySpeechEngine) SpeakStream(ctx context.Context, req api.OpenAIAudioSpeechRequest, emit func(tts.Chunk) error) error {
	audio, err := e.Speak(ctx, req)
	if err != nil {
		return err
	}
	return emit(tts.Chunk{Data: audio.Data, Done: true})
}
func (*gatewaySpeechEngine) Info(context.Context) (*api.SpeechVoicesResponse, error) {
	return &api.SpeechVoicesResponse{}, nil
}
func (*gatewaySpeechEngine) Close() error      { return nil }
func (*gatewaySpeechEngine) ModelName() string { return "org/speech" }

func TestGatewayAuxiliaryProtocolsUsePublishedServices(t *testing.T) {
	s := newNativeDecisionTestServer(t)
	config.ResetProviderPools()
	t.Cleanup(config.ResetProviderPools)
	_, key, err := s.apiKeys.Create("protocol-tests")
	if err != nil {
		t.Fatal(err)
	}
	s.engines["org/laya"] = &managedEngine{engine: &fakeSystemOneEngine{}, numCtx: 8192, numParallel: 1}
	for name, tag := range map[string]string{"image": "text-to-image", "edit": "image-to-image", "asr": "automatic-speech-recognition", "speech": "text-to-speech", "embedding": "feature-extraction"} {
		lm := &model.LocalModel{Namespace: "org", Name: name, Format: model.FormatSafeTensors, Files: []string{"weights.bin"}, Size: 4, PipelineTag: tag, DownloadedAt: time.Now()}
		if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.cfg.ModelDir, "org", name, "weights.bin"), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s.imageEngines["org/image"] = &managedImageEngine{engine: &fakeImageEngine{}}
	s.imageEngines["org/edit"] = &managedImageEngine{engine: &fakeImageEngine{}}
	s.asrEngines["org/asr"] = &managedASREngine{engine: &fakeASREngine{text: "recognized audio"}}
	s.ttsEngines["org/speech"] = &managedTTSEngine{engine: &gatewaySpeechEngine{}}
	s.engines[engineCacheKey("org/embedding", engineModeEmbed)] = &managedEngine{engine: &fakeEmbeddingsEngine{resp: api.OpenAIEmbeddingsResponse{
		Object: "list", Model: "org/embedding", Data: []api.OpenAIEmbeddingObject{{Object: "embedding", Embedding: []float64{0.1, 0.2}, Index: 0}}, Usage: api.OpenAIUsage{PromptTokens: 2, TotalTokens: 2},
	}}, numCtx: 4096, numParallel: 1}
	var pools []config.ProviderPool
	for id, category := range map[string]string{"laya": "chat", "image": "image", "edit": "image", "asr": "asr", "speech": "tts", "embedding": "embedding"} {
		pools = append(pools, config.ProviderPool{ID: id, Name: id, Model: "service-" + id, Category: category, Enabled: true, Policy: "fixed", Members: []config.ProviderPoolMember{{ID: "member", Source: "local", Model: "org/" + id}}})
	}
	if err := config.SaveProviderPools(pools); err != nil {
		t.Fatal(err)
	}
	for listener, handler := range map[string]http.Handler{"console": s.routes(), "desktop": s.externalAPIRoutes(), "public": s.publicClusterInferenceRoutes()} {
		t.Run(listener, func(t *testing.T) {
			cases := []struct {
				protocol, id, body, want string
				upload                   bool
			}{
				{"decisions", "laya", `"input":"I need a refund.","questions":[{"type":"predicate","name":"refund","instructions":"Refund requested?"}]`, `"probability":0.9`, false},
				{"embeddings", "embedding", `"input":"hello"`, `"embedding":[0.1,0.2]`, false},
				{"images/generations", "image", `"prompt":"a cat","response_format":"b64_json"`, `ZmFrZS1wbmc=`, false},
				{"images/edits", "edit", "", `ZmFrZS1wbmc=`, true},
				{"audio/transcriptions", "asr", "", `recognized audio`, true},
				{"audio/speech", "speech", `"input":"hello","response_format":"wav"`, "RIFF\x00\xffaudio", false},
			}
			for _, tc := range cases {
				t.Run(tc.protocol, func(t *testing.T) {
					var body bytes.Buffer
					contentType := "application/json"
					if tc.upload {
						form := multipart.NewWriter(&body)
						_ = form.WriteField("model", "service-"+tc.id)
						_ = form.WriteField("prompt", "add color")
						name := "image"
						if tc.id == "asr" {
							name = "file"
						}
						part, err := form.CreateFormFile(name, "input.bin")
						if err != nil {
							t.Fatal(err)
						}
						_, _ = part.Write([]byte{0, 255, 1, 13, 10})
						_ = form.Close()
						contentType = form.FormDataContentType()
					} else {
						body.WriteString(`{"model":"service-` + tc.id + `",` + tc.body + `}`)
					}
					r := httptest.NewRequest("POST", "/ai-gateway/v1/"+tc.protocol, &body)
					r.Header.Set("Content-Type", contentType)
					r.Header.Set("Authorization", "Bearer "+key)
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, r)
					if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) {
						t.Fatalf("%d %s", w.Code, w.Body)
					}
					observationID := w.Header().Get(observabilityRequestIDHeader)
					if observationID == "" {
						t.Fatal("inference response has no observation ID")
					}
					observation, err := s.observability.GetRequest(r.Context(), observationID)
					if err != nil {
						t.Fatal(err)
					}
					if observation.Model != "service-"+tc.id || observation.Protocol != tc.protocol || observation.PoolID != tc.id || observation.MemberModel != "org/"+tc.id {
						t.Fatalf("inference observation missing routing metadata: %+v", observation)
					}
					if tc.upload && (!strings.Contains(observation.RequestBody, `"_files"`) || !strings.Contains(observation.RequestBody, `"bytes":5`)) {
						t.Fatalf("missing upload metadata: %s", observation.RequestBody)
					}
					if observationOmitsResponsePayload(tc.protocol) && (!strings.Contains(observation.ResponseBody, `"payload_omitted":true`) || strings.Contains(observation.ResponseBody, tc.want)) {
						t.Fatalf("generated media was persisted: %q", observation.ResponseBody)
					}
					if tc.id == "embedding" && (observation.InputTokens != 2 || observation.OutputTokens != 0) {
						t.Fatalf("embedding token usage missing: %+v", observation)
					}
					if tc.id == "laya" && (!strings.Contains(w.Body.String(), `"model":"service-laya"`) || strings.Contains(w.Body.String(), `org/laya`)) {
						t.Fatal("runtime alias leaked", w.Body.String())
					}
					if tc.id == "speech" && w.Header().Get("Content-Type") != "audio/wav" {
						t.Fatal("lost audio content type")
					}
				})
			}
			w := gatewayRequest(handler, "POST", "/ai-gateway/v1/chat/completions", `{"model":"service-laya","messages":[{"role":"user","content":"hi"}]}`, key)
			if w.Code != 400 {
				t.Fatalf("decision-only model accepted chat: %d %s", w.Code, w.Body)
			}
		})
	}
	usage, err := s.apiUsage.List(config.APIUsageListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var decisions int64
	for _, item := range usage.Records {
		if item.PoolID == "laya" {
			decisions += item.Requests
			if item.Model != "service-laya" || item.InputTokens != 120 {
				t.Fatal("usage lost service model or duplicated tokens", item)
			}
		}
	}
	if decisions != 3 {
		t.Fatalf("decision requests recorded %d times, want one per listener", decisions)
	}
	for _, p := range pools {
		v := s.providerPoolView(p)
		if p.ID == "laya" && (v.Category != "decision" || !v.DecisionOnly || !slices.Equal(v.InferenceProtocols, []string{"decisions"})) {
			t.Fatalf("stale chat category was not corrected: %+v", v)
		}
	}
}

func TestInferenceHandlerStreamingAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stopped := make(chan struct{})
	response, err := callInferenceHandler(ctx, func(w http.ResponseWriter, r *http.Request) {
		defer close(stopped)
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}, httptest.NewRequest("POST", "/v1/audio/speech", nil))
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, 5)
	if _, err := io.ReadFull(response.Body, first); err != nil || string(first) != "first" {
		t.Fatal(string(first), err)
	}
	_ = response.Body.Close()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("closing response did not cancel inference")
	}
}

func TestPoolProtocolCapabilitiesIntersectAndIgnoreUploadBytes(t *testing.T) {
	if got := protocolRequestTokens(bytes.Repeat([]byte{255}, 1<<20)); got != 1 {
		t.Fatal("binary upload counted as tokens", got)
	}
	if got := providerPoolActualTokens([]byte(`{"usage":{"input_tokens":40,"output_tokens":2}}`), 1); got != 42 {
		t.Fatal(got)
	}
	if got := modelServiceProtocols("image", api.ModelInfo{PipelineTag: "image-to-image"}, "local"); !slices.Equal(got, []string{"images/edits"}) {
		t.Fatal(got)
	}
	for _, category := range []string{"asr", "tts", "decision"} {
		if got := modelServiceProtocols(category, api.ModelInfo{}, "provider:x"); len(got) != 0 {
			t.Fatal("advertised unsupported provider API", got)
		}
	}
}

func TestGatewayPreservesLargeDirectUploadsWithoutRelaxingServiceLimits(t *testing.T) {
	s := newTestServer(t)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "recording.wav")
	if err != nil {
		t.Fatal(err)
	}
	const fileSize = (32 << 20) + 1024
	if _, err := io.CopyN(part, zeroReader{}, fileSize); err != nil {
		t.Fatal(err)
	}
	// The target may follow the large file; no assumption about form order.
	_ = form.WriteField("model", "large-asr")
	_ = form.Close()
	data := body.Bytes()
	calls := 0
	next := func(w http.ResponseWriter, r *http.Request) {
		calls++
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		part, err := reader.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		size, err := io.Copy(io.Discard, part)
		if err != nil || size != fileSize {
			t.Error("upload changed", size, err)
		}
		w.WriteHeader(200)
	}
	for _, allowDirect := range []bool{true, false} {
		r := httptest.NewRequest("POST", "/ai-gateway/v1/audio/transcriptions", bytes.NewReader(data))
		r.Header.Set("Content-Type", form.FormDataContentType())
		w := httptest.NewRecorder()
		s.withGatewayProtocol(next, allowDirect)(w, r)
		want := 413
		if allowDirect {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("direct %v: %d %s", allowDirect, w.Code, w.Body)
		}
	}
	if calls != 1 {
		t.Fatal("public request escaped the limit", calls)
	}
	files, err := filepath.Glob(filepath.Join(s.cfg.TempDir(), "gateway-upload-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("upload spool leaked", files, err)
	}
}
