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
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	DirName      = "observability"
	DatabaseFile = "observability.db"
)

type RequestRecord struct {
	ID                    string
	TraceID               string
	ThreadID              string
	StartedAt             time.Time
	CompletedAt           time.Time
	Method                string
	Path                  string
	Protocol              string
	Status                string
	StatusCode            int
	Stream                bool
	Model                 string
	Source                string
	SourceType            string
	SourceName            string
	APIKeyID              string
	APIKeyName            string
	PoolID                string
	PoolName              string
	PoolModel             string
	MemberModel           string
	FallbackCount         int64
	LimitedCount          int64
	InputTokens           int64
	OutputTokens          int64
	CacheReadInputTokens  int64
	CacheCreationTokens   int64
	CacheEligibleTokens   int64
	DurationMS            int64
	FirstTokenLatencyMS   int64
	ErrorMessage          string
	RequestBody           string
	ResponseBody          string
	RequestBodyTruncated  bool
	ResponseBodyTruncated bool
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
	trace_id TEXT NOT NULL,
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
	member_model TEXT NOT NULL DEFAULT '',
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
	response_body_truncated INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_requests_started_at ON requests(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_requests_trace_id ON requests(trace_id, started_at);
CREATE INDEX IF NOT EXISTS idx_requests_thread_id ON requests(thread_id, started_at);
CREATE INDEX IF NOT EXISTS idx_requests_status ON requests(status, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_requests_model ON requests(model, started_at DESC);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("initializing observability database: %w", err)
	}
	for _, migration := range []struct {
		name       string
		definition string
	}{
		{"cache_read_input_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"cache_creation_input_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"cache_eligible_input_tokens", "INTEGER NOT NULL DEFAULT 0"},
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
	_, err = s.db.ExecContext(ctx, `
INSERT OR REPLACE INTO requests (
	id, trace_id, thread_id, started_at, completed_at, method, path, protocol,
	status, status_code, stream, model, source, source_type, source_name,
	api_key_id, api_key_name, pool_id, pool_name, pool_model, member_model,
	fallback_count, limited_count, input_tokens, output_tokens, duration_ms,
	cache_read_input_tokens, cache_creation_input_tokens, cache_eligible_input_tokens,
	first_token_latency_ms, error_message, request_body, response_body,
	request_body_truncated, response_body_truncated
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.TraceID, record.ThreadID, millis(record.StartedAt), millis(record.CompletedAt),
		record.Method, record.Path, record.Protocol, record.Status, record.StatusCode, boolInt(record.Stream),
		record.Model, record.Source, record.SourceType, record.SourceName, record.APIKeyID, record.APIKeyName,
		record.PoolID, record.PoolName, record.PoolModel, record.MemberModel, record.FallbackCount,
		record.LimitedCount, record.InputTokens, record.OutputTokens, record.DurationMS,
		record.CacheReadInputTokens, record.CacheCreationTokens, record.CacheEligibleTokens,
		record.FirstTokenLatencyMS, record.ErrorMessage, requestBody, responseBody,
		boolInt(record.RequestBodyTruncated), boolInt(record.ResponseBodyTruncated),
	)
	if err != nil {
		return fmt.Errorf("saving observability request: %w", err)
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
	COALESCE(SUM(input_tokens + output_tokens), 0),
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
	traceStatus := strings.TrimSpace(filter.Status)
	filter.Status = ""
	where, args := requestWhere(filter)
	having := ""
	switch traceStatus {
	case "completed":
		having = " HAVING SUM(CASE WHEN status != 'completed' THEN 1 ELSE 0 END) = 0"
	case "failed":
		having = " HAVING SUM(CASE WHEN status != 'completed' THEN 1 ELSE 0 END) > 0"
	}
	var page TracePage
	countQuery := "SELECT COUNT(*) FROM (SELECT trace_id FROM requests" + where + " GROUP BY trace_id" + having + ")"
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("counting observability traces: %w", err)
	}
	query := `
SELECT trace_id, MAX(thread_id), MIN(started_at), MAX(completed_at),
	CASE WHEN SUM(CASE WHEN status != 'completed' THEN 1 ELSE 0 END) > 0 THEN 'failed' ELSE 'completed' END,
	COUNT(*), GROUP_CONCAT(DISTINCT model), SUM(input_tokens + output_tokens)
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
		item.DurationMS = max64(0, completed-started)
		for _, model := range strings.Split(models, ",") {
			if model = strings.TrimSpace(model); model != "" {
				item.Models = append(item.Models, model)
			}
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
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
		trace.TotalTokens += record.InputTokens + record.OutputTokens
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
		clauses = append(clauses, "(LOWER(id) LIKE ? OR LOWER(trace_id) LIKE ? OR LOWER(thread_id) LIKE ? OR LOWER(model) LIKE ?)")
		args = append(args, like, like, like, like)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func requestSelect(includeBodies bool) string {
	bodyColumns := ""
	if includeBodies {
		bodyColumns = ", request_body, response_body"
	}
	return `SELECT id, trace_id, thread_id, started_at, completed_at, method, path, protocol,
status, status_code, stream, model, source, source_type, source_name, api_key_id, api_key_name,
pool_id, pool_name, pool_model, member_model, fallback_count, limited_count, input_tokens,
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
	var stream, requestTruncated, responseTruncated int
	dest := []any{
		&record.ID, &record.TraceID, &record.ThreadID, &started, &completed, &record.Method,
		&record.Path, &record.Protocol, &record.Status, &record.StatusCode, &stream, &record.Model,
		&record.Source, &record.SourceType, &record.SourceName, &record.APIKeyID, &record.APIKeyName,
		&record.PoolID, &record.PoolName, &record.PoolModel, &record.MemberModel, &record.FallbackCount,
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

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
