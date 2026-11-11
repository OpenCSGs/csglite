package license

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newActivationStore(t *testing.T) (*ActivationStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), activationFileName)
	return NewActivationStore(path), path
}

func TestActivationEnsurePendingKeepsReported(t *testing.T) {
	store, _ := newActivationStore(t)
	if _, err := store.EnsurePending("key-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	record, ok := store.Load()
	if !ok || record.Status != ActivationPending {
		t.Fatalf("record = %+v, want pending", record)
	}
	if err := store.MarkReported("key-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	// A repeated startup must not turn a reported device back into pending.
	record, err := store.EnsurePending("key-1", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != ActivationReported || record.ReportedAt == "" {
		t.Fatalf("record = %+v, want reported with a timestamp", record)
	}
}

func TestActivationResetsOnLicenseOrDeviceChange(t *testing.T) {
	store, _ := newActivationStore(t)
	if _, err := store.EnsurePending("key-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkReported("key-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRetry("key-1", "device-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	for _, change := range []struct{ key, device string }{
		{"key-2", "device-1"},
		{"key-1", "device-2"},
	} {
		record, err := store.EnsurePending(change.key, change.device)
		if err != nil {
			t.Fatal(err)
		}
		if record.Status != ActivationPending || record.NextRetryAt != "" || record.ReportedAt != "" {
			t.Fatalf("record after change to %v = %+v, want a fresh pending record", change, record)
		}
		if err := store.MarkReported(change.key, change.device); err != nil {
			t.Fatal(err)
		}
	}
}

func TestActivationRetryOnlyExtends(t *testing.T) {
	store, _ := newActivationStore(t)
	if _, err := store.EnsurePending("key-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	later := now.Add(time.Hour)
	if err := store.MarkRetry("key-1", "device-1", later); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRetry("key-1", "device-1", later.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	record, _ := store.Load()
	if record.NextRetryAt != later.Format(time.RFC3339) {
		t.Fatalf("next_retry_at = %q, want the later time %q", record.NextRetryAt, later.Format(time.RFC3339))
	}
	if store.Due(record) {
		t.Fatal("record must not be due before next_retry_at")
	}
	store.now = func() time.Time { return later.Add(time.Minute) }
	if !store.Due(record) {
		t.Fatal("record must be due after next_retry_at")
	}
}

func TestActivationUnmanagedClearsReportedAt(t *testing.T) {
	store, _ := newActivationStore(t)
	if _, err := store.EnsurePending("key-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkReported("key-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkUnmanaged("key-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	record, _ := store.Load()
	if record.Status != ActivationUnmanaged || record.ReportedAt != "" {
		t.Fatalf("record = %+v, want unmanaged without reported_at", record)
	}
}

func TestActivationReportedClearsRetry(t *testing.T) {
	store, _ := newActivationStore(t)
	if _, err := store.EnsurePending("key-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRetry("key-1", "device-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkReported("key-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	record, _ := store.Load()
	if record.Status != ActivationReported || record.NextRetryAt != "" {
		t.Fatalf("record = %+v, want reported without a retry time", record)
	}
}

func TestActivationCorruptFileIsTreatedAsAbsent(t *testing.T) {
	store, path := newActivationStore(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Load(); ok {
		t.Fatal("corrupt file must be reported as absent")
	}
	record, err := store.EnsurePending("key-1", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != ActivationPending {
		t.Fatalf("record = %+v, want pending", record)
	}
	if _, ok := store.Load(); !ok {
		t.Fatal("record should be readable after rebuilding")
	}
}

func TestActivationDelete(t *testing.T) {
	store, path := newActivationStore(t)
	if _, err := store.EnsurePending("key-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("state file still exists: %v", err)
	}
	// Deleting twice is not an error.
	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
}
