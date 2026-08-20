package inference

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/opencsgs/csglite/internal/correlation"
)

func TestLlamaEnginePropagatesCorrelationHeaders(t *testing.T) {
	values := correlation.Values{
		RequestID: "gateway-request", TraceID: "logical-trace",
		B3TraceID: "463ac35c9f6413ad", ThreadID: "thread-one",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(correlation.RequestIDHeader) != values.RequestID ||
			r.Header.Get(correlation.B3TraceIDHeader) != values.B3TraceID {
			t.Fatalf("correlation headers = %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	engine := &llamaEngine{port: port, client: server.Client()}
	ctx := correlation.WithContext(context.Background(), values)

	response, err := engine.ChatCompletion(ctx, map[string]interface{}{"model": "test"})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}
