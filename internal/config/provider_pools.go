package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ProviderPoolsFile stores user-defined model pools separately from provider
// credentials so existing providers.json remains backward compatible.
const ProviderPoolsFile = "provider_pools.json"

// ProviderPool exposes one public model ID and routes it to member models.
type ProviderPool struct {
	ID      string               `json:"id"`
	Name    string               `json:"name"`
	Model   string               `json:"model"`
	Enabled bool                 `json:"enabled"`
	Members []ProviderPoolMember `json:"members"`
}

// ProviderPoolMember identifies a concrete model source. Source is local,
// cloud, or provider:<provider-id>; Model is the model ID sent upstream.
type ProviderPoolMember struct {
	ID            string `json:"id"`
	Source        string `json:"source"`
	Model         string `json:"model"`
	Priority      int    `json:"priority,omitempty"`
	Weight        int    `json:"weight,omitempty"`
	RequestsPM    int    `json:"requests_per_minute,omitempty"`
	TokensPM      int    `json:"tokens_per_minute,omitempty"`
	MaxConcurrent int    `json:"max_concurrent,omitempty"`
}

var (
	providerPools     []ProviderPool
	providerPoolsOnce sync.Once
	providerPoolsMu   sync.RWMutex
	providerPoolsErr  error
)

func ProviderPoolsPath() (string, error) {
	home, err := AppHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ProviderPoolsFile), nil
}

func LoadProviderPools() ([]ProviderPool, error) {
	providerPoolsOnce.Do(func() {
		var loaded []ProviderPool
		path, err := ProviderPoolsPath()
		if err != nil {
			providerPoolsMu.Lock()
			providerPoolsErr = err
			providerPoolsMu.Unlock()
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				providerPoolsMu.Lock()
				providerPools = []ProviderPool{}
				providerPoolsMu.Unlock()
				return
			}
			providerPoolsMu.Lock()
			providerPoolsErr = err
			providerPoolsMu.Unlock()
			return
		}
		if err := json.Unmarshal(data, &loaded); err != nil {
			providerPoolsMu.Lock()
			providerPoolsErr = err
			providerPoolsMu.Unlock()
			return
		}
		providerPoolsMu.Lock()
		providerPools = normalizeProviderPools(loaded)
		providerPoolsErr = nil
		providerPoolsMu.Unlock()
	})
	providerPoolsMu.RLock()
	defer providerPoolsMu.RUnlock()
	return copyProviderPools(providerPools), providerPoolsErr
}

func GetProviderPools() []ProviderPool {
	providerPoolsMu.RLock()
	if providerPools != nil {
		out := copyProviderPools(providerPools)
		providerPoolsMu.RUnlock()
		return out
	}
	providerPoolsMu.RUnlock()
	pools, _ := LoadProviderPools()
	return pools
}

func SaveProviderPools(pools []ProviderPool) error {
	providerPoolsMu.Lock()
	defer providerPoolsMu.Unlock()
	path, err := ProviderPoolsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	pools = normalizeProviderPools(pools)
	data, err := json.MarshalIndent(pools, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	providerPools = pools
	providerPoolsErr = nil
	return nil
}

func ResetProviderPools() {
	providerPoolsMu.Lock()
	defer providerPoolsMu.Unlock()
	providerPools = nil
	providerPoolsErr = nil
	providerPoolsOnce = sync.Once{}
}

func normalizeProviderPools(pools []ProviderPool) []ProviderPool {
	out := make([]ProviderPool, 0, len(pools))
	ids := map[string]struct{}{}
	models := map[string]struct{}{}
	for _, pool := range pools {
		pool.ID = strings.TrimSpace(pool.ID)
		pool.Name = strings.TrimSpace(pool.Name)
		pool.Model = strings.TrimSpace(pool.Model)
		if pool.ID == "" || pool.Name == "" || pool.Model == "" {
			continue
		}
		if _, ok := ids[pool.ID]; ok {
			continue
		}
		if _, ok := models[pool.Model]; ok {
			continue
		}
		pool.Members = normalizeProviderPoolMembers(pool.Members)
		ids[pool.ID] = struct{}{}
		models[pool.Model] = struct{}{}
		out = append(out, pool)
	}
	return out
}

func normalizeProviderPoolMembers(members []ProviderPoolMember) []ProviderPoolMember {
	out := make([]ProviderPoolMember, 0, len(members))
	ids := map[string]struct{}{}
	for _, member := range members {
		member.ID = strings.TrimSpace(member.ID)
		member.Source = strings.TrimSpace(member.Source)
		member.Model = strings.TrimSpace(member.Model)
		if member.ID == "" || member.Source == "" || member.Model == "" {
			continue
		}
		if _, ok := ids[member.ID]; ok {
			continue
		}
		if member.Weight < 1 {
			member.Weight = 100
		}
		if member.Priority < 0 {
			member.Priority = 0
		}
		if member.RequestsPM < 0 {
			member.RequestsPM = 0
		}
		if member.TokensPM < 0 {
			member.TokensPM = 0
		}
		if member.MaxConcurrent < 0 {
			member.MaxConcurrent = 0
		}
		ids[member.ID] = struct{}{}
		out = append(out, member)
	}
	return out
}

func copyProviderPools(pools []ProviderPool) []ProviderPool {
	out := make([]ProviderPool, len(pools))
	for i, pool := range pools {
		out[i] = pool
		out[i].Members = append([]ProviderPoolMember{}, pool.Members...)
	}
	return out
}
