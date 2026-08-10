package observability

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStoreRequestLifecycleAndTraceAggregation(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	start := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	records := []RequestRecord{
		{
			ID: "req-1", TraceID: "trace-1", ThreadID: "thread-1", StartedAt: start,
			CompletedAt: start.Add(1200 * time.Millisecond), Method: "POST", Path: "/v1/chat/completions",
			Protocol: "openai", Status: "completed", StatusCode: 200, Stream: true, Model: "model-a",
			Source: "local", SourceType: "local", APIKeyID: "key-a", APIKeyName: "Client",
			InputTokens: 10, OutputTokens: 5, CacheReadInputTokens: 4, CacheCreationTokens: 2,
			CacheEligibleTokens: 10, DurationMS: 1200, FirstTokenLatencyMS: 120,
			RequestBody:  `{"model":"model-a","messages":[{"role":"user","content":"hello"}]}`,
			ResponseBody: `data: {"choices":[]}`,
		},
		{
			ID: "req-2", TraceID: "trace-1", ThreadID: "thread-1", StartedAt: start.Add(2 * time.Second),
			CompletedAt: start.Add(2500 * time.Millisecond), Method: "POST", Path: "/v1/responses",
			Protocol: "responses", Status: "failed", StatusCode: 429, Model: "model-b",
			Source: "provider:test", SourceType: "provider", InputTokens: 3, DurationMS: 500,
			ErrorMessage: "rate limited", RequestBodyTruncated: true,
		},
	}
	for _, record := range records {
		if err := store.Add(ctx, record); err != nil {
			t.Fatal(err)
		}
	}

	page, err := store.ListRequests(ctx, RequestFilter{Model: "model", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("request page = total %d items %d, want 2", page.Total, len(page.Items))
	}
	if page.Summary.Requests != 2 || page.Summary.Succeeded != 1 || page.Summary.Failed != 1 || page.Summary.TotalTokens != 18 {
		t.Fatalf("unexpected summary: %+v", page.Summary)
	}
	if page.Items[0].RequestBody != "" {
		t.Fatal("list response must not load request bodies")
	}

	detail, err := store.GetRequest(ctx, "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail.RequestBody, "hello") || !strings.Contains(detail.ResponseBody, "choices") {
		t.Fatalf("request bodies did not round trip: %+v", detail)
	}
	if detail.CacheReadInputTokens != 4 || detail.CacheCreationTokens != 2 || detail.CacheEligibleTokens != 10 {
		t.Fatalf("cache usage did not round trip: %+v", detail)
	}

	traces, err := store.ListTraces(ctx, RequestFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if traces.Total != 1 || len(traces.Items) != 1 {
		t.Fatalf("trace page = total %d items %d, want 1", traces.Total, len(traces.Items))
	}
	if traces.Items[0].Status != "failed" || traces.Items[0].RequestCount != 2 || traces.Items[0].TotalTokens != 18 {
		t.Fatalf("unexpected trace aggregate: %+v", traces.Items[0])
	}
	completedTraces, err := store.ListTraces(ctx, RequestFilter{Status: "completed", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if completedTraces.Total != 0 {
		t.Fatalf("mixed failed trace matched completed filter: %+v", completedTraces)
	}

	trace, requests, err := store.GetTrace(ctx, "trace-1")
	if err != nil {
		t.Fatal(err)
	}
	if trace.RequestCount != 2 || len(requests) != 2 || requests[0].ID != "req-1" {
		t.Fatalf("unexpected trace detail: %+v requests=%+v", trace, requests)
	}
	if !strings.Contains(requests[0].RequestBody, "hello") || !strings.Contains(requests[0].ResponseBody, "choices") {
		t.Fatalf("trace detail omitted input/output payloads: %+v", requests[0])
	}
}

func TestStoreCleanupAndDeleteAll(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	for _, record := range []RequestRecord{
		{ID: "old", TraceID: "old", StartedAt: now.AddDate(0, 0, -31), CompletedAt: now.AddDate(0, 0, -31), Method: "POST", Path: "/api/chat", Status: "completed", StatusCode: 200},
		{ID: "new", TraceID: "new", StartedAt: now, CompletedAt: now, Method: "POST", Path: "/api/chat", Status: "completed", StatusCode: 200},
	} {
		if err := store.Add(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := store.Cleanup(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("cleanup deleted %d rows, want 1", deleted)
	}
	page, err := store.ListRequests(ctx, RequestFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].ID != "new" {
		t.Fatalf("unexpected requests after cleanup: %+v", page)
	}
	if err := store.DeleteAll(ctx); err != nil {
		t.Fatal(err)
	}
	page, err = store.ListRequests(ctx, RequestFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Fatalf("requests remain after delete: %+v", page)
	}
}
