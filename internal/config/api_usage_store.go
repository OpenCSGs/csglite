package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// APIUsageDatabaseFile holds per-day usage buckets. It replaces the previous
// api_usage.json, which had to be rewritten in full on every metered request.
const APIUsageDatabaseFile = "api_usage.db"

const apiUsageSchema = `
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS api_usage_events (
	day TEXT NOT NULL,
	api_key_id TEXT NOT NULL,
	model TEXT NOT NULL,
	source TEXT NOT NULL,
	source_type TEXT NOT NULL,
	pool_id TEXT NOT NULL,
	pool_model TEXT NOT NULL,
	actual_member_id TEXT NOT NULL,
	member_model TEXT NOT NULL,
	cost_currency TEXT NOT NULL,
	cost_known INTEGER NOT NULL,
	api_key_name TEXT NOT NULL DEFAULT '',
	source_name TEXT NOT NULL DEFAULT '',
	pool_name TEXT NOT NULL DEFAULT '',
	estimated_cost REAL NOT NULL DEFAULT 0,
	fallback_count INTEGER NOT NULL DEFAULT 0,
	limited_count INTEGER NOT NULL DEFAULT 0,
	requests INTEGER NOT NULL DEFAULT 0,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	total_tokens INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (
		day, api_key_id, model, source, source_type,
		pool_id, pool_model, actual_member_id, member_model,
		cost_currency, cost_known
	)
);
CREATE INDEX IF NOT EXISTS idx_api_usage_events_day ON api_usage_events(day);
CREATE INDEX IF NOT EXISTS idx_api_usage_events_api_key ON api_usage_events(api_key_id, day);
CREATE TABLE IF NOT EXISTS api_usage_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

const apiUsageColumns = `day, api_key_id, model, source, source_type, pool_id, pool_model,
	actual_member_id, member_model, cost_currency, cost_known, api_key_name, source_name,
	pool_name, estimated_cost, fallback_count, limited_count, requests, input_tokens,
	output_tokens, total_tokens, created_at`

// apiUsageUpsertStatement folds a new event into its day bucket in a single
// write. Names fall back to the stored value when the new event omits them,
// matching latestNonEmpty.
const apiUsageUpsertStatement = `
INSERT INTO api_usage_events (` + apiUsageColumns + `)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (
	day, api_key_id, model, source, source_type,
	pool_id, pool_model, actual_member_id, member_model,
	cost_currency, cost_known
) DO UPDATE SET
	api_key_name = CASE WHEN excluded.api_key_name != '' THEN excluded.api_key_name ELSE api_usage_events.api_key_name END,
	source_name = CASE WHEN excluded.source_name != '' THEN excluded.source_name ELSE api_usage_events.source_name END,
	pool_name = CASE WHEN excluded.pool_name != '' THEN excluded.pool_name ELSE api_usage_events.pool_name END,
	estimated_cost = api_usage_events.estimated_cost + excluded.estimated_cost,
	fallback_count = api_usage_events.fallback_count + excluded.fallback_count,
	limited_count = api_usage_events.limited_count + excluded.limited_count,
	requests = api_usage_events.requests + excluded.requests,
	input_tokens = api_usage_events.input_tokens + excluded.input_tokens,
	output_tokens = api_usage_events.output_tokens + excluded.output_tokens,
	total_tokens = api_usage_events.total_tokens + excluded.total_tokens,
	created_at = MAX(api_usage_events.created_at, excluded.created_at)
`

type APIUsageStore struct {
	mu         sync.Mutex
	path       string
	legacyPath string
	db         *sql.DB
}

func NewAPIUsageStore(appHome string) *APIUsageStore {
	return &APIUsageStore{
		path:       filepath.Join(appHome, APIUsageDatabaseFile),
		legacyPath: filepath.Join(appHome, APIUsageFile),
	}
}

// Path reports the database file backing the store.
func (s *APIUsageStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *APIUsageStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	db := s.db
	s.db = nil
	return db.Close()
}

func (s *APIUsageStore) Add(event APIUsageEvent) error {
	if strings.TrimSpace(event.APIKeyID) == "" || strings.TrimSpace(event.Model) == "" {
		return nil
	}
	record, ok := apiUsageEventRecord(event)
	if !ok {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.openLocked()
	if err != nil {
		return err
	}
	_, err = db.Exec(apiUsageUpsertStatement, apiUsageInsertArgs(record)...)
	if err != nil {
		return fmt.Errorf("recording API usage: %w", err)
	}
	return nil
}

func (s *APIUsageStore) List(options APIUsageListOptions) (APIUsageState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.openLocked()
	if err != nil {
		return APIUsageState{}, err
	}
	events, err := apiUsageSelectEvents(db, options)
	if err != nil {
		return APIUsageState{}, err
	}
	events = compactAPIUsageEvents(events)
	records := aggregateAPIUsageEvents(events, options)
	sortAPIUsageRecords(records)
	return APIUsageState{Records: records, Events: filterAPIUsageEvents(events, options)}, nil
}

func (s *APIUsageStore) openLocked() (*sql.DB, error) {
	if s.db != nil {
		return s.db, nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return nil, fmt.Errorf("creating API usage directory: %w", err)
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return nil, fmt.Errorf("opening API usage database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(apiUsageSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initializing API usage database: %w", err)
	}
	s.db = db
	// A failed import must not take usage recording down: the JSON file stays
	// in place so the next start can retry it.
	if err := s.importLegacyLocked(db); err != nil {
		log.Printf("API USAGE: importing %s failed: %v", s.legacyPath, err)
	}
	return db, nil
}

// importLegacyLocked loads api_usage.json once and deletes it afterwards, so no
// stale copy is left behind. The import is also recorded in the database, so a
// file restored from elsewhere is not counted twice.
func (s *APIUsageStore) importLegacyLocked(db *sql.DB) error {
	if s.legacyPath == "" {
		return nil
	}
	data, err := os.ReadFile(s.legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	imported, err := apiUsageMetaValue(db, "legacy_json_imported")
	if err != nil {
		return err
	}
	if imported == "" {
		var state APIUsageState
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("parsing legacy API usage: %w", err)
		}
		migrateLegacyAPIUsageEvents(&state, time.Now().UTC())
		events := compactAPIUsageEvents(state.Events)
		if err := apiUsageInsertBatch(db, events); err != nil {
			return err
		}
		if err := apiUsageSetMeta(db, "legacy_json_imported", time.Now().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
		log.Printf("API USAGE: imported %d legacy usage buckets into %s", len(events), s.path)
	}
	if err := os.Remove(s.legacyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing imported API usage file: %w", err)
	}
	return nil
}

func apiUsageInsertBatch(db *sql.DB, events []APIUsageEventRecord) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	statement, err := tx.Prepare(apiUsageUpsertStatement)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, event := range events {
		if _, err := statement.Exec(apiUsageInsertArgs(event)...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func apiUsageSelectEvents(db *sql.DB, options APIUsageListOptions) ([]APIUsageEventRecord, error) {
	query := "SELECT " + apiUsageColumns + " FROM api_usage_events"
	var (
		conditions []string
		args       []any
	)
	// Day strings sort lexicographically, so a coarse range filter runs in the
	// index and the exact boundaries are still applied in Go.
	if options.Since != nil {
		conditions = append(conditions, "day >= ?")
		args = append(args, apiUsageEventDay(*options.Since))
	}
	if options.Until != nil {
		conditions = append(conditions, "day <= ?")
		args = append(args, apiUsageEventDay(*options.Until))
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC"
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("reading API usage: %w", err)
	}
	defer rows.Close()
	events := make([]APIUsageEventRecord, 0, 64)
	for rows.Next() {
		var (
			event     APIUsageEventRecord
			day       string
			costKnown int64
			createdAt int64
		)
		if err := rows.Scan(
			&day, &event.APIKeyID, &event.Model, &event.Source, &event.SourceType,
			&event.PoolID, &event.PoolModel, &event.ActualMemberID, &event.MemberModel,
			&event.CostCurrency, &costKnown, &event.APIKeyName, &event.SourceName,
			&event.PoolName, &event.EstimatedCost, &event.FallbackCount, &event.LimitedCount,
			&event.Requests, &event.InputTokens, &event.OutputTokens, &event.TotalTokens,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("reading API usage: %w", err)
		}
		event.CostKnown = costKnown != 0
		event.CreatedAt = apiUsageTimeFromStorage(createdAt)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading API usage: %w", err)
	}
	return events, nil
}

func apiUsageMetaValue(db *sql.DB, key string) (string, error) {
	var value string
	err := db.QueryRow("SELECT value FROM api_usage_meta WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

func apiUsageSetMeta(db *sql.DB, key, value string) error {
	_, err := db.Exec(
		"INSERT INTO api_usage_meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
		key, value,
	)
	return err
}

// apiUsageEventRecord normalizes an incoming event into the bucket shape used
// by both the database and the in-memory aggregation helpers.
func apiUsageEventRecord(event APIUsageEvent) (APIUsageEventRecord, bool) {
	createdAt := event.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	} else {
		createdAt = createdAt.UTC()
	}
	costKnown, costCurrency, estimatedCost := normalizeAPIUsageCost(
		event.CostKnown, event.CostCurrency, event.EstimatedCost,
	)
	compacted := compactAPIUsageEvents([]APIUsageEventRecord{{
		APIKeyID:       event.APIKeyID,
		APIKeyName:     event.APIKeyName,
		Model:          event.Model,
		Source:         event.Source,
		SourceType:     event.SourceType,
		SourceName:     event.SourceName,
		PoolID:         event.PoolID,
		PoolName:       event.PoolName,
		PoolModel:      event.PoolModel,
		ActualMemberID: event.ActualMemberID,
		MemberModel:    event.MemberModel,
		EstimatedCost:  estimatedCost,
		CostCurrency:   costCurrency,
		CostKnown:      costKnown,
		FallbackCount:  event.FallbackCount,
		LimitedCount:   event.LimitedCount,
		Requests:       1,
		InputTokens:    event.InputTokens,
		OutputTokens:   event.OutputTokens,
		TotalTokens:    event.InputTokens + event.OutputTokens,
		CreatedAt:      createdAt,
	}})
	if len(compacted) == 0 {
		return APIUsageEventRecord{}, false
	}
	return compacted[0], true
}

func apiUsageInsertArgs(event APIUsageEventRecord) []any {
	costKnown := 0
	if event.CostKnown {
		costKnown = 1
	}
	return []any{
		apiUsageEventDay(event.CreatedAt),
		event.APIKeyID,
		event.Model,
		event.Source,
		event.SourceType,
		event.PoolID,
		event.PoolModel,
		event.ActualMemberID,
		event.MemberModel,
		event.CostCurrency,
		costKnown,
		event.APIKeyName,
		event.SourceName,
		event.PoolName,
		event.EstimatedCost,
		event.FallbackCount,
		event.LimitedCount,
		apiUsageEventRequests(event),
		event.InputTokens,
		event.OutputTokens,
		apiUsageEventTotalTokens(event),
		apiUsageTimeToStorage(event.CreatedAt),
	}
}

func apiUsageTimeToStorage(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano()
}

func apiUsageTimeFromStorage(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}
