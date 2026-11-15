package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
)

// Pool auxiliary protocols reuse the existing admission, failover, affinity and
// accounting machinery while preserving each endpoint's original handler.
func (s *Server) servePoolProtocol(w http.ResponseWriter, r *http.Request, pool config.ProviderPool, next http.HandlerFunc, data []byte) {
	members := make([]providerPoolEngineMember, 0, len(pool.Members))
	for _, member := range pool.Members {
		member := member
		if s.providerPoolSourceState(member) != "configured" {
			member.Disabled = true
		}
		members = append(members, providerPoolEngineMember{member: member, new: func() (inference.Engine, error) {
			return &protocolMemberEngine{model: member.Model, call: func(ctx context.Context) (*http.Response, error) {
				var localChoice *cluster.LocalChoice
				body, contentType, err := inference.RewriteModelRequest(data, r.Header.Get("Content-Type"), member.Model, member.Source)
				if err != nil {
					return nil, err
				}
				path := providerRouteLegacyPath(r.URL.Path)
				source := member.Source
				if cluster.IsClusterSource(source) {
					if s.cluster == nil {
						return nil, inference.NewHTTPStatusError(503, "cluster is not available")
					}
					body, contentType, err = inference.RewriteModelRequest(body, contentType, member.Model, "local")
					if err != nil {
						return nil, err
					}
					headers := make(http.Header)
					headers.Set("Accept", r.Header.Get("Accept"))
					headers.Set("Content-Type", contentType)
					response, routeErr := s.cluster.RouteRaw(ctx, member.Model, member.Source, path, body, headers)
					var choice *cluster.LocalChoice
					if errors.As(routeErr, &choice) {
						localChoice = choice
					} else {
						return response, routeErr
					}
					source = "local"
				}
				ctx = withProviderRouteSourceContext(ctx, source)
				ctx = context.WithValue(ctx, skipMemberUsageKey{}, true)
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body))
				if err != nil {
					localChoice.Release()
					return nil, err
				}
				req.Header = r.Header.Clone()
				req.Header.Set("Content-Type", contentType)
				response, err := callInferenceHandler(ctx, next, req)
				if err != nil {
					localChoice.Release()
				} else if localChoice != nil {
					// A streaming handler returns its headers before inference ends.
					// Retain the cluster reservation until the response is closed.
					response.Body = &cancelInferenceBody{ReadCloser: response.Body, cancel: localChoice.Release}
				}
				return response, err
			}}, nil
		}})
	}
	engine := &providerPoolEngine{poolID: pool.ID, poolName: pool.Name, modelID: pool.Model, policy: config.NormalizeProviderPoolPolicy(pool.Policy), members: members,
		mu: &s.poolMu, current: s.poolCurrent, runtime: s.poolRuntime, affinity: s.poolAffinity, affinityKey: providerPoolUsageCaptureFromContext(r.Context()).affinityKey(), usage: providerPoolUsageCaptureFromContext(r.Context())}
	decisions := strings.HasSuffix(r.URL.Path, "/decisions")
	response, err := engine.runProxy(r.Context(), protocolRequestTokens(data), decisions, func(eng inference.Engine) (*http.Response, error) {
		return eng.(*protocolMemberEngine).call(r.Context())
	})
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	defer response.Body.Close()
	input, output := 0, 0
	if decisions {
		// Decisions are a bounded JSON result, never a stream. Report the public
		// service ID just as the text gateway does, hiding the runtime alias.
		body, err := io.ReadAll(response.Body)
		if err != nil {
			writeOpenAIInferenceError(w, err)
			return
		}
		usage := observationResponseUsageFromBodies(body)
		input, output = int(usage.inputTokens), int(usage.outputTokens)
		var payload map[string]json.RawMessage
		if json.Unmarshal(body, &payload) == nil && payload != nil {
			payload["model"], _ = json.Marshal(pool.Model)
			body, _ = json.Marshal(payload)
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
	}
	for key, values := range response.Header {
		// The outer observation belongs to this gateway's store. A worker's
		// observation ID must not replace the ID clients use to find it here.
		if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Connection") ||
			(strings.EqualFold(key, observabilityRequestIDHeader) && w.Header().Get(observabilityRequestIDHeader) != "") {
			continue
		}
		w.Header()[key] = append([]string(nil), values...)
	}
	w.WriteHeader(response.StatusCode)
	destination := io.Writer(w)
	if f, ok := w.(http.Flusher); ok {
		destination = &flushingWriter{writer: destination, flush: f}
	}
	if _, err := io.Copy(destination, response.Body); err != nil {
		return
	}
	s.recordAPIUsage(r, pool.Model, poolSource(pool.ID), input, output)
}

// Uploaded image/audio bytes are not tokens. JSON protocols estimate only
// their text and decision questions, retaining the pool's admission limits.
func protocolRequestTokens(data []byte) int {
	var body map[string]interface{}
	if json.Unmarshal(data, &body) != nil {
		return 1
	}
	return estimateProviderPoolRequestTokens(body) + estimateProviderPoolValueTokens(body["questions"])
}

type protocolMemberEngine struct {
	model string
	call  func(context.Context) (*http.Response, error)
}

func (e *protocolMemberEngine) ModelName() string { return e.model }
func (e *protocolMemberEngine) Close() error      { return nil }
func (e *protocolMemberEngine) Generate(context.Context, string, inference.Options, inference.TokenCallback) (string, error) {
	return "", fmt.Errorf("use the model's inference protocol")
}
func (e *protocolMemberEngine) Chat(context.Context, []inference.Message, inference.Options, inference.TokenCallback) (string, error) {
	return "", fmt.Errorf("use the model's inference protocol")
}

type skipMemberUsageKey struct{}
type flushingWriter struct {
	writer io.Writer
	flush  http.Flusher
}

func (w *flushingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.flush.Flush()
	return n, err
}

type inferencePipeWriter struct {
	header  http.Header
	writer  *io.PipeWriter
	started chan *http.Response
	once    sync.Once
}

func (w *inferencePipeWriter) Header() http.Header { return w.header }
func (w *inferencePipeWriter) WriteHeader(status int) {
	w.once.Do(func() { w.started <- &http.Response{StatusCode: status, Header: w.header.Clone()} })
}
func (w *inferencePipeWriter) Write(p []byte) (int, error) {
	w.WriteHeader(200)
	return w.writer.Write(p)
}
func (w *inferencePipeWriter) Flush() { w.WriteHeader(200) }

// Closing the response cancels a running local/cloud operation and releases the
// pipe writer, including when a public client disconnects during audio output.
func callInferenceHandler(ctx context.Context, handler http.HandlerFunc, req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	out := &inferencePipeWriter{header: make(http.Header), writer: writer, started: make(chan *http.Response, 1)}
	go func() {
		defer writer.Close()
		defer func() {
			if recovered := recover(); recovered != nil {
				_ = writer.CloseWithError(fmt.Errorf("inference handler failed"))
				out.WriteHeader(500)
			}
		}()
		handler(out, req.WithContext(ctx))
		out.WriteHeader(200)
	}()
	select {
	case response := <-out.started:
		response.Body = &cancelInferenceBody{ReadCloser: reader, cancel: cancel}
		return response, nil
	case <-ctx.Done():
		cancel()
		_ = reader.Close()
		return nil, ctx.Err()
	}
}

type cancelInferenceBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelInferenceBody) Close() error { b.cancel(); return b.ReadCloser.Close() }
