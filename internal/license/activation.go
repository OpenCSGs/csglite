package license

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/safefile"
)

// activationFileName is the write-off state kept beside the license file. It
// records whether this machine has been reported against the license, so a
// device is claimed once rather than on every start.
const activationFileName = "license.activation"

// ActivationStatus is the device write-off state, independent of the license's
// own validity (Status).
type ActivationStatus string

const (
	// ActivationPending means the device still has to be reported.
	ActivationPending ActivationStatus = "pending"
	// ActivationReported means the server recorded the device.
	ActivationReported ActivationStatus = "reported"
	// ActivationUnmanaged means the server's ledger does not know the license
	// (LICENSE-ERR-5). Retrying cannot help, so it is terminal.
	ActivationUnmanaged ActivationStatus = "unmanaged"
)

// Activation is the on-disk write-off record.
type Activation struct {
	LicenseKey string           `json:"license_key"`
	DeviceID   string           `json:"device_id"`
	Status     ActivationStatus `json:"status"`
	// ReportedAt is when the server last accepted the device.
	ReportedAt string `json:"reported_at,omitempty"`
	// NextRetryAt delays the next report after the server answered 404 without
	// a business error code, which is how an older server presents a missing
	// route.
	NextRetryAt string `json:"next_retry_at,omitempty"`
}

// ActivationStore reads and writes the write-off state. One instance is shared
// by the HTTP handlers, the CLI and the startup scan of a process, so every
// read-modify-write is serialized by mu.
type ActivationStore struct {
	path string
	now  func() time.Time

	mu sync.Mutex
}

// NewActivationStore returns a store for path. An empty path disables it.
func NewActivationStore(path string) *ActivationStore {
	return &ActivationStore{path: path, now: time.Now}
}

// Path returns the state file path, or empty when the store is disabled.
func (s *ActivationStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Load returns the current record. A missing, empty or corrupt file is
// reported as absent: a broken state file must never block startup or prevent
// a later report.
func (s *ActivationStore) Load() (Activation, bool) {
	if s == nil || s.path == "" {
		return Activation{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Delete removes the record. Removing an absent file is not an error.
func (s *ActivationStore) Delete() error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing activation state %s: %w", s.path, err)
	}
	return nil
}

// EnsurePending makes the record match the given license and device without
// downgrading a reported or unmanaged record. A different license key or
// device id starts a new record, because the previous write-off no longer
// describes this machine and license; this reset is intentional even when the
// old record was terminal, otherwise a replaced license or a moved storage root
// would never consume a seat. Callers that race with a license swap are
// guarded by the stale-outcome check in reportLicenseActivation.
func (s *ActivationStore) EnsurePending(licenseKey, deviceID string) (Activation, error) {
	if s == nil || s.path == "" {
		return Activation{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.load()
	if ok && current.LicenseKey == licenseKey && current.DeviceID == deviceID {
		return current, nil
	}
	next := Activation{LicenseKey: licenseKey, DeviceID: deviceID, Status: ActivationPending}
	if err := s.save(next); err != nil {
		return next, err
	}
	return next, nil
}

// MarkReported records a successful write-off, including one the server
// accepted with over_limit.
func (s *ActivationStore) MarkReported(licenseKey, deviceID string) error {
	return s.setStatus(licenseKey, deviceID, ActivationReported)
}

// MarkUnmanaged records a license the server's ledger does not know.
func (s *ActivationStore) MarkUnmanaged(licenseKey, deviceID string) error {
	return s.setStatus(licenseKey, deviceID, ActivationUnmanaged)
}

// MarkRetry keeps the record pending and delays the next attempt. The delay is
// only ever extended, never shortened, so concurrent writers cannot make the
// client hammer an older server.
func (s *ActivationStore) MarkRetry(licenseKey, deviceID string, at time.Time) error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.load()
	if !ok || current.LicenseKey != licenseKey || current.DeviceID != deviceID {
		current = Activation{LicenseKey: licenseKey, DeviceID: deviceID, Status: ActivationPending}
	}
	if current.Status != ActivationPending {
		return nil
	}
	next := at.UTC().Format(time.RFC3339)
	if current.NextRetryAt != "" {
		if existing, err := time.Parse(time.RFC3339, current.NextRetryAt); err == nil && existing.After(at) {
			next = existing.UTC().Format(time.RFC3339)
		}
	}
	current.NextRetryAt = next
	return s.save(current)
}

// Due reports whether a pending record may be reported now. A missing record
// is due, because the caller creates it first.
func (s *ActivationStore) Due(current Activation) bool {
	if current.Status != ActivationPending {
		return false
	}
	if strings.TrimSpace(current.NextRetryAt) == "" {
		return true
	}
	at, err := time.Parse(time.RFC3339, current.NextRetryAt)
	if err != nil {
		return true
	}
	return !s.now().Before(at)
}

func (s *ActivationStore) setStatus(licenseKey, deviceID string, status ActivationStatus) error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.load()
	if !ok || current.LicenseKey != licenseKey || current.DeviceID != deviceID {
		current = Activation{LicenseKey: licenseKey, DeviceID: deviceID}
	}
	current.Status = status
	current.NextRetryAt = ""
	if status == ActivationReported {
		current.ReportedAt = s.now().UTC().Format(time.RFC3339)
	} else {
		// reported_at only describes a reported record; keeping it after a
		// transition to unmanaged would leave a contradictory timestamp.
		current.ReportedAt = ""
	}
	return s.save(current)
}

func (s *ActivationStore) load() (Activation, bool) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("license: reading activation state: %v", err)
		}
		return Activation{}, false
	}
	var record Activation
	if err := json.Unmarshal(raw, &record); err != nil {
		log.Printf("license: activation state %s is unreadable, treating it as absent: %v", s.path, err)
		return Activation{}, false
	}
	if strings.TrimSpace(record.LicenseKey) == "" || strings.TrimSpace(record.DeviceID) == "" || record.Status == "" {
		log.Printf("license: activation state %s is incomplete, treating it as absent", s.path)
		return Activation{}, false
	}
	return record, true
}

func (s *ActivationStore) save(record Activation) error {
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding activation state: %w", err)
	}
	if err := safefile.Write(s.path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing activation state %s: %w", s.path, err)
	}
	return nil
}
