package server

import (
	"fmt"
	"testing"
	"time"
)

func TestDatasetExportJobStorePrunesExpiredTerminalJobs(t *testing.T) {
	now := time.Now().UTC()
	store := newDatasetExportJobStore()
	store.jobs["expired"] = &datasetExportJob{
		ID: "expired", Status: "completed", CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-25 * time.Hour),
	}
	store.jobs["recent"] = &datasetExportJob{
		ID: "recent", Status: "failed", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	store.jobs["running"] = &datasetExportJob{
		ID: "running", Status: "running", CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-48 * time.Hour),
	}

	if _, ok := store.Get("expired"); ok {
		t.Fatal("expired completed job was retained")
	}
	if _, ok := store.Get("recent"); !ok {
		t.Fatal("recent failed job was removed")
	}
	if _, ok := store.Get("running"); !ok {
		t.Fatal("active job was removed")
	}
}

func TestDatasetExportJobStoreKeepsMostRecentTerminalJobs(t *testing.T) {
	now := time.Now().UTC()
	store := newDatasetExportJobStore()
	for index := 0; index < datasetExportJobMaxStored+20; index++ {
		id := fmt.Sprintf("job-%03d", index)
		store.jobs[id] = &datasetExportJob{
			ID: id, Status: "completed", CreatedAt: now, UpdatedAt: now.Add(time.Duration(index) * time.Second),
		}
	}

	if _, ok := store.Get(fmt.Sprintf("job-%03d", datasetExportJobMaxStored+19)); !ok {
		t.Fatal("most recent completed job was removed")
	}
	if len(store.jobs) != datasetExportJobMaxStored {
		t.Fatalf("stored jobs = %d, want %d", len(store.jobs), datasetExportJobMaxStored)
	}
	if _, ok := store.jobs["job-000"]; ok {
		t.Fatal("oldest completed job was retained")
	}
}
