package apps

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/opencsgs/csglite/internal/safefile"
)

const (
	ProviderModeNative  = "native"
	ProviderModeOpenCSG = "opencsg"
)

type SourceSwitchStatus struct {
	Mode    string
	Drifted bool
}

type sourceFileSnapshot struct {
	Known   bool        `json:"known"`
	Existed bool        `json:"existed"`
	Data    []byte      `json:"data,omitempty"`
	Mode    os.FileMode `json:"mode,omitempty"`
}

type sourceSwitchRecord struct {
	Mode          string             `json:"mode"`
	Original      sourceFileSnapshot `json:"original"`
	ManagedSHA256 string             `json:"managed_sha256,omitempty"`
}

type SourceSwitchManager struct {
	root string
	mu   sync.Mutex
}

func NewSourceSwitchManager(storageRoot string) *SourceSwitchManager {
	return &SourceSwitchManager{
		root: filepath.Join(storageRoot, "apps", "config-switches"),
	}
}

func (m *SourceSwitchManager) Status(group, targetPath string, isManaged func([]byte) bool) (SourceSwitchStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	record, found, err := m.load(group)
	if err != nil {
		return SourceSwitchStatus{}, err
	}
	if !found {
		data, _, err := readOptionalFile(targetPath)
		if err != nil {
			return SourceSwitchStatus{}, err
		}
		mode := ProviderModeNative
		if isManaged(data) {
			mode = ProviderModeOpenCSG
		}
		return SourceSwitchStatus{Mode: mode}, nil
	}

	status := SourceSwitchStatus{Mode: normalizeProviderMode(record.Mode)}
	if status.Mode == ProviderModeOpenCSG && record.ManagedSHA256 != "" {
		data, _, err := readOptionalFile(targetPath)
		if err != nil {
			return SourceSwitchStatus{}, err
		}
		status.Drifted = fileDigest(data) != record.ManagedSHA256
	}
	return status, nil
}

func (m *SourceSwitchManager) UseOpenCSG(
	group, targetPath string,
	isManaged func([]byte) bool,
	apply func() error,
) (SourceSwitchStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	record, found, err := m.load(group)
	if err != nil {
		return SourceSwitchStatus{}, err
	}
	currentData, currentInfo, err := readOptionalFile(targetPath)
	if err != nil {
		return SourceSwitchStatus{}, err
	}
	if found && normalizeProviderMode(record.Mode) == ProviderModeOpenCSG &&
		record.ManagedSHA256 != "" && fileDigest(currentData) != record.ManagedSHA256 {
		return SourceSwitchStatus{Mode: ProviderModeOpenCSG, Drifted: true},
			fmt.Errorf("configuration changed after OpenCSG was enabled; restore or review the app configuration before switching again")
	}

	if !found || normalizeProviderMode(record.Mode) != ProviderModeOpenCSG {
		if isManaged(currentData) {
			// Legacy CSGLite versions did not keep a pre-switch snapshot.
			record.Original = sourceFileSnapshot{}
		} else {
			record.Original = snapshotOptionalFile(currentData, currentInfo)
		}
	}

	if err := apply(); err != nil {
		if restoreErr := restoreSnapshot(targetPath, snapshotOptionalFile(currentData, currentInfo)); restoreErr != nil {
			return SourceSwitchStatus{}, fmt.Errorf("%w (also failed to roll back configuration: %v)", err, restoreErr)
		}
		return SourceSwitchStatus{}, err
	}
	managedData, _, err := readOptionalFile(targetPath)
	if err != nil {
		if restoreErr := restoreSnapshot(targetPath, snapshotOptionalFile(currentData, currentInfo)); restoreErr != nil {
			return SourceSwitchStatus{}, fmt.Errorf("%w (also failed to roll back configuration: %v)", err, restoreErr)
		}
		return SourceSwitchStatus{}, err
	}
	record.Mode = ProviderModeOpenCSG
	record.ManagedSHA256 = fileDigest(managedData)
	if err := m.save(group, record); err != nil {
		if restoreErr := restoreSnapshot(targetPath, snapshotOptionalFile(currentData, currentInfo)); restoreErr != nil {
			return SourceSwitchStatus{}, fmt.Errorf("%w (also failed to roll back configuration: %v)", err, restoreErr)
		}
		return SourceSwitchStatus{}, err
	}
	return SourceSwitchStatus{Mode: ProviderModeOpenCSG}, nil
}

func (m *SourceSwitchManager) RestoreNative(
	group, targetPath string,
	isManaged func([]byte) bool,
	restoreLegacy func() error,
) (SourceSwitchStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	record, found, err := m.load(group)
	if err != nil {
		return SourceSwitchStatus{}, err
	}
	currentData, currentInfo, err := readOptionalFile(targetPath)
	if err != nil {
		return SourceSwitchStatus{}, err
	}
	if found && normalizeProviderMode(record.Mode) == ProviderModeOpenCSG &&
		record.ManagedSHA256 != "" && fileDigest(currentData) != record.ManagedSHA256 {
		return SourceSwitchStatus{Mode: ProviderModeOpenCSG, Drifted: true},
			fmt.Errorf("configuration changed after OpenCSG was enabled; refusing to overwrite the newer app configuration")
	}

	if found && record.Original.Known {
		if err := restoreSnapshot(targetPath, record.Original); err != nil {
			return SourceSwitchStatus{}, err
		}
	} else if isManaged(currentData) {
		if err := restoreLegacy(); err != nil {
			return SourceSwitchStatus{}, err
		}
	}

	record.Mode = ProviderModeNative
	record.ManagedSHA256 = ""
	if err := m.save(group, record); err != nil {
		if restoreErr := restoreSnapshot(targetPath, snapshotOptionalFile(currentData, currentInfo)); restoreErr != nil {
			return SourceSwitchStatus{}, fmt.Errorf("%w (also failed to roll back configuration: %v)", err, restoreErr)
		}
		return SourceSwitchStatus{}, err
	}
	return SourceSwitchStatus{Mode: ProviderModeNative}, nil
}

func (m *SourceSwitchManager) recordPath(group string) string {
	return filepath.Join(m.root, group+".json")
}

func (m *SourceSwitchManager) load(group string) (sourceSwitchRecord, bool, error) {
	data, err := os.ReadFile(m.recordPath(group))
	if errors.Is(err, os.ErrNotExist) {
		return sourceSwitchRecord{}, false, nil
	}
	if err != nil {
		return sourceSwitchRecord{}, false, err
	}
	var record sourceSwitchRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return sourceSwitchRecord{}, false, fmt.Errorf("reading source switch state: %w", err)
	}
	return record, true, nil
}

func (m *SourceSwitchManager) save(group string, record sourceSwitchRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return safefile.Write(m.recordPath(group), data, 0o600)
}

func normalizeProviderMode(mode string) string {
	if mode == ProviderModeOpenCSG {
		return ProviderModeOpenCSG
	}
	return ProviderModeNative
}

func readOptionalFile(path string) ([]byte, os.FileInfo, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	return data, info, nil
}

func snapshotOptionalFile(data []byte, info os.FileInfo) sourceFileSnapshot {
	snapshot := sourceFileSnapshot{Known: true, Existed: info != nil}
	if info != nil {
		snapshot.Data = append([]byte(nil), data...)
		snapshot.Mode = info.Mode().Perm()
	}
	return snapshot
}

func restoreSnapshot(path string, snapshot sourceFileSnapshot) error {
	if !snapshot.Existed {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	mode := snapshot.Mode
	if mode == 0 {
		mode = 0o600
	}
	return safefile.Write(path, snapshot.Data, mode)
}

func fileDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
