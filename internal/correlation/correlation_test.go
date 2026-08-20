package correlation

import (
	"context"
	"net/http"
	"testing"
)

func TestFromHeadersPreservesValidIdentifiers(t *testing.T) {
	headers := http.Header{}
	headers.Set(RequestIDHeader, "gateway-request")
	headers.Set(B3TraceIDHeader, "463ac35c9f6413ad48485a3953bb6124")
	headers.Set(TraceIDHeader, "conversation-trace")
	headers.Set(ThreadIDHeader, "thread-one")

	values := FromHeaders(headers)
	if values.RequestID != "gateway-request" ||
		values.B3TraceID != "463ac35c9f6413ad48485a3953bb6124" ||
		values.TraceID != "conversation-trace" ||
		values.ThreadID != "thread-one" {
		t.Fatalf("unexpected correlation values: %+v", values)
	}
}

func TestFromHeadersRejectsInvalidIdentifiers(t *testing.T) {
	headers := http.Header{}
	headers.Set(RequestIDHeader, "bad request")
	headers.Set(B3TraceIDHeader, "0000000000000000")
	headers.Set(TraceIDHeader, "bad/trace")

	values := FromHeaders(headers)
	if values.RequestID == "" || values.RequestID == "bad request" {
		t.Fatalf("request ID was not replaced: %q", values.RequestID)
	}
	if len(values.B3TraceID) != 32 || values.B3TraceID == "0000000000000000" {
		t.Fatalf("B3 trace ID was not replaced: %q", values.B3TraceID)
	}
	if values.TraceID == "" || values.TraceID == "bad/trace" {
		t.Fatalf("trace ID was not replaced: %q", values.TraceID)
	}
}

func TestFromHeadersUsesB3AsLogicalTraceFallback(t *testing.T) {
	headers := http.Header{}
	headers.Set(B3TraceIDHeader, "463ac35c9f6413ad")
	values := FromHeaders(headers)
	if values.TraceID != "463ac35c9f6413ad" {
		t.Fatalf("trace ID = %q", values.TraceID)
	}
}

func TestApplyRequestHeadersUsesContext(t *testing.T) {
	values := Values{
		RequestID: "request-one",
		TraceID:   "trace-one",
		B3TraceID: "463ac35c9f6413ad",
		ThreadID:  "thread-one",
	}
	req, err := http.NewRequestWithContext(WithContext(context.Background(), values), http.MethodGet, "http://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ApplyRequestHeaders(req)
	if req.Header.Get(RequestIDHeader) != values.RequestID ||
		req.Header.Get(B3TraceIDHeader) != values.B3TraceID ||
		req.Header.Get(TraceIDHeader) != values.TraceID ||
		req.Header.Get(ThreadIDHeader) != values.ThreadID {
		t.Fatalf("unexpected propagated headers: %v", req.Header)
	}
}

func TestApplyRequestHeadersWithoutContextDoesNothing(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ApplyRequestHeaders(req)
	if len(req.Header) != 0 {
		t.Fatalf("unexpected propagated headers: %v", req.Header)
	}
}
