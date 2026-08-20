package server

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/correlation"
)

func TestCorrelationMiddlewareCoversRejectedRequests(t *testing.T) {
	handler := correlationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values, ok := correlation.FromContext(r.Context())
		if !ok || values.RequestID != "gateway-request" {
			t.Fatalf("correlation context = %+v, ok=%t", values, ok)
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	req.Header.Set(correlation.RequestIDHeader, "gateway-request")
	req.Header.Set(correlation.B3TraceIDHeader, "463AC35C9F6413AD")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder.Header().Get(correlation.RequestIDHeader) != "gateway-request" {
		t.Fatalf("request ID = %q", recorder.Header().Get(correlation.RequestIDHeader))
	}
	if recorder.Header().Get(correlation.B3TraceIDHeader) != "463ac35c9f6413ad" {
		t.Fatalf("B3 trace ID = %q", recorder.Header().Get(correlation.B3TraceIDHeader))
	}
}

func TestCORSAllowsAndExposesCorrelationHeaders(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodOptions, "/api/chat", nil)
	req.Header.Set("Origin", "https://example.test")
	recorder := httptest.NewRecorder()

	s.corsMiddleware(http.NotFoundHandler()).ServeHTTP(recorder, req)

	if !strings.Contains(recorder.Header().Get("Access-Control-Allow-Headers"), correlation.RequestIDHeader) ||
		!strings.Contains(recorder.Header().Get("Access-Control-Allow-Headers"), correlation.B3TraceIDHeader) {
		t.Fatalf("allow headers = %q", recorder.Header().Get("Access-Control-Allow-Headers"))
	}
	if !strings.Contains(recorder.Header().Get("Access-Control-Expose-Headers"), correlation.RequestIDHeader) ||
		!strings.Contains(recorder.Header().Get("Access-Control-Expose-Headers"), correlation.B3TraceIDHeader) {
		t.Fatalf("expose headers = %q", recorder.Header().Get("Access-Control-Expose-Headers"))
	}
}

func TestRequestLogIncludesCorrelationIdentifiers(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })

	handler := correlationMiddleware(LogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})))
	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	req.Header.Set(correlation.RequestIDHeader, "gateway-request")
	req.Header.Set(correlation.B3TraceIDHeader, "463ac35c9f6413ad")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	logged := output.String()
	if !strings.Contains(logged, `status=202`) ||
		!strings.Contains(logged, `request_id="gateway-request"`) ||
		!strings.Contains(logged, `trace_id="463ac35c9f6413ad"`) {
		t.Fatalf("request log = %q", logged)
	}
}
