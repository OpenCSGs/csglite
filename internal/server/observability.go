package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/observability"
)

const (
	observabilityRequestIDHeader = "X-CSGLite-Request-ID"
	observabilityTraceIDHeader   = "X-CSGLite-Trace-ID"
	observabilityThreadIDHeader  = "X-CSGLite-Thread-ID"
	observabilityBodyLimit       = 1024 * 1024
	observabilityUsageTailLimit  = 64 * 1024
)

type observationContextKey struct{}

type observationMetadata struct {
	mu           sync.Mutex
	model        string
	source       string
	sourceType   string
	sourceName   string
	pool         *apiUsagePoolMetadata
	inputTokens  int64
	outputTokens int64
}

type observationMetadataSnapshot struct {
	model        string
	source       string
	sourceType   string
	sourceName   string
	pool         *apiUsagePoolMetadata
	inputTokens  int64
	outputTokens int64
}

func observationFromContext(ctx context.Context) *observationMetadata {
	metadata, _ := ctx.Value(observationContextKey{}).(*observationMetadata)
	return metadata
}

func (m *observationMetadata) setUsage(model, source, sourceType, sourceName string, inputTokens, outputTokens int64, pool *apiUsagePoolMetadata) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.model = strings.TrimSpace(model)
	m.source = strings.TrimSpace(source)
	m.sourceType = strings.TrimSpace(sourceType)
	m.sourceName = strings.TrimSpace(sourceName)
	m.inputTokens = inputTokens
	m.outputTokens = outputTokens
	if pool != nil {
		copy := *pool
		m.pool = &copy
	}
}

func (m *observationMetadata) snapshot() observationMetadataSnapshot {
	if m == nil {
		return observationMetadataSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := observationMetadataSnapshot{
		model:        m.model,
		source:       m.source,
		sourceType:   m.sourceType,
		sourceName:   m.sourceName,
		inputTokens:  m.inputTokens,
		outputTokens: m.outputTokens,
	}
	if m.pool != nil {
		pool := *m.pool
		snapshot.pool = &pool
	}
	return snapshot
}

type observationResponseWriter struct {
	http.ResponseWriter
	status     int
	firstWrite time.Time
	body       bytes.Buffer
	usageTail  []byte
	truncated  bool
}

func (w *observationResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *observationResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.firstWrite.IsZero() {
		w.firstWrite = time.Now()
	}
	w.capture(p)
	return w.ResponseWriter.Write(p)
}

func (w *observationResponseWriter) capture(p []byte) {
	w.captureUsageTail(p)
	remaining := observabilityBodyLimit - w.body.Len()
	if remaining <= 0 {
		w.truncated = true
		return
	}
	if len(p) > remaining {
		_, _ = w.body.Write(p[:remaining])
		w.truncated = true
		return
	}
	_, _ = w.body.Write(p)
}

func (w *observationResponseWriter) captureUsageTail(p []byte) {
	if len(p) >= observabilityUsageTailLimit {
		w.usageTail = append(w.usageTail[:0], p[len(p)-observabilityUsageTailLimit:]...)
		return
	}
	overflow := len(w.usageTail) + len(p) - observabilityUsageTailLimit
	if overflow > 0 {
		copy(w.usageTail, w.usageTail[overflow:])
		w.usageTail = w.usageTail[:len(w.usageTail)-overflow]
	}
	w.usageTail = append(w.usageTail, p...)
}

func (w *observationResponseWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *observationResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *observationResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (s *Server) observabilityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isObservedTextGenerationRequest(r) {
			next.ServeHTTP(w, r)
			return
		}

		startedAt := time.Now().UTC()
		requestID := newObservationID("req")
		traceID := normalizedObservationID(r.Header.Get(observabilityTraceIDHeader))
		if traceID == "" {
			traceID = newObservationID("trace")
		}
		threadID := normalizedObservationID(r.Header.Get(observabilityThreadIDHeader))
		if threadID == "" {
			threadID = newObservationID("thread")
		}
		w.Header().Set(observabilityRequestIDHeader, requestID)
		w.Header().Set(observabilityTraceIDHeader, traceID)
		w.Header().Set(observabilityThreadIDHeader, threadID)

		requestBody, err := io.ReadAll(r.Body)
		if err != nil {
			r.Body = io.NopCloser(bytes.NewReader(requestBody))
			next.ServeHTTP(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(requestBody))
		sanitizedRequest := sanitizeObservationBody(requestBody)
		capturedRequest, requestTruncated := limitedBody(sanitizedRequest)

		metadata := &observationMetadata{}
		ctx := context.WithValue(r.Context(), observationContextKey{}, metadata)
		ctx, cacheCollector := inference.WithCacheUsageCollector(ctx)
		ow := &observationResponseWriter{ResponseWriter: w}
		next.ServeHTTP(ow, r.WithContext(ctx))

		completedAt := time.Now().UTC()
		if ow.status == 0 {
			ow.status = http.StatusOK
		}
		snapshot := metadata.snapshot()
		if snapshot.model == "" {
			snapshot.model, snapshot.source = observationRequestModelAndSource(requestBody)
		}
		if snapshot.sourceType == "" {
			resolvedSource, sourceType, sourceName := s.resolveAPIUsageSource(context.Background(), snapshot.model, snapshot.source)
			snapshot.source, snapshot.sourceType, snapshot.sourceName = resolvedSource, sourceType, sourceName
		}
		keyID, keyName := apiUsageBuiltinKeyID, apiUsageBuiltinKeyName
		if key, ok := authenticatedAPIKey(r.WithContext(ctx)); ok {
			keyID, keyName = key.ID, key.Name
		}
		status := "completed"
		if ow.status >= http.StatusBadRequest {
			status = "failed"
		}
		responseBody := sanitizeObservationBody(ow.body.Bytes())
		responseUsage := observationResponseUsageFromBodies(ow.body.Bytes(), ow.usageTail)
		hasMeaningfulTokenUsage := responseUsage.inputTokens > 0 ||
			responseUsage.outputTokens > 0 ||
			responseUsage.totalTokens > 0
		if hasMeaningfulTokenUsage {
			if responseUsage.hasInputTokens {
				snapshot.inputTokens = responseUsage.inputTokens
			}
			if responseUsage.hasOutputTokens {
				snapshot.outputTokens = responseUsage.outputTokens
			}
		}
		inferenceCacheUsage := cacheCollector.Snapshot()
		if !responseUsage.hasCacheUsage || responseUsage.eligibleTokens <= 0 {
			responseUsage.readTokens = inferenceCacheUsage.ReadInputTokens
			responseUsage.creationTokens = inferenceCacheUsage.CreationInputTokens
			responseUsage.eligibleTokens = inferenceCacheUsage.EligibleInputTokens
		}
		snapshot.inputTokens = observationMax64(snapshot.inputTokens, responseUsage.eligibleTokens)
		record := observability.RequestRecord{
			ID:                    requestID,
			TraceID:               traceID,
			ThreadID:              threadID,
			StartedAt:             startedAt,
			CompletedAt:           completedAt,
			Method:                r.Method,
			Path:                  r.URL.Path,
			Protocol:              observationProtocol(r.URL.Path),
			Status:                status,
			StatusCode:            ow.status,
			Stream:                observationRequestStreams(requestBody),
			Model:                 snapshot.model,
			Source:                snapshot.source,
			SourceType:            snapshot.sourceType,
			SourceName:            snapshot.sourceName,
			APIKeyID:              keyID,
			APIKeyName:            keyName,
			InputTokens:           snapshot.inputTokens,
			OutputTokens:          snapshot.outputTokens,
			CacheReadInputTokens:  responseUsage.readTokens,
			CacheCreationTokens:   responseUsage.creationTokens,
			CacheEligibleTokens:   responseUsage.eligibleTokens,
			DurationMS:            completedAt.Sub(startedAt).Milliseconds(),
			RequestBody:           string(capturedRequest),
			ResponseBody:          string(responseBody),
			RequestBodyTruncated:  requestTruncated,
			ResponseBodyTruncated: ow.truncated,
		}
		if !ow.firstWrite.IsZero() {
			record.FirstTokenLatencyMS = ow.firstWrite.Sub(startedAt).Milliseconds()
		}
		if snapshot.pool != nil {
			record.PoolID = snapshot.pool.PoolID
			record.PoolName = snapshot.pool.PoolName
			record.PoolModel = snapshot.pool.PoolModel
			record.MemberModel = snapshot.pool.MemberModel
			record.FallbackCount = snapshot.pool.FallbackCount
			record.LimitedCount = snapshot.pool.LimitedCount
		}
		if status == "failed" {
			record.ErrorMessage = observationErrorMessage(responseBody)
		}
		if err := s.addObservation(record); err != nil {
			log.Printf("OBSERVABILITY: save request %s failed: %v", requestID, err)
		}
	})
}

func (s *Server) addObservation(record observability.RequestRecord) error {
	s.observabilityMu.RLock()
	defer s.observabilityMu.RUnlock()
	if s.observability == nil {
		return nil
	}
	if err := s.observability.Add(context.Background(), record); err != nil {
		return err
	}
	now := time.Now().Unix()
	nextCleanup := s.observabilityCleanupAt.Load()
	if now >= nextCleanup && s.observabilityCleanupAt.CompareAndSwap(nextCleanup, now+int64((6*time.Hour)/time.Second)) {
		if _, err := s.observability.Cleanup(context.Background(), config.ObservabilityRetentionDays(s.cfg.Observability)); err != nil {
			log.Printf("OBSERVABILITY: retention cleanup failed: %v", err)
		}
	}
	return nil
}

func isObservedTextGenerationRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/api/chat", "/api/generate", "/v1/chat/completions", "/v1/responses",
		"/v1/messages", "/anthropic/messages", "/anthropic/v1/messages":
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/providers/") &&
		(strings.HasSuffix(r.URL.Path, "/v1/chat/completions") ||
			strings.HasSuffix(r.URL.Path, "/v1/responses") ||
			strings.HasSuffix(r.URL.Path, "/v1/messages"))
}

func observationProtocol(path string) string {
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		return "openai"
	case strings.HasSuffix(path, "/responses"):
		return "responses"
	case strings.HasSuffix(path, "/messages"):
		return "anthropic"
	default:
		return "lite"
	}
}

func observationRequestModelAndSource(body []byte) (string, string) {
	var value struct {
		Model  string `json:"model"`
		Source string `json:"source"`
	}
	_ = json.Unmarshal(body, &value)
	return strings.TrimSpace(value.Model), strings.TrimSpace(value.Source)
}

type observationResponseUsage struct {
	inputTokens     int64
	outputTokens    int64
	totalTokens     int64
	readTokens      int64
	creationTokens  int64
	eligibleTokens  int64
	hasInputTokens  bool
	hasOutputTokens bool
	hasTotalTokens  bool
	hasCacheUsage   bool
}

func observationResponseUsageFromBodies(bodies ...[]byte) observationResponseUsage {
	var result observationResponseUsage
	for _, body := range bodies {
		collectObservationResponseBodyUsage(body, &result)
	}
	if result.hasTotalTokens {
		switch {
		case result.hasInputTokens && !result.hasOutputTokens && result.totalTokens >= result.inputTokens:
			result.outputTokens = result.totalTokens - result.inputTokens
			result.hasOutputTokens = true
		case result.hasOutputTokens && !result.hasInputTokens && result.totalTokens >= result.outputTokens:
			result.inputTokens = result.totalTokens - result.outputTokens
			result.hasInputTokens = true
		}
	}
	return result
}

func collectObservationResponseBodyUsage(body []byte, result *observationResponseUsage) {
	var value any
	if json.Unmarshal(body, &value) == nil {
		collectObservationResponseUsage(value, result)
		return
	}
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("data:")) {
			line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		}
		if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
			continue
		}
		value = nil
		if json.Unmarshal(line, &value) == nil {
			collectObservationResponseUsage(value, result)
		}
	}
}

func collectObservationResponseUsage(value any, result *observationResponseUsage) {
	switch typed := value.(type) {
	case map[string]any:
		updateObservationResponseUsage(typed, result)
		for _, child := range typed {
			collectObservationResponseUsage(child, result)
		}
	case []any:
		for _, child := range typed {
			collectObservationResponseUsage(child, result)
		}
	}
}

func updateObservationResponseUsage(value map[string]any, result *observationResponseUsage) {
	inputTokens, hasInputTokens := observationJSONInt(value, "input_tokens", "prompt_tokens")
	outputTokens, hasOutputTokens := observationJSONInt(value, "output_tokens", "completion_tokens")
	totalTokens, hasTotalTokens := observationJSONInt(value, "total_tokens")
	if hasInputTokens {
		result.inputTokens = observationMax64(result.inputTokens, inputTokens)
		result.hasInputTokens = true
	}
	if hasOutputTokens {
		result.outputTokens = observationMax64(result.outputTokens, outputTokens)
		result.hasOutputTokens = true
	}
	if hasTotalTokens {
		result.totalTokens = observationMax64(result.totalTokens, totalTokens)
		result.hasTotalTokens = true
	}
	readTokens, hasAnthropicRead := observationJSONInt(value, "cache_read_input_tokens", "cache_read_tokens")
	topLevelRead, hasTopLevelRead := observationJSONInt(value, "cached_tokens")
	readTokens = observationMax64(readTokens, topLevelRead)
	creationTokens, hasCreation := observationJSONInt(value, "cache_creation_input_tokens", "cache_creation_tokens", "write_cached_tokens", "cache_write_tokens")
	nestedRead, hasNestedRead := observationNestedJSONInt(value, "cached_tokens", "prompt_tokens_details", "input_tokens_details")
	if hasNestedRead {
		readTokens = observationMax64(readTokens, nestedRead)
	}
	nestedCreation := maxObservationNestedJSONInt(value, []string{"write_cached_tokens", "cache_write_tokens"}, "prompt_tokens_details", "input_tokens_details")
	if nestedCreation > 0 {
		hasCreation = true
		creationTokens = observationMax64(creationTokens, nestedCreation)
	}
	if hasAnthropicRead || hasTopLevelRead || hasNestedRead || hasCreation {
		result.hasCacheUsage = true
	}
	result.readTokens = observationMax64(result.readTokens, readTokens)
	result.creationTokens = observationMax64(result.creationTokens, creationTokens)
	switch {
	case hasAnthropicRead:
		result.eligibleTokens = observationMax64(result.eligibleTokens, inputTokens+readTokens+creationTokens)
	case hasTopLevelRead:
		switch {
		case inputTokens < 0 && readTokens > 0:
			inputTokens += 2 * readTokens
		case inputTokens >= 0 && readTokens > 0 && inputTokens < readTokens:
			inputTokens += readTokens
		}
		result.eligibleTokens = observationMax64(result.eligibleTokens, inputTokens)
	case hasNestedRead || hasCreation:
		result.eligibleTokens = observationMax64(result.eligibleTokens, inputTokens)
	}
}

func observationJSONInt(value map[string]any, keys ...string) (int64, bool) {
	for _, key := range keys {
		raw, ok := value[key]
		if !ok {
			continue
		}
		switch number := raw.(type) {
		case float64:
			return observationMax64(0, int64(number)), true
		case json.Number:
			parsed, err := number.Int64()
			return observationMax64(0, parsed), err == nil
		}
	}
	return 0, false
}

func maxObservationJSONInt(value map[string]any, keys ...string) int64 {
	var result int64
	for _, key := range keys {
		if number, ok := observationJSONInt(value, key); ok {
			result = observationMax64(result, number)
		}
	}
	return result
}

func observationNestedJSONInt(value map[string]any, target string, parents ...string) (int64, bool) {
	for _, parent := range parents {
		nested, ok := value[parent].(map[string]any)
		if !ok {
			continue
		}
		if number, found := observationJSONInt(nested, target); found {
			return number, true
		}
	}
	return 0, false
}

func maxObservationNestedJSONInt(value map[string]any, targets []string, parents ...string) int64 {
	var result int64
	for _, target := range targets {
		if number, found := observationNestedJSONInt(value, target, parents...); found {
			result = observationMax64(result, number)
		}
	}
	return result
}

func observationMax64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func observationRequestStreams(body []byte) bool {
	var value struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &value)
	return value.Stream
}

func limitedBody(body []byte) ([]byte, bool) {
	if len(body) <= observabilityBodyLimit {
		return append([]byte(nil), body...), false
	}
	return append([]byte(nil), body[:observabilityBodyLimit]...), true
}

func sanitizeObservationBody(body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	var value any
	if json.Unmarshal(body, &value) != nil {
		return redactObservationText(body)
	}
	redactObservationValue(value)
	sanitized, err := json.Marshal(value)
	if err != nil {
		return body
	}
	return sanitized
}

var observationSecretPattern = regexp.MustCompile(`(?i)("(?:authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|password|client[_-]?secret|secret)"\s*:\s*)"[^"]*"`)

func redactObservationText(body []byte) []byte {
	return observationSecretPattern.ReplaceAll(body, []byte(`${1}"[REDACTED]"`))
}

func redactObservationValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isSensitiveObservationKey(key) {
				typed[key] = "[REDACTED]"
				continue
			}
			redactObservationValue(child)
		}
	case []any:
		for _, child := range typed {
			redactObservationValue(child)
		}
	}
}

func isSensitiveObservationKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	switch normalized {
	case "authorization", "api_key", "apikey", "access_token", "refresh_token", "password", "secret", "client_secret":
		return true
	default:
		return strings.HasSuffix(normalized, "_api_key") || strings.HasSuffix(normalized, "_token")
	}
}

func observationErrorMessage(body []byte) string {
	message := strings.TrimSpace(string(body))
	var value struct {
		Error any    `json:"error"`
		Msg   string `json:"msg"`
	}
	if json.Unmarshal(body, &value) == nil {
		switch errorValue := value.Error.(type) {
		case string:
			message = errorValue
		case map[string]any:
			if text, ok := errorValue["message"].(string); ok {
				message = text
			}
		default:
			if value.Msg != "" {
				message = value.Msg
			}
		}
	}
	if len(message) > 1024 {
		message = message[:1024]
	}
	return message
}

func normalizedObservationID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("-_.:", char) {
			continue
		}
		return ""
	}
	return value
}

func newObservationID(prefix string) string {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(random)
}

func (s *Server) cleanupObservability() {
	s.observabilityMu.RLock()
	defer s.observabilityMu.RUnlock()
	if s.observability == nil {
		return
	}
	if _, err := s.observability.Cleanup(context.Background(), config.ObservabilityRetentionDays(s.cfg.Observability)); err != nil {
		log.Printf("OBSERVABILITY: retention cleanup failed: %v", err)
	}
}

func (s *Server) reopenObservability(storageRoot string) error {
	next, err := observability.Open(storageRoot)
	if err != nil {
		return err
	}
	s.observabilityMu.Lock()
	previous := s.observability
	s.observability = next
	s.observabilityMu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	s.cleanupObservability()
	return nil
}
