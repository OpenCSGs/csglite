package observability

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	DirName      = "observability"
	DatabaseFile = "observability.db"
)

type RequestRecord struct {
	ID                         string
	RequestID                  string
	TraceID                    string
	B3TraceID                  string
	ThreadID                   string
	StartedAt                  time.Time
	CompletedAt                time.Time
	Method                     string
	Path                       string
	Protocol                   string
	Status                     string
	StatusCode                 int
	Stream                     bool
	Model                      string
	Source                     string
	SourceType                 string
	SourceName                 string
	APIKeyID                   string
	APIKeyName                 string
	PoolID                     string
	PoolName                   string
	PoolModel                  string
	ActualMemberID             string
	MemberModel                string
	PoolPolicy                 string
	RouterProfileID            string
	RouterProfileVersion       int
	RouterProfileSchemaVersion int
	RouterAlgorithm            string
	RoutingTextVersion         string
	RouterConfidence           float64
	RouterMargin               float64
	RouterSimilarity           float64
	SemanticRouted             bool
	SemanticCluster            int
	SemanticClusterID          string
	SemanticDistance           float64
	SemanticOOD                bool
	SemanticFallback           bool
	SemanticFallbackReason     string
	PriceInputPerMillion       float64
	PriceOutputPerMillion      float64
	EstimatedCost              float64
	CostCurrency               string
	CostKnown                  bool
	FallbackCount              int64
	LimitedCount               int64
	InputTokens                int64
	OutputTokens               int64
	CacheReadInputTokens       int64
	CacheCreationTokens        int64
	CacheEligibleTokens        int64
	DurationMS                 int64
	FirstTokenLatencyMS        int64
	ErrorMessage               string
	RequestBody                string
	ResponseBody               string
	RequestBodyTruncated       bool
	ResponseBodyTruncated      bool
}

type RequestFilter struct {
	From     *time.Time
	To       *time.Time
	Status   string
	Model    string
	Source   string
	APIKeyID string
	Query    string
	Limit    int
	Offset   int
}

type RequestSummary struct {
	Requests       int64
	Succeeded      int64
	Failed         int64
	TotalTokens    int64
	AverageLatency float64
}

type RequestPage struct {
	Items   []RequestRecord
	Total   int64
	Summary RequestSummary
}

type TraceRecord struct {
	TraceID      string
	ThreadID     string
	StartedAt    time.Time
	CompletedAt  time.Time
	Status       string
	RequestCount int64
	Models       []string
	TotalTokens  int64
	DurationMS   int64
}

type TracePage struct {
	Items []TraceRecord
	Total int64
}

type Store struct {
	db   *sql.DB
	path string
}

func Open(storageRoot string) (*Store, error) {
	if strings.TrimSpace(storageRoot) == "" {
		return nil, errors.New("observability storage root is required")
	}
	dir := filepath.Join(filepath.Clean(storageRoot), DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating observability directory: %w", err)
	}
	path := filepath.Join(dir, DatabaseFile)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening observability database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db, path: path}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) init() error {
	const schema = `
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS requests (
	id TEXT PRIMARY KEY,
	request_id TEXT NOT NULL DEFAULT '',
	trace_id TEXT NOT NULL,
	b3_trace_id TEXT NOT NULL DEFAULT '',
	thread_id TEXT NOT NULL DEFAULT '',
	started_at INTEGER NOT NULL,
	completed_at INTEGER NOT NULL,
	method TEXT NOT NULL,
	path TEXT NOT NULL,
	protocol TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	status_code INTEGER NOT NULL,
	stream INTEGER NOT NULL DEFAULT 0,
	model TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL DEFAULT '',
	source_type TEXT NOT NULL DEFAULT '',
	source_name TEXT NOT NULL DEFAULT '',
	api_key_id TEXT NOT NULL DEFAULT '',
	api_key_name TEXT NOT NULL DEFAULT '',
	pool_id TEXT NOT NULL DEFAULT '',
	pool_name TEXT NOT NULL DEFAULT '',
	pool_model TEXT NOT NULL DEFAULT '',
	actual_member_id TEXT NOT NULL DEFAULT '',
	member_model TEXT NOT NULL DEFAULT '',
	pool_policy TEXT NOT NULL DEFAULT '',
	router_profile_id TEXT NOT NULL DEFAULT '',
	router_profile_version INTEGER NOT NULL DEFAULT 0,
	router_profile_schema_version INTEGER NOT NULL DEFAULT 0,
	router_algorithm TEXT NOT NULL DEFAULT '',
	routing_text_version TEXT NOT NULL DEFAULT '',
	router_confidence REAL NOT NULL DEFAULT 0,
	router_margin REAL NOT NULL DEFAULT 0,
	router_similarity REAL NOT NULL DEFAULT 0,
	semantic_routed INTEGER NOT NULL DEFAULT 0,
	semantic_cluster INTEGER NOT NULL DEFAULT 0,
	semantic_cluster_id TEXT NOT NULL DEFAULT '',
	semantic_distance REAL NOT NULL DEFAULT 0,
	semantic_ood INTEGER NOT NULL DEFAULT 0,
	semantic_fallback INTEGER NOT NULL DEFAULT 0,
	semantic_fallback_reason TEXT NOT NULL DEFAULT '',
	price_input_per_million REAL NOT NULL DEFAULT 0,
	price_output_per_million REAL NOT NULL DEFAULT 0,
	estimated_cost REAL NOT NULL DEFAULT 0,
	cost_currency TEXT NOT NULL DEFAULT '',
	cost_known INTEGER NOT NULL DEFAULT 0,
	fallback_count INTEGER NOT NULL DEFAULT 0,
	limited_count INTEGER NOT NULL DEFAULT 0,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
	cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
	cache_eligible_input_tokens INTEGER NOT NULL DEFAULT 0,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	first_token_latency_ms INTEGER NOT NULL DEFAULT 0,
	error_message TEXT NOT NULL DEFAULT '',
	request_body BLOB,
	response_body BLOB,
	request_body_truncated INTEGER NOT NULL DEFAULT 0,
	response_body_truncated INTEGER NOT NULL DEFAULT 0,
	usage_reconciled INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_requests_started_at ON requests(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_requests_trace_id ON requests(trace_id, started_at);
CREATE INDEX IF NOT EXISTS idx_requests_thread_id ON requests(thread_id, started_at);
CREATE INDEX IF NOT EXISTS idx_requests_status ON requests(status, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_requests_model ON requests(model, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_requests_source ON requests(source, source_name);
CREATE INDEX IF NOT EXISTS idx_requests_pool_id ON requests(pool_id, pool_name);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("initializing observability database: %w", err)
	}
	for _, migration := range []struct {
		name       string
		definition string
	}{
		{"request_id", "TEXT NOT NULL DEFAULT ''"},
		{"b3_trace_id", "TEXT NOT NULL DEFAULT ''"},
		{"cache_read_input_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"cache_creation_input_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"cache_eligible_input_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"usage_reconciled", "INTEGER NOT NULL DEFAULT 0"},
		{"pool_policy", "TEXT NOT NULL DEFAULT ''"},
		{"semantic_routed", "INTEGER NOT NULL DEFAULT 0"},
		{"semantic_cluster", "INTEGER NOT NULL DEFAULT 0"},
		{"semantic_distance", "REAL NOT NULL DEFAULT 0"},
		{"semantic_fallback", "INTEGER NOT NULL DEFAULT 0"},
		{"actual_member_id", "TEXT NOT NULL DEFAULT ''"},
		{"router_profile_id", "TEXT NOT NULL DEFAULT ''"},
		{"router_profile_version", "INTEGER NOT NULL DEFAULT 0"},
		{"router_profile_schema_version", "INTEGER NOT NULL DEFAULT 0"},
		{"router_algorithm", "TEXT NOT NULL DEFAULT ''"},
		{"routing_text_version", "TEXT NOT NULL DEFAULT ''"},
		{"router_confidence", "REAL NOT NULL DEFAULT 0"},
		{"router_margin", "REAL NOT NULL DEFAULT 0"},
		{"router_similarity", "REAL NOT NULL DEFAULT 0"},
		{"semantic_cluster_id", "TEXT NOT NULL DEFAULT ''"},
		{"semantic_ood", "INTEGER NOT NULL DEFAULT 0"},
		{"semantic_fallback_reason", "TEXT NOT NULL DEFAULT ''"},
		{"price_input_per_million", "REAL NOT NULL DEFAULT 0"},
		{"price_output_per_million", "REAL NOT NULL DEFAULT 0"},
		{"estimated_cost", "REAL NOT NULL DEFAULT 0"},
		{"cost_currency", "TEXT NOT NULL DEFAULT ''"},
		{"cost_known", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := s.addColumnIfMissing("requests", migration.name, migration.definition); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) addColumnIfMissing(table, column, definition string) error {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return fmt.Errorf("inspecting observability table %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("scanning observability table %s: %w", table, err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspecting observability table %s: %w", table, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("closing observability table inspection %s: %w", table, err)
	}
	if _, err := s.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition); err != nil {
		return fmt.Errorf("adding observability column %s: %w", column, err)
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Add(ctx context.Context, record RequestRecord) error {
	if s == nil || s.db == nil {
		return errors.New("observability store is unavailable")
	}
	requestBody, err := compress(record.RequestBody)
	if err != nil {
		return fmt.Errorf("compressing request body: %w", err)
	}
	responseBody, err := compress(record.ResponseBody)
	if err != nil {
		return fmt.Errorf("compressing response body: %w", err)
	}
	args := []any{
		record.ID, record.RequestID, record.TraceID, record.B3TraceID, record.ThreadID,
		millis(record.StartedAt), millis(record.CompletedAt),
		record.Method, record.Path, record.Protocol, record.Status, record.StatusCode, boolInt(record.Stream),
		record.Model, record.Source, record.SourceType, record.SourceName, record.APIKeyID, record.APIKeyName,
		record.PoolID, record.PoolName, record.PoolModel, record.ActualMemberID, record.MemberModel, record.PoolPolicy,
		record.RouterProfileID, record.RouterProfileVersion, record.RouterProfileSchemaVersion,
		record.RouterAlgorithm, record.RoutingTextVersion, record.RouterConfidence,
		record.RouterMargin, record.RouterSimilarity,
		boolInt(record.SemanticRouted), record.SemanticCluster, record.SemanticClusterID, record.SemanticDistance,
		boolInt(record.SemanticOOD), boolInt(record.SemanticFallback), record.SemanticFallbackReason,
		record.PriceInputPerMillion, record.PriceOutputPerMillion, record.EstimatedCost,
		record.CostCurrency, boolInt(record.CostKnown), record.FallbackCount, record.LimitedCount,
		record.InputTokens, record.OutputTokens, record.DurationMS,
		record.CacheReadInputTokens, record.CacheCreationTokens, record.CacheEligibleTokens,
		record.FirstTokenLatencyMS, record.ErrorMessage, requestBody, responseBody,
		boolInt(record.RequestBodyTruncated), boolInt(record.ResponseBodyTruncated), 1,
	}
	query := `
INSERT OR REPLACE INTO requests (
	id, request_id, trace_id, b3_trace_id, thread_id, started_at, completed_at, method, path, protocol,
	status, status_code, stream, model, source, source_type, source_name,
	api_key_id, api_key_name, pool_id, pool_name, pool_model, actual_member_id, member_model,
	pool_policy, router_profile_id, router_profile_version, router_profile_schema_version,
	router_algorithm, routing_text_version, router_confidence, router_margin, router_similarity,
	semantic_routed, semantic_cluster, semantic_cluster_id, semantic_distance, semantic_ood, semantic_fallback,
	semantic_fallback_reason, price_input_per_million, price_output_per_million, estimated_cost, cost_currency, cost_known,
	fallback_count, limited_count, input_tokens, output_tokens, duration_ms,
	cache_read_input_tokens, cache_creation_input_tokens, cache_eligible_input_tokens,
	first_token_latency_ms, error_message, request_body, response_body,
	request_body_truncated, response_body_truncated, usage_reconciled
) VALUES (` + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + `)`
	_, err = s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("saving observability request: %w", err)
	}
	return nil
}

// ReconcileUsage repairs records written before response usage was captured.
// Each stored response is processed at most once.
func (s *Store) ReconcileUsage(ctx context.Context, reconcile func(string) (int64, int64, bool)) error {
	if s == nil || s.db == nil {
		return errors.New("observability store is unavailable")
	}
	if reconcile == nil {
		return errors.New("usage reconciler is required")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, response_body
FROM requests
WHERE usage_reconciled = 0 AND response_body IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("loading observability usage for reconciliation: %w", err)
	}
	type correction struct {
		id               string
		inputTokens      int64
		outputTokens     int64
		hasProviderUsage bool
	}
	corrections := make([]correction, 0)
	for rows.Next() {
		var id string
		var compressed []byte
		if err := rows.Scan(&id, &compressed); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scanning observability usage for reconciliation: %w", err)
		}
		body, err := decompress(compressed)
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("decompressing observability response %s: %w", id, err)
		}
		inputTokens, outputTokens, ok := reconcile(body)
		corrections = append(corrections, correction{
			id: id, inputTokens: inputTokens, outputTokens: outputTokens, hasProviderUsage: ok,
		})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("reading observability usage for reconciliation: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("closing observability usage reconciliation rows: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting observability usage reconciliation: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	for _, item := range corrections {
		if item.hasProviderUsage {
			_, err = tx.ExecContext(ctx, `
UPDATE requests
SET input_tokens = MAX(?, cache_eligible_input_tokens), output_tokens = ?, usage_reconciled = 1
WHERE id = ?`, item.inputTokens, item.outputTokens, item.id)
		} else {
			_, err = tx.ExecContext(ctx, "UPDATE requests SET usage_reconciled = 1 WHERE id = ?", item.id)
		}
		if err != nil {
			return fmt.Errorf("reconciling observability usage %s: %w", item.id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing observability usage reconciliation: %w", err)
	}
	return nil
}

func (s *Store) ListRequests(ctx context.Context, filter RequestFilter) (RequestPage, error) {
	filter = normalizeFilter(filter)
	where, args := requestWhere(filter)
	var page RequestPage
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM requests"+where, args...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("counting observability requests: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*),
	COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN status != 'completed' THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(MAX(input_tokens, cache_eligible_input_tokens) + output_tokens), 0),
	COALESCE(AVG(duration_ms), 0)
FROM requests`+where, args...).Scan(
		&page.Summary.Requests, &page.Summary.Succeeded, &page.Summary.Failed,
		&page.Summary.TotalTokens, &page.Summary.AverageLatency,
	); err != nil {
		return page, fmt.Errorf("summarizing observability requests: %w", err)
	}
	query := requestSelect(false) + where + " ORDER BY started_at DESC LIMIT ? OFFSET ?"
	listArgs := append(append([]any{}, args...), filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, query, listArgs...)
	if err != nil {
		return page, fmt.Errorf("listing observability requests: %w", err)
	}
	defer rows.Close()
	page.Items = make([]RequestRecord, 0, filter.Limit)
	for rows.Next() {
		record, err := scanRequest(rows, false)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, record)
	}
	return page, rows.Err()
}

func (s *Store) GetRequest(ctx context.Context, id string) (RequestRecord, error) {
	row := s.db.QueryRowContext(ctx, requestSelect(true)+" WHERE id = ?", strings.TrimSpace(id))
	return scanRequest(row, true)
}

func (s *Store) ListTraces(ctx context.Context, filter RequestFilter) (TracePage, error) {
	filter = normalizeFilter(filter)
	where, args, having := traceQueryParts(filter)
	var page TracePage
	countQuery := "SELECT COUNT(*) FROM (SELECT trace_id FROM requests" + where + " GROUP BY trace_id" + having + ")"
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("counting observability traces: %w", err)
	}
	query := `
SELECT trace_id, MAX(thread_id), MIN(started_at), MAX(completed_at),
	CASE WHEN SUM(CASE WHEN status != 'completed' THEN 1 ELSE 0 END) > 0 THEN 'failed' ELSE 'completed' END,
	COUNT(*), GROUP_CONCAT(DISTINCT model), SUM(MAX(input_tokens, cache_eligible_input_tokens) + output_tokens)
FROM requests` + where + `
GROUP BY trace_id
` + having + `
ORDER BY MIN(started_at) DESC
LIMIT ? OFFSET ?`
	listArgs := append(append([]any{}, args...), filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, query, listArgs...)
	if err != nil {
		return page, fmt.Errorf("listing observability traces: %w", err)
	}
	defer rows.Close()
	page.Items = make([]TraceRecord, 0, filter.Limit)
	for rows.Next() {
		var item TraceRecord
		var started, completed int64
		var models string
		if err := rows.Scan(&item.TraceID, &item.ThreadID, &started, &completed, &item.Status, &item.RequestCount, &models, &item.TotalTokens); err != nil {
			return page, fmt.Errorf("scanning observability trace: %w", err)
		}
		item.StartedAt = fromMillis(started)
		item.CompletedAt = fromMillis(completed)
		item.DurationMS = max(0, completed-started)
		for _, model := range strings.Split(models, ",") {
			if model = strings.TrimSpace(model); model != "" {
				item.Models = append(item.Models, model)
			}
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}

// ListTraceIDs returns every trace matching the same server-side filters used
// by ListTraces. It intentionally ignores pagination so callers can operate on
// the complete filtered result without sending thousands of IDs from the UI.
func (s *Store) ListTraceIDs(ctx context.Context, filter RequestFilter) ([]string, error) {
	where, args, having := traceQueryParts(filter)
	query := "SELECT trace_id FROM requests" + where + " GROUP BY trace_id" + having + " ORDER BY MIN(started_at) DESC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing filtered observability trace IDs: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning filtered observability trace ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading filtered observability trace IDs: %w", err)
	}
	return ids, nil
}

func (s *Store) GetTrace(ctx context.Context, traceID string) (TraceRecord, []RequestRecord, error) {
	rows, err := s.db.QueryContext(ctx, requestSelect(true)+" WHERE trace_id = ? ORDER BY started_at", strings.TrimSpace(traceID))
	if err != nil {
		return TraceRecord{}, nil, fmt.Errorf("loading observability trace: %w", err)
	}
	defer rows.Close()
	requests := make([]RequestRecord, 0)
	for rows.Next() {
		record, err := scanRequest(rows, true)
		if err != nil {
			return TraceRecord{}, nil, err
		}
		requests = append(requests, record)
	}
	if err := rows.Err(); err != nil {
		return TraceRecord{}, nil, err
	}
	if len(requests) == 0 {
		return TraceRecord{}, nil, sql.ErrNoRows
	}
	trace := TraceRecord{
		TraceID:     requests[0].TraceID,
		ThreadID:    requests[0].ThreadID,
		StartedAt:   requests[0].StartedAt,
		CompletedAt: requests[0].CompletedAt,
		Status:      "completed",
	}
	models := map[string]struct{}{}
	for _, record := range requests {
		trace.RequestCount++
		trace.TotalTokens += requestTotalTokens(record)
		if record.StartedAt.Before(trace.StartedAt) {
			trace.StartedAt = record.StartedAt
		}
		if record.CompletedAt.After(trace.CompletedAt) {
			trace.CompletedAt = record.CompletedAt
		}
		if record.Status != "completed" {
			trace.Status = "failed"
		}
		if record.Model != "" {
			models[record.Model] = struct{}{}
		}
	}
	for model := range models {
		trace.Models = append(trace.Models, model)
	}
	trace.DurationMS = trace.CompletedAt.Sub(trace.StartedAt).Milliseconds()
	return trace, requests, nil
}

// ListCompletedPoolRequests returns a bounded, pool-isolated set of recent
// completed text-generation observations with captured bodies.
func (s *Store) ListCompletedPoolRequests(ctx context.Context, poolID string, limit int) ([]RequestRecord, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("observability store is unavailable")
	}
	poolID = strings.TrimSpace(poolID)
	if poolID == "" {
		return nil, errors.New("provider pool ID is required")
	}
	if limit <= 0 || limit > 128 {
		limit = 128
	}
	rows, err := s.db.QueryContext(ctx, requestSelect(true)+`
 WHERE pool_id = ? AND status = 'completed' AND request_body IS NOT NULL
 AND NOT EXISTS (
	SELECT 1 FROM requests failed
	WHERE failed.pool_id = requests.pool_id
	AND failed.trace_id = requests.trace_id
	AND failed.status != 'completed'
 )
 ORDER BY completed_at DESC, id DESC LIMIT ?`, poolID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing completed pool requests: %w", err)
	}
	defer rows.Close()
	records := make([]RequestRecord, 0, limit)
	for rows.Next() {
		record, err := scanRequest(rows, true)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading completed pool requests: %w", err)
	}
	return records, nil
}

func requestTotalTokens(record RequestRecord) int64 {
	return max(record.InputTokens, record.CacheEligibleTokens) + record.OutputTokens
}

// VisitTraces reads selected traces in bounded batches. It avoids one SQL query
// per trace and releases the SQLite connection between batches so observation
// writes can continue during large exports.
func (s *Store) VisitTraces(ctx context.Context, traceIDs []string, visit func(string, []RequestRecord) error) error {
	const batchSize = 64
	if s == nil || s.db == nil {
		return errors.New("observability store is unavailable")
	}
	if visit == nil {
		return errors.New("trace visitor is required")
	}
	for start := 0; start < len(traceIDs); start += batchSize {
		end := min(start+batchSize, len(traceIDs))
		batch := traceIDs[start:end]
		placeholders := make([]string, 0, len(batch))
		args := make([]any, 0, len(batch))
		for _, traceID := range batch {
			placeholders = append(placeholders, "?")
			args = append(args, strings.TrimSpace(traceID))
		}
		query := requestSelect(true) + " WHERE trace_id IN (" + strings.Join(placeholders, ",") + ") ORDER BY trace_id, started_at"
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("loading observability traces for export: %w", err)
		}
		currentID := ""
		records := make([]RequestRecord, 0, 4)
		for rows.Next() {
			record, scanErr := scanRequest(rows, true)
			if scanErr != nil {
				_ = rows.Close()
				return scanErr
			}
			if currentID != "" && record.TraceID != currentID {
				if err := visit(currentID, records); err != nil {
					_ = rows.Close()
					return err
				}
				records = make([]RequestRecord, 0, 4)
			}
			currentID = record.TraceID
			records = append(records, record)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("reading observability traces for export: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("closing observability trace export rows: %w", err)
		}
		if currentID != "" {
			if err := visit(currentID, records); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) DeleteAll(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM requests"); err != nil {
		return fmt.Errorf("clearing observability requests: %w", err)
	}
	return nil
}

func (s *Store) Cleanup(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	result, err := s.db.ExecContext(ctx, "DELETE FROM requests WHERE started_at < ?", millis(cutoff))
	if err != nil {
		return 0, fmt.Errorf("cleaning observability requests: %w", err)
	}
	return result.RowsAffected()
}

const facetLimit = 200

type FacetValue struct {
	Value string
	Label string
	Count int64
}

type Facets struct {
	Models []FacetValue
	Routes []FacetValue
}

// Facets returns distinct model names and route identifiers from the request
// history so the UI can offer dropdown filters. Values with an empty identifier
// are skipped; routes carry the friendliest display label found for each value.
func (s *Store) Facets(ctx context.Context) (Facets, error) {
	var facets Facets
	modelRows, err := s.db.QueryContext(ctx, `
SELECT model, COUNT(*) FROM requests WHERE TRIM(model) != '' GROUP BY TRIM(model) ORDER BY COUNT(*) DESC, model LIMIT ?`, facetLimit)
	if err != nil {
		return facets, fmt.Errorf("loading observability model facets: %w", err)
	}
	defer modelRows.Close()
	for modelRows.Next() {
		var value string
		var count int64
		if err := modelRows.Scan(&value, &count); err != nil {
			return facets, fmt.Errorf("scanning observability model facet: %w", err)
		}
		facets.Models = append(facets.Models, FacetValue{Value: strings.TrimSpace(value), Count: count})
	}
	if err := modelRows.Err(); err != nil {
		return facets, fmt.Errorf("reading observability model facets: %w", err)
	}

	routes := make(map[string]FacetValue)
	for _, query := range []string{
		`SELECT source, source_name, COUNT(*) FROM requests WHERE TRIM(source) != '' GROUP BY source, source_name LIMIT ?`,
		`SELECT pool_id, pool_name, COUNT(*) FROM requests WHERE TRIM(pool_id) != '' GROUP BY pool_id, pool_name LIMIT ?`,
	} {
		if err := collectRouteFacets(ctx, s.db, query, routes); err != nil {
			return facets, err
		}
	}
	facets.Routes = make([]FacetValue, 0, len(routes))
	for _, route := range routes {
		facets.Routes = append(facets.Routes, route)
	}
	sort.Slice(facets.Routes, func(i, j int) bool {
		if facets.Routes[i].Count != facets.Routes[j].Count {
			return facets.Routes[i].Count > facets.Routes[j].Count
		}
		return facets.Routes[i].Value < facets.Routes[j].Value
	})
	return facets, nil
}

func collectRouteFacets(ctx context.Context, db *sql.DB, query string, routes map[string]FacetValue) error {
	rows, err := db.QueryContext(ctx, query, facetLimit)
	if err != nil {
		return fmt.Errorf("loading observability route facets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var value, label string
		var count int64
		if err := rows.Scan(&value, &label, &count); err != nil {
			return fmt.Errorf("scanning observability route facet: %w", err)
		}
		value = strings.TrimSpace(value)
		label = strings.TrimSpace(label)
		existing := routes[value]
		existing.Value = value
		if existing.Label == "" {
			existing.Label = label
		}
		existing.Count += count
		routes[value] = existing
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading observability route facets: %w", err)
	}
	return nil
}

func normalizeFilter(filter RequestFilter) RequestFilter {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	return filter
}

func requestWhere(filter RequestFilter) (string, []any) {
	clauses := make([]string, 0, 7)
	args := make([]any, 0, 7)
	if filter.From != nil {
		clauses = append(clauses, "started_at >= ?")
		args = append(args, millis(*filter.From))
	}
	if filter.To != nil {
		clauses = append(clauses, "started_at <= ?")
		args = append(args, millis(*filter.To))
	}
	if value := strings.TrimSpace(filter.Status); value != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Model); value != "" {
		clauses = append(clauses, "LOWER(model) LIKE ?")
		args = append(args, "%"+strings.ToLower(value)+"%")
	}
	if value := strings.TrimSpace(filter.Source); value != "" {
		clauses = append(clauses, "(source = ? OR source_type = ? OR pool_id = ?)")
		args = append(args, value, value, value)
	}
	if value := strings.TrimSpace(filter.APIKeyID); value != "" {
		clauses = append(clauses, "api_key_id = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Query); value != "" {
		like := "%" + strings.ToLower(value) + "%"
		clauses = append(clauses, "(LOWER(id) LIKE ? OR LOWER(request_id) LIKE ? OR LOWER(trace_id) LIKE ? OR LOWER(b3_trace_id) LIKE ? OR LOWER(thread_id) LIKE ? OR LOWER(model) LIKE ?)")
		args = append(args, like, like, like, like, like, like)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func traceQueryParts(filter RequestFilter) (string, []any, string) {
	clauses := make([]string, 0, 7)
	args := make([]any, 0, 12)
	if filter.From != nil {
		clauses = append(clauses, "MIN(started_at) >= ?")
		args = append(args, millis(*filter.From))
	}
	if filter.To != nil {
		clauses = append(clauses, "MIN(started_at) <= ?")
		args = append(args, millis(*filter.To))
	}
	switch strings.TrimSpace(filter.Status) {
	case "completed":
		clauses = append(clauses, "SUM(CASE WHEN status != 'completed' THEN 1 ELSE 0 END) = 0")
	case "failed":
		clauses = append(clauses, "SUM(CASE WHEN status != 'completed' THEN 1 ELSE 0 END) > 0")
	}
	if value := strings.TrimSpace(filter.Model); value != "" {
		clauses = append(clauses, "SUM(CASE WHEN LOWER(model) LIKE ? THEN 1 ELSE 0 END) > 0")
		args = append(args, "%"+strings.ToLower(value)+"%")
	}
	if value := strings.TrimSpace(filter.Source); value != "" {
		clauses = append(clauses, "SUM(CASE WHEN source = ? OR source_type = ? OR pool_id = ? THEN 1 ELSE 0 END) > 0")
		args = append(args, value, value, value)
	}
	if value := strings.TrimSpace(filter.APIKeyID); value != "" {
		clauses = append(clauses, "SUM(CASE WHEN api_key_id = ? THEN 1 ELSE 0 END) > 0")
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Query); value != "" {
		like := "%" + strings.ToLower(value) + "%"
		clauses = append(clauses, `SUM(CASE WHEN
			LOWER(id) LIKE ? OR LOWER(request_id) LIKE ? OR LOWER(trace_id) LIKE ? OR
			LOWER(b3_trace_id) LIKE ? OR LOWER(thread_id) LIKE ? OR LOWER(model) LIKE ?
			THEN 1 ELSE 0 END) > 0`)
		args = append(args, like, like, like, like, like, like)
	}
	if len(clauses) == 0 {
		return "", args, ""
	}
	return "", args, " HAVING " + strings.Join(clauses, " AND ")
}

func requestSelect(includeBodies bool) string {
	bodyColumns := ""
	if includeBodies {
		bodyColumns = ", request_body, response_body"
	}
	return `SELECT id, request_id, trace_id, b3_trace_id, thread_id, started_at, completed_at, method, path, protocol,
status, status_code, stream, model, source, source_type, source_name, api_key_id, api_key_name,
pool_id, pool_name, pool_model, actual_member_id, member_model, pool_policy,
router_profile_id, router_profile_version, router_profile_schema_version, router_algorithm,
routing_text_version, router_confidence, router_margin, router_similarity,
semantic_routed, semantic_cluster, semantic_cluster_id, semantic_distance, semantic_ood,
semantic_fallback, semantic_fallback_reason, price_input_per_million, price_output_per_million,
estimated_cost, cost_currency, cost_known, fallback_count, limited_count, input_tokens,
output_tokens, duration_ms, cache_read_input_tokens, cache_creation_input_tokens,
cache_eligible_input_tokens, first_token_latency_ms, error_message,
request_body_truncated, response_body_truncated` + bodyColumns + ` FROM requests`
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRequest(row scanner, includeBodies bool) (RequestRecord, error) {
	var record RequestRecord
	var started, completed int64
	var stream, semanticRouted, semanticOOD, semanticFallback, costKnown, requestTruncated, responseTruncated int
	dest := []any{
		&record.ID, &record.RequestID, &record.TraceID, &record.B3TraceID, &record.ThreadID,
		&started, &completed, &record.Method,
		&record.Path, &record.Protocol, &record.Status, &record.StatusCode, &stream, &record.Model,
		&record.Source, &record.SourceType, &record.SourceName, &record.APIKeyID, &record.APIKeyName,
		&record.PoolID, &record.PoolName, &record.PoolModel, &record.ActualMemberID, &record.MemberModel, &record.PoolPolicy,
		&record.RouterProfileID, &record.RouterProfileVersion, &record.RouterProfileSchemaVersion,
		&record.RouterAlgorithm, &record.RoutingTextVersion, &record.RouterConfidence,
		&record.RouterMargin, &record.RouterSimilarity,
		&semanticRouted, &record.SemanticCluster, &record.SemanticClusterID, &record.SemanticDistance, &semanticOOD,
		&semanticFallback, &record.SemanticFallbackReason, &record.PriceInputPerMillion, &record.PriceOutputPerMillion,
		&record.EstimatedCost, &record.CostCurrency, &costKnown, &record.FallbackCount,
		&record.LimitedCount, &record.InputTokens, &record.OutputTokens, &record.DurationMS,
		&record.CacheReadInputTokens, &record.CacheCreationTokens, &record.CacheEligibleTokens,
		&record.FirstTokenLatencyMS, &record.ErrorMessage, &requestTruncated, &responseTruncated,
	}
	var requestBody, responseBody []byte
	if includeBodies {
		dest = append(dest, &requestBody, &responseBody)
	}
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return record, err
		}
		return record, fmt.Errorf("scanning observability request: %w", err)
	}
	record.StartedAt = fromMillis(started)
	record.CompletedAt = fromMillis(completed)
	record.Stream = stream != 0
	record.SemanticRouted = semanticRouted != 0
	record.SemanticOOD = semanticOOD != 0
	record.SemanticFallback = semanticFallback != 0
	record.CostKnown = costKnown != 0
	record.RequestBodyTruncated = requestTruncated != 0
	record.ResponseBodyTruncated = responseTruncated != 0
	if includeBodies {
		var err error
		if record.RequestBody, err = decompress(requestBody); err != nil {
			return record, fmt.Errorf("decompressing request body: %w", err)
		}
		if record.ResponseBody, err = decompress(responseBody); err != nil {
			return record, fmt.Errorf("decompressing response body: %w", err)
		}
	}
	return record, nil
}

func compress(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	if _, err := writer.Write([]byte(value)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func decompress(value []byte) (string, error) {
	if len(value) == 0 {
		return "", nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(value))
	if err != nil {
		return "", err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	return string(data), err
}

func millis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

func fromMillis(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
