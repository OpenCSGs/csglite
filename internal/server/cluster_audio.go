package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/asr"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/tts"
	"github.com/opencsgs/csglite/pkg/api"
)

// Speech recognition and synthesis take the same road as chat: the handler
// asks for an engine by model and source, and the cluster is one more kind of
// engine. A cluster-backed engine forwards the call to the member the
// scheduler picks and falls back to the local runtime when this node is the
// best choice.

// routedNodeReporter is implemented by cluster-backed engines so a handler can
// tell the caller which node executed the request.
type routedNodeReporter interface {
	RoutedNode() (uuid, name string)
}

// setRoutedNodeHeaders adds the executing-node headers when eng is routed.
func setRoutedNodeHeaders(w http.ResponseWriter, eng any) {
	rep, ok := eng.(routedNodeReporter)
	if !ok {
		return
	}
	uuid, name := rep.RoutedNode()
	if uuid == "" {
		return
	}
	w.Header().Set(cluster.NodeHeader, uuid)
	w.Header().Set(cluster.NodeNameHeader, name)
}

// getASREngine resolves a speech-recognition engine by model and source with
// the same precedence as getChatEngine: an explicit cluster source, then the
// cluster for models this node lacks (or every model in balanced mode), then
// the local runtime, then a peer when the local load fails.
func (s *Server) getASREngine(ctx context.Context, modelID, source string) (asr.Engine, error) {
	source = strings.TrimSpace(source)
	if cluster.IsClusterSource(source) {
		if s.cluster == nil {
			return nil, inference.NewHTTPStatusError(http.StatusNotFound, "the cluster feature is disabled on this node")
		}
		return &clusterASREngine{s: s, model: modelID, source: source}, nil
	}
	if source == "" && s.clusterRoutingWanted(modelID) {
		return &clusterASREngine{s: s, model: modelID, source: cluster.SourceCluster}, nil
	}
	eng, err := s.getOrLoadASREngine(ctx, modelID)
	if err == nil {
		return eng, nil
	}
	if !strings.EqualFold(source, "local") && s.cluster != nil && s.cluster.RemoteHasModel(modelID) {
		return &clusterASREngine{s: s, model: modelID, source: cluster.SourceCluster}, nil
	}
	return nil, err
}

// getTTSEngine is getASREngine for speech synthesis.
func (s *Server) getTTSEngine(ctx context.Context, modelID, source string) (tts.Engine, error) {
	source = strings.TrimSpace(source)
	if cluster.IsClusterSource(source) {
		if s.cluster == nil {
			return nil, inference.NewHTTPStatusError(http.StatusNotFound, "the cluster feature is disabled on this node")
		}
		return &clusterTTSEngine{s: s, model: modelID, source: source}, nil
	}
	if source == "" && s.clusterRoutingWanted(modelID) {
		return &clusterTTSEngine{s: s, model: modelID, source: cluster.SourceCluster}, nil
	}
	eng, err := s.getOrLoadTTSEngine(ctx, modelID)
	if err == nil {
		return eng, nil
	}
	if !strings.EqualFold(source, "local") && s.cluster != nil && s.cluster.RemoteHasModel(modelID) {
		return &clusterTTSEngine{s: s, model: modelID, source: cluster.SourceCluster}, nil
	}
	return nil, err
}

// routedNode remembers which member answered the last call.
type routedNode struct {
	mu   sync.Mutex
	uuid string
	name string
}

func (r *routedNode) set(h http.Header) {
	r.mu.Lock()
	r.uuid = h.Get(cluster.NodeHeader)
	r.name = h.Get(cluster.NodeNameHeader)
	r.mu.Unlock()
}

func (r *routedNode) get() (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.uuid, r.name
}

// routeOrLocal forwards through the cluster or reports that the scheduler
// chose this node.
func (s *Server) routeOrLocal(ctx context.Context, modelID, source, path string, body []byte, contentType, accept string) (*http.Response, bool, error) {
	if key := providerPoolUsageCaptureFromContext(ctx).affinityKey(); key != "" {
		ctx = cluster.WithAffinityKey(ctx, key)
	}
	headers := http.Header{}
	headers.Set("Content-Type", contentType)
	if accept != "" {
		headers.Set("Accept", accept)
	}
	resp, err := s.cluster.RouteRaw(ctx, modelID, source, path, body, headers)
	if errors.Is(err, cluster.ErrServeLocally) {
		return nil, true, nil
	}
	return resp, false, err
}

// ---- speech recognition ----

type clusterASREngine struct {
	s      *Server
	model  string
	source string
	node   routedNode
}

func (e *clusterASREngine) ModelName() string            { return e.model }
func (e *clusterASREngine) Close() error                 { return nil }
func (e *clusterASREngine) RoutedNode() (string, string) { return e.node.get() }

// transcriptionForm rebuilds the multipart upload from the saved file.
func transcriptionForm(req api.OpenAIAudioTranscriptionRequest, stream bool) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	file, err := os.Open(req.FilePath)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	part, err := mw.CreateFormFile("file", filepath.Base(req.FilePath))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, "", err
	}
	fields := map[string]string{
		"model":           req.Model,
		"source":          "local",
		"language":        req.Language,
		"prompt":          req.Prompt,
		"response_format": "verbose_json",
	}
	if stream {
		fields["stream"] = "true"
	}
	if req.Temperature != nil {
		fields["temperature"] = strconv.FormatFloat(*req.Temperature, 'f', -1, 64)
	}
	if req.ITN != nil {
		fields["itn"] = strconv.FormatBool(*req.ITN)
	}
	if len(req.Hotwords) > 0 {
		fields["hotwords"] = strings.Join(req.Hotwords, ",")
	}
	for k, v := range fields {
		if v == "" {
			continue
		}
		if err := mw.WriteField(k, v); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

func (e *clusterASREngine) Transcribe(ctx context.Context, req api.OpenAIAudioTranscriptionRequest) (*api.OpenAIAudioTranscriptionResponse, error) {
	body, contentType, err := transcriptionForm(req, false)
	if err != nil {
		return nil, err
	}
	resp, local, err := e.s.routeOrLocal(ctx, e.model, e.source, "/v1/audio/transcriptions", body, contentType, "application/json")
	if err != nil {
		return nil, err
	}
	if local {
		eng, err := e.s.getOrLoadASREngine(ctx, e.model)
		if err != nil {
			return nil, err
		}
		return eng.Transcribe(ctx, req)
	}
	defer resp.Body.Close()
	e.node.set(resp.Header)
	var out api.OpenAIAudioTranscriptionResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding transcription from node: %w", err)
	}
	return &out, nil
}

func (e *clusterASREngine) TranscribeStream(ctx context.Context, req api.OpenAIAudioTranscriptionRequest, onChunk func(api.OpenAIAudioTranscriptionResponse) error) error {
	body, contentType, err := transcriptionForm(req, true)
	if err != nil {
		return err
	}
	resp, local, err := e.s.routeOrLocal(ctx, e.model, e.source, "/v1/audio/transcriptions", body, contentType, "text/event-stream")
	if err != nil {
		return err
	}
	if local {
		eng, err := e.s.getOrLoadASREngine(ctx, e.model)
		if err != nil {
			return err
		}
		return eng.TranscribeStream(ctx, req, onChunk)
	}
	defer resp.Body.Close()
	e.node.set(resp.Header)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event struct {
			Response *api.OpenAIAudioTranscriptionResponse `json:"response"`
			Error    string                                `json:"error"`
			Done     bool                                  `json:"done"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) != nil {
			continue
		}
		if event.Error != "" {
			return errors.New(event.Error)
		}
		if event.Done {
			return nil
		}
		if event.Response != nil {
			if err := onChunk(*event.Response); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

// ---- speech synthesis ----

type clusterTTSEngine struct {
	s      *Server
	model  string
	source string
	node   routedNode
}

func (e *clusterTTSEngine) ModelName() string            { return e.model }
func (e *clusterTTSEngine) Close() error                 { return nil }
func (e *clusterTTSEngine) RoutedNode() (string, string) { return e.node.get() }

// Info is answered by the local runtime; voice lists are model facts, not
// node facts, so any copy of the model will do.
func (e *clusterTTSEngine) Info(ctx context.Context) (*api.SpeechVoicesResponse, error) {
	eng, err := e.s.getOrLoadTTSEngine(ctx, e.model)
	if err != nil {
		return nil, err
	}
	return eng.Info(ctx)
}

func speechBody(req api.OpenAIAudioSpeechRequest, stream bool) ([]byte, error) {
	req.Source = "local"
	req.Stream = stream
	return json.Marshal(req)
}

func sampleRateFromContentType(ct string) int {
	_, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return 0
	}
	rate, _ := strconv.Atoi(params["rate"])
	return rate
}

func (e *clusterTTSEngine) Speak(ctx context.Context, req api.OpenAIAudioSpeechRequest) (*tts.Audio, error) {
	body, err := speechBody(req, false)
	if err != nil {
		return nil, err
	}
	resp, local, err := e.s.routeOrLocal(ctx, e.model, e.source, "/v1/audio/speech", body, "application/json", "")
	if err != nil {
		return nil, err
	}
	if local {
		eng, err := e.s.getOrLoadTTSEngine(ctx, e.model)
		if err != nil {
			return nil, err
		}
		return eng.Speak(ctx, req)
	}
	defer resp.Body.Close()
	e.node.set(resp.Header)
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, err
	}
	rate := sampleRateFromContentType(resp.Header.Get("Content-Type"))
	if rate == 0 {
		rate = req.SampleRate
	}
	return &tts.Audio{Data: data, Format: req.ResponseFormat, SampleRate: rate}, nil
}

func (e *clusterTTSEngine) SpeakStream(ctx context.Context, req api.OpenAIAudioSpeechRequest, onChunk func(tts.Chunk) error) error {
	body, err := speechBody(req, true)
	if err != nil {
		return err
	}
	resp, local, err := e.s.routeOrLocal(ctx, e.model, e.source, "/v1/audio/speech", body, "application/json", "")
	if err != nil {
		return err
	}
	if local {
		eng, err := e.s.getOrLoadTTSEngine(ctx, e.model)
		if err != nil {
			return err
		}
		return eng.SpeakStream(ctx, req, onChunk)
	}
	defer resp.Body.Close()
	e.node.set(resp.Header)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if err := onChunk(tts.Chunk{Data: append([]byte(nil), buf[:n]...)}); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return onChunk(tts.Chunk{Done: true})
		}
		if readErr != nil {
			return readErr
		}
	}
}

// isRoutedEngine reports whether eng runs on another node, so a failure must
// not evict a local worker.
func isRoutedEngine(eng any) bool {
	_, ok := eng.(routedNodeReporter)
	return ok
}
