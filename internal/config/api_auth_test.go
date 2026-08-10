package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAPIUsageMigratesLegacyRequestCounts(t *testing.T) {
	dir := t.TempDir()
	usedAt := time.Date(2026, 5, 15, 8, 30, 0, 0, time.UTC)
	legacy := APIUsageState{
		Records: []APIUsageRecord{
			{
				APIKeyID:     "key-1",
				APIKeyName:   "client",
				Model:        "test/model",
				Source:       "local",
				SourceType:   "local",
				Requests:     42,
				InputTokens:  100,
				OutputTokens: 23,
				TotalTokens:  123,
				LastUsedAt:   usedAt,
			},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy usage: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, APIUsageFile), data, 0o600); err != nil {
		t.Fatalf("write legacy usage: %v", err)
	}

	store := NewAPIUsageStore(dir)
	state, err := store.List(APIUsageListOptions{})
	if err != nil {
		t.Fatalf("list usage: %v", err)
	}
	if len(state.Records) != 1 {
		t.Fatalf("records = %#v, want one", state.Records)
	}
	record := state.Records[0]
	if record.Requests != 42 || record.InputTokens != 100 || record.OutputTokens != 23 || record.TotalTokens != 123 {
		t.Fatalf("migrated record = %#v, want legacy totals preserved", record)
	}
}

func TestAPIUsageCompactsEventsByDayAndSource(t *testing.T) {
	dir := t.TempDir()
	store := NewAPIUsageStore(dir)
	first := time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)
	events := []APIUsageEvent{
		{
			APIKeyID:     "key-1",
			APIKeyName:   "client",
			Model:        "test/model",
			Source:       "provider:a",
			SourceType:   "provider",
			SourceName:   "Provider A",
			InputTokens:  1,
			OutputTokens: 2,
			CreatedAt:    first,
		},
		{
			APIKeyID:     "key-1",
			APIKeyName:   "client",
			Model:        "test/model",
			Source:       "provider:a",
			SourceType:   "provider",
			SourceName:   "Provider A",
			InputTokens:  3,
			OutputTokens: 4,
			CreatedAt:    first.Add(2 * time.Hour),
		},
		{
			APIKeyID:     "key-1",
			APIKeyName:   "client",
			Model:        "test/model",
			Source:       "provider:a",
			SourceType:   "provider",
			SourceName:   "Provider A",
			InputTokens:  5,
			OutputTokens: 6,
			CreatedAt:    first.AddDate(0, 0, 1),
		},
	}
	for _, event := range events {
		if err := store.Add(event); err != nil {
			t.Fatalf("add usage: %v", err)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, APIUsageFile))
	if err != nil {
		t.Fatalf("read usage file: %v", err)
	}
	var persisted APIUsageState
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decode persisted usage: %v", err)
	}
	if len(persisted.Events) != 2 {
		t.Fatalf("events = %#v, want two daily buckets", persisted.Events)
	}
	if persisted.Events[0].Requests != 2 || persisted.Events[0].InputTokens != 4 || persisted.Events[0].OutputTokens != 6 {
		t.Fatalf("first bucket = %#v, want same-day usage compacted", persisted.Events[0])
	}

	state, err := store.List(APIUsageListOptions{})
	if err != nil {
		t.Fatalf("list usage: %v", err)
	}
	if len(state.Records) != 1 || state.Records[0].Requests != 3 || state.Records[0].TotalTokens != 21 {
		t.Fatalf("records = %#v, want aggregate across buckets", state.Records)
	}
}

func TestAPIUsageRecordsSortByLastUsedDescending(t *testing.T) {
	older := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	records := []APIUsageRecord{
		{APIKeyID: "key-a", Model: "older-model", LastUsedAt: older},
		{APIKeyID: "key-b", Model: "newer-model", LastUsedAt: newer},
		{APIKeyID: "key-c", Model: "same-time-b", MemberModel: "member-b", LastUsedAt: newer},
		{APIKeyID: "key-d", Model: "same-time-a", MemberModel: "member-a", LastUsedAt: newer},
	}

	sortAPIUsageRecords(records)

	want := []string{"newer-model", "same-time-a", "same-time-b", "older-model"}
	for i, model := range want {
		if records[i].Model != model {
			t.Fatalf("record %d model = %q, want %q (records=%#v)", i, records[i].Model, model, records)
		}
	}
}

func TestAPIUsagePoolMetadataAggregatesAndFiltersWithoutBreakingLegacyEvents(t *testing.T) {
	dir := t.TempDir()
	usedAt := time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)
	legacy := `{"records":[],"events":[{"api_key_id":"legacy","api_key_name":"Legacy","model":"old-model","source":"cloud","source_type":"cloud","requests":1,"input_tokens":2,"output_tokens":3,"total_tokens":5,"created_at":"2026-06-19T09:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(dir, APIUsageFile), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy usage: %v", err)
	}
	store := NewAPIUsageStore(dir)
	for _, event := range []APIUsageEvent{
		{
			APIKeyID: "key", APIKeyName: "Client", Model: "public-model",
			Source: "provider:member", SourceType: "provider", SourceName: "Member Provider",
			PoolID: "pool-id", PoolName: "Primary Pool", PoolModel: "public-model", MemberModel: "upstream-model",
			FallbackCount: 1, LimitedCount: 1, InputTokens: 4, OutputTokens: 6, CreatedAt: usedAt,
		},
		{
			APIKeyID: "key", APIKeyName: "Client", Model: "public-model",
			Source: "provider:member", SourceType: "provider", SourceName: "Member Provider",
			PoolID: "pool-id", PoolName: "Primary Pool", PoolModel: "public-model", MemberModel: "upstream-model",
			FallbackCount: 2, InputTokens: 1, OutputTokens: 2, CreatedAt: usedAt.Add(time.Hour),
		},
	} {
		if err := store.Add(event); err != nil {
			t.Fatalf("add pool usage: %v", err)
		}
	}

	state, err := store.List(APIUsageListOptions{Pool: "primary pool", Provider: "Member Provider"})
	if err != nil {
		t.Fatalf("list filtered pool usage: %v", err)
	}
	if len(state.Records) != 1 {
		t.Fatalf("records = %#v, want one pool member aggregate", state.Records)
	}
	record := state.Records[0]
	if record.Requests != 2 || record.TotalTokens != 13 || record.FallbackCount != 3 || record.LimitedCount != 1 {
		t.Fatalf("pool counters = %#v, want compacted totals", record)
	}
	if record.PoolID != "pool-id" || record.PoolModel != "public-model" || record.MemberModel != "upstream-model" {
		t.Fatalf("pool attribution = %#v", record)
	}

	all, err := store.List(APIUsageListOptions{})
	if err != nil {
		t.Fatalf("list all usage: %v", err)
	}
	if len(all.Records) != 2 {
		t.Fatalf("records = %#v, want legacy and pool usage readable", all.Records)
	}
}
