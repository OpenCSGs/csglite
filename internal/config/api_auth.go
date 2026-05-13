package config

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	APIKeysFile  = "api_keys.json"
	APIUsageFile = "api_usage.json"
)

type APIKeyRecord struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Prefix     string    `json:"prefix"`
	Hash       string    `json:"hash"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

type APIKeyState struct {
	AuthEnabled bool           `json:"auth_enabled"`
	Keys        []APIKeyRecord `json:"keys"`
}

type APIKeyStore struct {
	path string
	mu   sync.RWMutex
}

func NewAPIKeyStore(appHome string) *APIKeyStore {
	return &APIKeyStore{path: filepath.Join(appHome, APIKeysFile)}
}

func (s *APIKeyStore) State() (APIKeyState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadLocked()
}

func (s *APIKeyStore) SetAuthEnabled(enabled bool) (APIKeyState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return APIKeyState{}, err
	}
	state.AuthEnabled = enabled
	if err := s.saveLocked(state); err != nil {
		return APIKeyState{}, err
	}
	return state, nil
}

func (s *APIKeyStore) Create(name string) (APIKeyRecord, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		name = "Default API Key"
	}

	state, err := s.loadLocked()
	if err != nil {
		return APIKeyRecord{}, "", err
	}

	plain, err := generateAPIKeySecret()
	if err != nil {
		return APIKeyRecord{}, "", err
	}
	now := time.Now().UTC()
	id, err := secureRandomHex(8)
	if err != nil {
		return APIKeyRecord{}, "", err
	}
	record := APIKeyRecord{
		ID:        id,
		Name:      name,
		Prefix:    keyPrefix(plain),
		Hash:      hashAPIKey(plain),
		CreatedAt: now,
	}
	state.Keys = append(state.Keys, record)
	if err := s.saveLocked(state); err != nil {
		return APIKeyRecord{}, "", err
	}
	return record, plain, nil
}

func (s *APIKeyStore) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return false, err
	}
	next := state.Keys[:0]
	deleted := false
	for _, key := range state.Keys {
		if key.ID == id {
			deleted = true
			continue
		}
		next = append(next, key)
	}
	if !deleted {
		return false, nil
	}
	state.Keys = next
	if err := s.saveLocked(state); err != nil {
		return false, err
	}
	return true, nil
}

func (s *APIKeyStore) Validate(plain string) (APIKeyRecord, bool, error) {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return APIKeyRecord{}, false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return APIKeyRecord{}, false, err
	}
	hash := hashAPIKey(plain)
	for i := range state.Keys {
		if subtle.ConstantTimeCompare([]byte(state.Keys[i].Hash), []byte(hash)) == 1 {
			state.Keys[i].LastUsedAt = time.Now().UTC()
			record := state.Keys[i]
			if err := s.saveLocked(state); err != nil {
				return APIKeyRecord{}, false, err
			}
			return record, true, nil
		}
	}
	return APIKeyRecord{}, false, nil
}

func (s *APIKeyStore) loadLocked() (APIKeyState, error) {
	var state APIKeyState
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return APIKeyState{Keys: []APIKeyRecord{}}, nil
		}
		return APIKeyState{}, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return APIKeyState{}, err
	}
	if state.Keys == nil {
		state.Keys = []APIKeyRecord{}
	}
	return state, nil
}

func (s *APIKeyStore) saveLocked(state APIKeyState) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

type APIUsageEvent struct {
	APIKeyID     string
	APIKeyName   string
	Model        string
	InputTokens  int64
	OutputTokens int64
}

type APIUsageRecord struct {
	APIKeyID     string    `json:"api_key_id"`
	APIKeyName   string    `json:"api_key_name"`
	Model        string    `json:"model"`
	Requests     int64     `json:"requests"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	TotalTokens  int64     `json:"total_tokens"`
	LastUsedAt   time.Time `json:"last_used_at"`
}

type APIUsageState struct {
	Records []APIUsageRecord `json:"records"`
}

type APIUsageStore struct {
	path string
	mu   sync.Mutex
}

func NewAPIUsageStore(appHome string) *APIUsageStore {
	return &APIUsageStore{path: filepath.Join(appHome, APIUsageFile)}
}

func (s *APIUsageStore) Add(event APIUsageEvent) error {
	if strings.TrimSpace(event.APIKeyID) == "" || strings.TrimSpace(event.Model) == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for i := range state.Records {
		if state.Records[i].APIKeyID == event.APIKeyID && state.Records[i].Model == event.Model {
			state.Records[i].APIKeyName = event.APIKeyName
			state.Records[i].Requests++
			state.Records[i].InputTokens += event.InputTokens
			state.Records[i].OutputTokens += event.OutputTokens
			state.Records[i].TotalTokens += event.InputTokens + event.OutputTokens
			state.Records[i].LastUsedAt = now
			return s.saveLocked(state)
		}
	}
	state.Records = append(state.Records, APIUsageRecord{
		APIKeyID:     event.APIKeyID,
		APIKeyName:   event.APIKeyName,
		Model:        event.Model,
		Requests:     1,
		InputTokens:  event.InputTokens,
		OutputTokens: event.OutputTokens,
		TotalTokens:  event.InputTokens + event.OutputTokens,
		LastUsedAt:   now,
	})
	return s.saveLocked(state)
}

func (s *APIUsageStore) List() (APIUsageState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return APIUsageState{}, err
	}
	sort.Slice(state.Records, func(i, j int) bool {
		if state.Records[i].APIKeyName == state.Records[j].APIKeyName {
			return state.Records[i].Model < state.Records[j].Model
		}
		return state.Records[i].APIKeyName < state.Records[j].APIKeyName
	})
	return state, nil
}

func (s *APIUsageStore) loadLocked() (APIUsageState, error) {
	var state APIUsageState
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return APIUsageState{Records: []APIUsageRecord{}}, nil
		}
		return APIUsageState{}, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return APIUsageState{}, err
	}
	if state.Records == nil {
		state.Records = []APIUsageRecord{}
	}
	return state, nil
}

func (s *APIUsageStore) saveLocked(state APIUsageState) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func generateAPIKeySecret() (string, error) {
	secret, err := secureRandomHex(24)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("csglite_%s", secret), nil
}

func secureRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func keyPrefix(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:12]
}
