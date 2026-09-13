package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type persistedAPIUsageBucket struct {
	day          string
	apiKeyID     string
	model        string
	requests     int64
	inputTokens  int64
	outputTokens int64
	totalTokens  int64
}

func openPersistedAPIUsage(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, APIUsageDatabaseFile))
	if err != nil {
		t.Fatalf("open usage database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// readPersistedAPIUsageBuckets reads what actually landed on disk, so tests
// assert against the database rather than the store's in-memory aggregation.
func readPersistedAPIUsageBuckets(t *testing.T, dir string) []persistedAPIUsageBucket {
	t.Helper()
	rows, err := openPersistedAPIUsage(t, dir).Query(
		"SELECT day, api_key_id, model, requests, input_tokens, output_tokens, total_tokens " +
			"FROM api_usage_events ORDER BY day, api_key_id, model",
	)
	if err != nil {
		t.Fatalf("read usage buckets: %v", err)
	}
	defer rows.Close()
	var buckets []persistedAPIUsageBucket
	for rows.Next() {
		var bucket persistedAPIUsageBucket
		if err := rows.Scan(&bucket.day, &bucket.apiKeyID, &bucket.model, &bucket.requests,
			&bucket.inputTokens, &bucket.outputTokens, &bucket.totalTokens); err != nil {
			t.Fatalf("scan usage bucket: %v", err)
		}
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read usage buckets: %v", err)
	}
	return buckets
}

func persistedAPIUsageColumns(t *testing.T, dir string) []string {
	t.Helper()
	rows, err := openPersistedAPIUsage(t, dir).Query("PRAGMA table_info(api_usage_events)")
	if err != nil {
		t.Fatalf("inspect usage table: %v", err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var (
			cid, notNull, primaryKey int
			name, columnType         string
			defaultValue             any
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan usage column: %v", err)
		}
		columns = append(columns, name)
	}
	return columns
}

func TestAPIUsageImportsLegacyJSONOnceAndRemovesIt(t *testing.T) {
	dir := t.TempDir()
	legacy := APIUsageState{
		Events: []APIUsageEventRecord{
			{
				APIKeyID: "key-1", APIKeyName: "client", Model: "test/model",
				Source: "provider:a", SourceType: "provider", SourceName: "Provider A",
				Requests: 4, InputTokens: 10, OutputTokens: 6, TotalTokens: 16,
				CreatedAt: time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC),
			},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(dir, APIUsageFile)
	if err := os.WriteFile(legacyPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewAPIUsageStore(dir)
	state, err := store.List(APIUsageListOptions{})
	if err != nil {
		t.Fatalf("list usage: %v", err)
	}
	if len(state.Records) != 1 || state.Records[0].Requests != 4 || state.Records[0].TotalTokens != 16 {
		t.Fatalf("imported records = %#v, want the legacy bucket", state.Records)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("imported file was left behind: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// A file restored from elsewhere must not double count: the import is
	// marked done in the database, not only by the file being gone.
	if err := os.WriteFile(legacyPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	reopened := NewAPIUsageStore(dir)
	t.Cleanup(func() { _ = reopened.Close() })
	state, err = reopened.List(APIUsageListOptions{})
	if err != nil {
		t.Fatalf("list usage after reopen: %v", err)
	}
	if len(state.Records) != 1 || state.Records[0].Requests != 4 || state.Records[0].TotalTokens != 16 {
		t.Fatalf("records after reopen = %#v, want the import to run only once", state.Records)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("restored file was left behind: %v", err)
	}
}

func TestAPIUsageConcurrentAddsAccumulateWithoutLoss(t *testing.T) {
	dir := t.TempDir()
	store := NewAPIUsageStore(dir)
	t.Cleanup(func() { _ = store.Close() })
	day := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

	const workers, each = 16, 25
	var wg sync.WaitGroup
	errs := make(chan error, workers*each)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := store.Add(APIUsageEvent{
					APIKeyID: fmt.Sprintf("key-%d", w%2), APIKeyName: "client",
					Model: "test/model", Source: "provider:a", SourceType: "provider",
					SourceName: "Provider A", InputTokens: 2, OutputTokens: 3,
					CreatedAt: day.Add(time.Duration(i) * time.Minute),
				}); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent add: %v", err)
	}

	buckets := readPersistedAPIUsageBuckets(t, dir)
	if len(buckets) != 2 {
		t.Fatalf("buckets = %#v, want one per API key and day", buckets)
	}
	for _, bucket := range buckets {
		if bucket.requests != workers/2*each {
			t.Fatalf("bucket %#v lost writes, want %d requests", bucket, workers/2*each)
		}
		if bucket.totalTokens != int64(workers/2*each*5) {
			t.Fatalf("bucket %#v lost tokens", bucket)
		}
	}
}
