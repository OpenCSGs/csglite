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
	APIKeyID      string
	APIKeyName    string
	Model         string
	Source        string
	SourceType    string
	SourceName    string
	PoolID        string
	PoolName      string
	PoolModel     string
	MemberModel   string
	FallbackCount int64
	LimitedCount  int64
	InputTokens   int64
	OutputTokens  int64
	CreatedAt     time.Time
}

type APIUsageRecord struct {
	APIKeyID      string    `json:"api_key_id"`
	APIKeyName    string    `json:"api_key_name"`
	Model         string    `json:"model"`
	Source        string    `json:"source,omitempty"`
	SourceType    string    `json:"source_type,omitempty"`
	SourceName    string    `json:"source_name,omitempty"`
	PoolID        string    `json:"pool_id,omitempty"`
	PoolName      string    `json:"pool_name,omitempty"`
	PoolModel     string    `json:"pool_model,omitempty"`
	MemberModel   string    `json:"member_model,omitempty"`
	FallbackCount int64     `json:"fallback_count,omitempty"`
	LimitedCount  int64     `json:"limited_count,omitempty"`
	Requests      int64     `json:"requests"`
	InputTokens   int64     `json:"input_tokens"`
	OutputTokens  int64     `json:"output_tokens"`
	TotalTokens   int64     `json:"total_tokens"`
	LastUsedAt    time.Time `json:"last_used_at"`
}

type APIUsageEventRecord struct {
	APIKeyID      string    `json:"api_key_id"`
	APIKeyName    string    `json:"api_key_name"`
	Model         string    `json:"model"`
	Source        string    `json:"source,omitempty"`
	SourceType    string    `json:"source_type,omitempty"`
	SourceName    string    `json:"source_name,omitempty"`
	PoolID        string    `json:"pool_id,omitempty"`
	PoolName      string    `json:"pool_name,omitempty"`
	PoolModel     string    `json:"pool_model,omitempty"`
	MemberModel   string    `json:"member_model,omitempty"`
	FallbackCount int64     `json:"fallback_count,omitempty"`
	LimitedCount  int64     `json:"limited_count,omitempty"`
	Requests      int64     `json:"requests,omitempty"`
	InputTokens   int64     `json:"input_tokens"`
	OutputTokens  int64     `json:"output_tokens"`
	TotalTokens   int64     `json:"total_tokens"`
	CreatedAt     time.Time `json:"created_at"`
}

type APIUsageState struct {
	Records []APIUsageRecord      `json:"records"`
	Events  []APIUsageEventRecord `json:"events,omitempty"`
}

type APIUsageListOptions struct {
	Since    *time.Time
	Until    *time.Time
	Provider string
	Pool     string
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
	migrateLegacyAPIUsageEvents(&state, time.Now().UTC())
	state.Events = compactAPIUsageEvents(state.Events)
	now := event.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	record := APIUsageEventRecord{
		APIKeyID:      event.APIKeyID,
		APIKeyName:    event.APIKeyName,
		Model:         event.Model,
		Source:        strings.TrimSpace(event.Source),
		SourceType:    strings.TrimSpace(event.SourceType),
		SourceName:    strings.TrimSpace(event.SourceName),
		PoolID:        strings.TrimSpace(event.PoolID),
		PoolName:      strings.TrimSpace(event.PoolName),
		PoolModel:     strings.TrimSpace(event.PoolModel),
		MemberModel:   strings.TrimSpace(event.MemberModel),
		FallbackCount: event.FallbackCount,
		LimitedCount:  event.LimitedCount,
		Requests:      1,
		InputTokens:   event.InputTokens,
		OutputTokens:  event.OutputTokens,
		TotalTokens:   event.InputTokens + event.OutputTokens,
		CreatedAt:     now,
	}
	upsertAPIUsageEventBucket(&state.Events, record)
	state.Records = aggregateAPIUsageEvents(state.Events, APIUsageListOptions{})
	sortAPIUsageRecords(state.Records)
	return s.saveLocked(state)
}

func (s *APIUsageStore) List(options APIUsageListOptions) (APIUsageState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return APIUsageState{}, err
	}
	migrateLegacyAPIUsageEvents(&state, time.Now().UTC())
	if len(state.Events) > 0 {
		state.Events = compactAPIUsageEvents(state.Events)
		state.Records = aggregateAPIUsageEvents(state.Events, options)
	} else {
		state.Records = filterAPIUsageRecords(state.Records, options)
	}
	sortAPIUsageRecords(state.Records)
	return APIUsageState{Records: state.Records, Events: filterAPIUsageEvents(state.Events, options)}, nil
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
	if state.Events == nil {
		state.Events = []APIUsageEventRecord{}
	}
	return state, nil
}

func migrateLegacyAPIUsageEvents(state *APIUsageState, fallback time.Time) {
	if len(state.Events) > 0 || len(state.Records) == 0 {
		return
	}
	if fallback.IsZero() {
		fallback = time.Now().UTC()
	}
	for _, record := range state.Records {
		usedAt := record.LastUsedAt
		if usedAt.IsZero() {
			usedAt = fallback
		}
		state.Events = append(state.Events, APIUsageEventRecord{
			APIKeyID:      record.APIKeyID,
			APIKeyName:    record.APIKeyName,
			Model:         record.Model,
			Source:        record.Source,
			SourceType:    record.SourceType,
			SourceName:    record.SourceName,
			PoolID:        record.PoolID,
			PoolName:      record.PoolName,
			PoolModel:     record.PoolModel,
			MemberModel:   record.MemberModel,
			FallbackCount: record.FallbackCount,
			LimitedCount:  record.LimitedCount,
			Requests:      apiUsageRecordRequests(record),
			InputTokens:   record.InputTokens,
			OutputTokens:  record.OutputTokens,
			TotalTokens:   record.TotalTokens,
			CreatedAt:     usedAt.UTC(),
		})
	}
}

func compactAPIUsageEvents(events []APIUsageEventRecord) []APIUsageEventRecord {
	if len(events) == 0 {
		return []APIUsageEventRecord{}
	}
	out := make([]APIUsageEventRecord, 0, len(events))
	index := make(map[string]int, len(events))
	for _, event := range events {
		event.APIKeyID = strings.TrimSpace(event.APIKeyID)
		event.APIKeyName = strings.TrimSpace(event.APIKeyName)
		event.Model = strings.TrimSpace(event.Model)
		event.Source = strings.TrimSpace(event.Source)
		event.SourceType = strings.TrimSpace(event.SourceType)
		event.SourceName = strings.TrimSpace(event.SourceName)
		event.PoolID = strings.TrimSpace(event.PoolID)
		event.PoolName = strings.TrimSpace(event.PoolName)
		event.PoolModel = strings.TrimSpace(event.PoolModel)
		event.MemberModel = strings.TrimSpace(event.MemberModel)
		event.Requests = apiUsageEventRequests(event)
		event.TotalTokens = apiUsageEventTotalTokens(event)
		if !event.CreatedAt.IsZero() {
			event.CreatedAt = event.CreatedAt.UTC()
		}
		key := apiUsageEventBucketKey(event)
		if i, ok := index[key]; ok {
			out[i].APIKeyName = latestNonEmpty(out[i].APIKeyName, event.APIKeyName)
			out[i].SourceName = latestNonEmpty(out[i].SourceName, event.SourceName)
			out[i].PoolName = latestNonEmpty(out[i].PoolName, event.PoolName)
			out[i].Requests += event.Requests
			out[i].FallbackCount += event.FallbackCount
			out[i].LimitedCount += event.LimitedCount
			out[i].InputTokens += event.InputTokens
			out[i].OutputTokens += event.OutputTokens
			out[i].TotalTokens += event.TotalTokens
			if event.CreatedAt.After(out[i].CreatedAt) {
				out[i].CreatedAt = event.CreatedAt
			}
			continue
		}
		index[key] = len(out)
		out = append(out, event)
	}
	sortAPIUsageEvents(out)
	return out
}

func upsertAPIUsageEventBucket(events *[]APIUsageEventRecord, event APIUsageEventRecord) {
	compacted := compactAPIUsageEvents([]APIUsageEventRecord{event})
	if len(compacted) == 0 {
		return
	}
	event = compacted[0]
	key := apiUsageEventBucketKey(event)
	for i := range *events {
		if apiUsageEventBucketKey((*events)[i]) == key {
			(*events)[i].APIKeyName = latestNonEmpty((*events)[i].APIKeyName, event.APIKeyName)
			(*events)[i].SourceName = latestNonEmpty((*events)[i].SourceName, event.SourceName)
			(*events)[i].PoolName = latestNonEmpty((*events)[i].PoolName, event.PoolName)
			(*events)[i].Requests += apiUsageEventRequests(event)
			(*events)[i].FallbackCount += event.FallbackCount
			(*events)[i].LimitedCount += event.LimitedCount
			(*events)[i].InputTokens += event.InputTokens
			(*events)[i].OutputTokens += event.OutputTokens
			(*events)[i].TotalTokens += apiUsageEventTotalTokens(event)
			if event.CreatedAt.After((*events)[i].CreatedAt) {
				(*events)[i].CreatedAt = event.CreatedAt
			}
			return
		}
	}
	*events = append(*events, event)
	sortAPIUsageEvents(*events)
}

func upsertAPIUsageRecord(state *APIUsageState, event APIUsageEventRecord) {
	requests := apiUsageEventRequests(event)
	for i := range state.Records {
		if state.Records[i].APIKeyID == event.APIKeyID &&
			state.Records[i].Model == event.Model &&
			state.Records[i].Source == event.Source &&
			state.Records[i].SourceType == event.SourceType &&
			state.Records[i].PoolID == event.PoolID &&
			state.Records[i].PoolModel == event.PoolModel &&
			state.Records[i].MemberModel == event.MemberModel {
			state.Records[i].APIKeyName = event.APIKeyName
			state.Records[i].SourceName = event.SourceName
			state.Records[i].PoolName = latestNonEmpty(state.Records[i].PoolName, event.PoolName)
			state.Records[i].Requests += requests
			state.Records[i].FallbackCount += event.FallbackCount
			state.Records[i].LimitedCount += event.LimitedCount
			state.Records[i].InputTokens += event.InputTokens
			state.Records[i].OutputTokens += event.OutputTokens
			state.Records[i].TotalTokens += apiUsageEventTotalTokens(event)
			state.Records[i].LastUsedAt = event.CreatedAt
			return
		}
	}
	state.Records = append(state.Records, APIUsageRecord{
		APIKeyID:      event.APIKeyID,
		APIKeyName:    event.APIKeyName,
		Model:         event.Model,
		Source:        event.Source,
		SourceType:    event.SourceType,
		SourceName:    event.SourceName,
		PoolID:        event.PoolID,
		PoolName:      event.PoolName,
		PoolModel:     event.PoolModel,
		MemberModel:   event.MemberModel,
		FallbackCount: event.FallbackCount,
		LimitedCount:  event.LimitedCount,
		Requests:      requests,
		InputTokens:   event.InputTokens,
		OutputTokens:  event.OutputTokens,
		TotalTokens:   apiUsageEventTotalTokens(event),
		LastUsedAt:    event.CreatedAt,
	})
}

func aggregateAPIUsageEvents(events []APIUsageEventRecord, options APIUsageListOptions) []APIUsageRecord {
	state := APIUsageState{Records: []APIUsageRecord{}}
	for _, event := range events {
		if !apiUsageTimeInRange(event.CreatedAt, options) {
			continue
		}
		if !apiUsageMatches(event.Source, event.SourceName, event.PoolID, event.PoolName, event.PoolModel, options) {
			continue
		}
		upsertAPIUsageRecord(&state, event)
	}
	return state.Records
}

func apiUsageEventBucketKey(event APIUsageEventRecord) string {
	return strings.Join([]string{
		strings.TrimSpace(event.APIKeyID),
		strings.TrimSpace(event.Model),
		strings.TrimSpace(event.Source),
		strings.TrimSpace(event.SourceType),
		strings.TrimSpace(event.PoolID),
		strings.TrimSpace(event.PoolModel),
		strings.TrimSpace(event.MemberModel),
		apiUsageEventDay(event.CreatedAt),
	}, "\x00")
}

func apiUsageEventDay(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format("2006-01-02")
}

func apiUsageRecordRequests(record APIUsageRecord) int64 {
	if record.Requests > 0 {
		return record.Requests
	}
	return 1
}

func apiUsageEventRequests(event APIUsageEventRecord) int64 {
	if event.Requests > 0 {
		return event.Requests
	}
	return 1
}

func apiUsageEventTotalTokens(event APIUsageEventRecord) int64 {
	if event.TotalTokens != 0 {
		return event.TotalTokens
	}
	return event.InputTokens + event.OutputTokens
}

func latestNonEmpty(current, next string) string {
	next = strings.TrimSpace(next)
	if next != "" {
		return next
	}
	return strings.TrimSpace(current)
}

func filterAPIUsageRecords(records []APIUsageRecord, options APIUsageListOptions) []APIUsageRecord {
	out := make([]APIUsageRecord, 0, len(records))
	for _, record := range records {
		if apiUsageTimeInRange(record.LastUsedAt, options) && apiUsageMatches(record.Source, record.SourceName, record.PoolID, record.PoolName, record.PoolModel, options) {
			out = append(out, record)
		}
	}
	return out
}

func filterAPIUsageEvents(events []APIUsageEventRecord, options APIUsageListOptions) []APIUsageEventRecord {
	out := make([]APIUsageEventRecord, 0, len(events))
	for _, event := range events {
		if apiUsageTimeInRange(event.CreatedAt, options) && apiUsageMatches(event.Source, event.SourceName, event.PoolID, event.PoolName, event.PoolModel, options) {
			out = append(out, event)
		}
	}
	return out
}

func apiUsageProviderMatches(source, sourceName string, options APIUsageListOptions) bool {
	provider := strings.TrimSpace(options.Provider)
	if provider == "" {
		return true
	}
	source = strings.TrimSpace(source)
	sourceName = strings.TrimSpace(sourceName)
	if strings.EqualFold(sourceName, provider) || strings.EqualFold(source, provider) {
		return true
	}
	if strings.HasPrefix(strings.ToLower(source), "provider:") {
		return strings.EqualFold(strings.TrimSpace(source[len("provider:"):]), provider)
	}
	return false
}

func apiUsageMatches(source, sourceName, poolID, poolName, poolModel string, options APIUsageListOptions) bool {
	if !apiUsageProviderMatches(source, sourceName, options) {
		return false
	}
	pool := strings.TrimSpace(options.Pool)
	if pool == "" {
		return true
	}
	poolID = strings.TrimSpace(poolID)
	poolName = strings.TrimSpace(poolName)
	poolModel = strings.TrimSpace(poolModel)
	return strings.EqualFold(poolID, pool) ||
		strings.EqualFold(poolName, pool) ||
		strings.EqualFold(poolModel, pool) ||
		strings.EqualFold("pool:"+poolID, pool)
}

func apiUsageTimeInRange(value time.Time, options APIUsageListOptions) bool {
	if value.IsZero() {
		return options.Since == nil && options.Until == nil
	}
	value = value.UTC()
	if options.Since != nil && value.Before(options.Since.UTC()) {
		return false
	}
	if options.Until != nil && !value.Before(options.Until.UTC()) {
		return false
	}
	return true
}

func sortAPIUsageRecords(records []APIUsageRecord) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].APIKeyName != records[j].APIKeyName {
			return records[i].APIKeyName < records[j].APIKeyName
		}
		if records[i].SourceType != records[j].SourceType {
			return records[i].SourceType < records[j].SourceType
		}
		if records[i].SourceName != records[j].SourceName {
			return records[i].SourceName < records[j].SourceName
		}
		return records[i].Model < records[j].Model
	})
}

func sortAPIUsageEvents(events []APIUsageEventRecord) {
	sort.Slice(events, func(i, j int) bool {
		if !events[i].CreatedAt.Equal(events[j].CreatedAt) {
			return events[i].CreatedAt.Before(events[j].CreatedAt)
		}
		if events[i].APIKeyName != events[j].APIKeyName {
			return events[i].APIKeyName < events[j].APIKeyName
		}
		if events[i].SourceType != events[j].SourceType {
			return events[i].SourceType < events[j].SourceType
		}
		if events[i].SourceName != events[j].SourceName {
			return events[i].SourceName < events[j].SourceName
		}
		return events[i].Model < events[j].Model
	})
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
