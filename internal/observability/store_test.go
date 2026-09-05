package observability

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreMigratesRouterAndCostSnapshotColumns(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, DirName, DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{
		"actual_member_id", "router_profile_id", "router_profile_version", "routing_text_version",
		"semantic_cluster_id", "semantic_ood", "semantic_fallback_reason", "price_input_per_million",
		"price_output_per_million", "estimated_cost", "cost_currency", "cost_known",
	} {
		if _, err := db.Exec("ALTER TABLE requests DROP COLUMN " + column); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.Add(t.Context(), RequestRecord{
		ID: "migrated", TraceID: "trace", StartedAt: now, CompletedAt: now,
		Method: "POST", Path: "/v1/chat/completions", Status: "completed", StatusCode: http.StatusOK,
		RouterProfileID: "profile", SemanticClusterID: "cluster", CostKnown: true, CostCurrency: "USD",
	}); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetRequest(t.Context(), "migrated")
	if err != nil {
		t.Fatal(err)
	}
	if record.RouterProfileID != "profile" || record.SemanticClusterID != "cluster" || !record.CostKnown {
		t.Fatalf("migrated record = %+v", record)
	}
}

func TestStoreRetainsPerRequestRouterAndPriceSnapshots(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for i, snapshot := range []RequestRecord{
		{
			RouterProfileID: "profile-1", RouterProfileVersion: 1,
			SemanticClusterID: "cluster-1", SemanticDistance: 0.01,
			PriceInputPerMillion: 1, PriceOutputPerMillion: 2,
			EstimatedCost: 0.001, CostCurrency: "USD", CostKnown: true,
		},
		{
			RouterProfileID: "profile-2", RouterProfileVersion: 2,
			RouterProfileSchemaVersion: 2, RouterAlgorithm: "pairwise_router_v2",
			RouterConfidence: .82, RouterMargin: .21, RouterSimilarity: .73,
			SemanticClusterID: "cluster-2", SemanticDistance: 0.987654,
			SemanticFallback: true, SemanticFallbackReason: "low_confidence",
			PriceInputPerMillion: 3, PriceOutputPerMillion: 5,
			EstimatedCost: 0.004, CostCurrency: "EUR", CostKnown: true,
		},
	} {
		snapshot.ID = fmt.Sprintf("snapshot-%d", i)
		snapshot.TraceID = "trace-snapshots"
		snapshot.StartedAt, snapshot.CompletedAt = now, now
		snapshot.Method, snapshot.Path = "POST", "/v1/chat/completions"
		snapshot.Status, snapshot.StatusCode = "completed", http.StatusOK
		if err := store.Add(t.Context(), snapshot); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.GetRequest(t.Context(), "snapshot-0")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.GetRequest(t.Context(), "snapshot-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.RouterProfileID != "profile-1" || first.SemanticDistance != 0.01 ||
		first.PriceInputPerMillion != 1 || first.CostCurrency != "USD" {
		t.Fatalf("first snapshot = %+v", first)
	}
	if second.RouterProfileID != "profile-2" || second.SemanticDistance != 0.987654 ||
		second.RouterProfileSchemaVersion != 2 || second.RouterAlgorithm != "pairwise_router_v2" ||
		second.RouterConfidence != .82 || second.RouterMargin != .21 || second.RouterSimilarity != .73 ||
		second.SemanticClusterID != "cluster-2" ||
		second.SemanticFallbackReason != "low_confidence" ||
		second.PriceInputPerMillion != 3 || second.CostCurrency != "EUR" {
		t.Fatalf("second snapshot = %+v", second)
	}
}

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
			ID: "req-1", RequestID: "gateway-request", TraceID: "trace-1",
			B3TraceID: "463ac35c9f6413ad", ThreadID: "thread-1", StartedAt: start,
			CompletedAt: start.Add(1200 * time.Millisecond), Method: "POST", Path: "/v1/chat/completions",
			Protocol: "openai", Status: "completed", StatusCode: 200, Stream: true, Model: "model-a",
			Source: "local", SourceType: "local", APIKeyID: "key-a", APIKeyName: "Client",
			PoolID: "pool-a", ActualMemberID: "member-a", MemberModel: "actual-a", PoolPolicy: "semantic",
			RouterProfileID: "profile-a", RouterProfileVersion: 3, RoutingTextVersion: "routing-v1",
			SemanticRouted: true, SemanticCluster: 2, SemanticClusterID: "cluster-code", SemanticDistance: 0.125,
			SemanticOOD: true, SemanticFallback: true, SemanticFallbackReason: "out_of_distribution",
			PriceInputPerMillion: 1.5, PriceOutputPerMillion: 2.5, EstimatedCost: 0.0000215,
			CostCurrency: "USD", CostKnown: true,
			InputTokens: 6, OutputTokens: 5, CacheReadInputTokens: 4, CacheCreationTokens: 2,
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
	correlated, err := store.ListRequests(ctx, RequestFilter{Query: "463ac35c", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if correlated.Total != 1 || correlated.Items[0].ID != "req-1" {
		t.Fatalf("B3-filtered requests = %+v", correlated)
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
	if detail.RequestID != "gateway-request" || detail.B3TraceID != "463ac35c9f6413ad" {
		t.Fatalf("correlation identifiers did not round trip: %+v", detail)
	}
	if detail.RouterProfileID != "profile-a" || detail.RouterProfileVersion != 3 ||
		detail.RoutingTextVersion != "routing-v1" || detail.SemanticClusterID != "cluster-code" ||
		!detail.SemanticOOD || detail.SemanticFallbackReason != "out_of_distribution" ||
		detail.ActualMemberID != "member-a" || !detail.CostKnown || detail.CostCurrency != "USD" ||
		detail.EstimatedCost != 0.0000215 {
		t.Fatalf("router and cost snapshot did not round trip: %+v", detail)
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

	facets, err := store.Facets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(facets.Models) != 2 || facets.Models[0].Value != "model-a" || facets.Models[0].Count != 1 {
		t.Fatalf("model facets = %+v, want model-a and model-b", facets.Models)
	}
	if len(facets.Routes) != 3 {
		t.Fatalf("route facets = %+v, want local, provider:test, and pool-a", facets.Routes)
	}
	routes := make(map[string]FacetValue)
	for _, route := range facets.Routes {
		routes[route.Value] = route
	}
	if routes["local"].Count != 1 || routes["provider:test"].Count != 1 || routes["pool-a"].Count != 1 {
		t.Fatalf("unexpected route facet values: %+v", routes)
	}
}

func TestStoreDoesNotUseCorrelationRequestIDAsPrimaryKey(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, id := range []string{"internal-one", "internal-two"} {
		if err := store.Add(t.Context(), RequestRecord{
			ID: id, RequestID: "shared-client-id", TraceID: "trace",
			StartedAt: now, CompletedAt: now, Method: "POST", Path: "/api/chat",
			Status: "completed", StatusCode: http.StatusOK,
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ListRequests(t.Context(), RequestFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Fatalf("stored requests = %d, want 2", page.Total)
	}
}

func TestStoreReconcileUsageRepairsHistoricalRecordsOnce(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	record := RequestRecord{
		ID: "req-old", TraceID: "trace-old", StartedAt: time.Now(), CompletedAt: time.Now(),
		Method: "POST", Path: "/v1/chat/completions", Status: "completed", StatusCode: 200,
		InputTokens: 66, CacheEligibleTokens: 80,
		ResponseBody: `data: {"usage":{"prompt_tokens":80,"completion_tokens":5,"total_tokens":85}}`,
	}
	if err := store.Add(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE requests SET usage_reconciled = 0 WHERE id = ?", record.ID); err != nil {
		t.Fatal(err)
	}
	calls := 0
	reconcile := func(string) (int64, int64, bool) {
		calls++
		return 80, 5, true
	}
	if err := store.ReconcileUsage(ctx, reconcile); err != nil {
		t.Fatal(err)
	}
	if err := store.ReconcileUsage(ctx, reconcile); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("reconciler calls = %d, want 1", calls)
	}
	detail, err := store.GetRequest(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.InputTokens != 80 || detail.OutputTokens != 5 {
		t.Fatalf("reconciled usage = input %d output %d, want 80 and 5", detail.InputTokens, detail.OutputTokens)
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

func TestStoreTraceFiltersPreserveCompleteTraceAggregates(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	start := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	records := []RequestRecord{
		{ID: "mixed-1", TraceID: "trace-mixed", StartedAt: start, CompletedAt: start, Method: "POST", Path: "/api/chat", Status: "completed", StatusCode: 200, Model: "model-a"},
		{ID: "mixed-2", TraceID: "trace-mixed", StartedAt: start.Add(time.Minute), CompletedAt: start.Add(time.Minute), Method: "POST", Path: "/api/chat", Status: "failed", StatusCode: 500, Model: "model-b"},
		{ID: "later-1", TraceID: "trace-later", StartedAt: start.Add(48 * time.Hour), CompletedAt: start.Add(48 * time.Hour), Method: "POST", Path: "/api/chat", Status: "completed", StatusCode: 200, Model: "model-a"},
	}
	for _, record := range records {
		if err := store.Add(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}

	page, err := store.ListTraces(t.Context(), RequestFilter{Model: "model-b", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].TraceID != "trace-mixed" {
		t.Fatalf("model-filtered traces = %+v", page)
	}
	if page.Items[0].RequestCount != 2 || page.Items[0].Status != "failed" {
		t.Fatalf("filtered trace lost its complete aggregate: %+v", page.Items[0])
	}

	from := start.Add(24 * time.Hour)
	ids, err := store.ListTraceIDs(t.Context(), RequestFilter{From: &from})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "trace-later" {
		t.Fatalf("time-filtered trace IDs = %v, want [trace-later]", ids)
	}
}

func TestStoreVisitTracesReadsSelectedBodiesInBatches(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	for index := range 130 {
		traceID := fmt.Sprintf("trace-%03d", index)
		if err := store.Add(ctx, RequestRecord{
			ID: fmt.Sprintf("request-%03d", index), TraceID: traceID,
			StartedAt: now, CompletedAt: now, Method: "POST", Path: "/api/chat",
			Status: "completed", StatusCode: 200,
			RequestBody:  `{"messages":[{"role":"user","content":"hello"}]}`,
			ResponseBody: `{"message":{"role":"assistant","content":"world"}}`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	selected := make([]string, 0, 66)
	for index := 0; index < 130; index += 2 {
		selected = append(selected, fmt.Sprintf("trace-%03d", index))
	}
	visited := make(map[string]bool)
	if err := store.VisitTraces(ctx, selected, func(traceID string, records []RequestRecord) error {
		if len(records) != 1 || !strings.Contains(records[0].RequestBody, "hello") {
			t.Fatalf("records for %s = %+v", traceID, records)
		}
		visited[traceID] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(visited) != len(selected) {
		t.Fatalf("visited %d traces, want %d", len(visited), len(selected))
	}
}

func TestListCompletedPoolRequestsIsBoundedAndPoolIsolated(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, record := range []RequestRecord{
		{ID: "a-complete", TraceID: "trace-a", PoolID: "pool-a", Status: "completed", CompletedAt: now, RequestBody: `{"messages":[{"role":"user","content":"pool a request"}]}`},
		{ID: "a-failed", TraceID: "trace-a-failed", PoolID: "pool-a", Status: "failed", CompletedAt: now.Add(time.Second), RequestBody: `{}`},
		{ID: "b-complete", TraceID: "trace-b", PoolID: "pool-b", Status: "completed", CompletedAt: now.Add(2 * time.Second), RequestBody: `{"messages":[{"role":"user","content":"pool b request"}]}`},
	} {
		if err := store.Add(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	records, err := store.ListCompletedPoolRequests(t.Context(), "pool-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "a-complete" ||
		!strings.Contains(records[0].RequestBody, "pool a request") {
		t.Fatalf("pool-a completed records = %+v", records)
	}
}
