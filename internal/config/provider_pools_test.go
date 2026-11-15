package config

import (
	"encoding/json"
	"testing"
)

func TestNormalizeProviderPoolsDefaultsLegacyPolicy(t *testing.T) {
	var pools []ProviderPool
	if err := json.Unmarshal([]byte(`[{
		"id":"legacy","name":"Legacy","model":"legacy-model","enabled":true,
		"members":[{"id":"member","source":"cloud","model":"model"}]
	}]`), &pools); err != nil {
		t.Fatalf("decode legacy pools: %v", err)
	}
	normalized := normalizeProviderPools(pools)
	if len(normalized) != 1 || normalized[0].Policy != ProviderPoolPolicyPriorityWeight {
		t.Fatalf("normalized pools = %#v", normalized)
	}
}

func TestNormalizeProviderPoolPolicy(t *testing.T) {
	if got := NormalizeProviderPoolPolicy(" semantic "); got != ProviderPoolPolicyPriorityWeight {
		t.Fatalf("removed semantic policy = %q", got)
	}
	if got := NormalizeProviderPoolPolicy("unknown"); got != ProviderPoolPolicyPriorityWeight {
		t.Fatalf("unknown policy = %q", got)
	}
}

func TestModelServicesPersistAndKeepLegacyDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ResetProviderPools()
	t.Cleanup(ResetProviderPools)
	pools := []ProviderPool{}
	for _, policy := range []string{"", ProviderPoolPolicyFixed, ProviderPoolPolicyFailover, ProviderPoolPolicyWeighted} {
		id := "service-" + policy
		pools = append(pools, ProviderPool{ID: id, Name: id, Model: id, Enabled: true, Policy: policy, Category: "embedding", Members: []ProviderPoolMember{{ID: "one", Source: "provider:p", Model: "upstream", Disabled: policy != ""}}})
	}
	if err := SaveProviderPools(pools); err != nil {
		t.Fatal(err)
	}
	ResetProviderPools()
	loaded, err := LoadProviderPools()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(pools) {
		t.Fatalf("loaded %d services", len(loaded))
	}
	for i, pool := range loaded {
		if pool.Policy != NormalizeProviderPoolPolicy(pools[i].Policy) || pool.Category != "embedding" || pool.Members[0].Disabled != pools[i].Members[0].Disabled {
			t.Fatalf("round-trip changed service: %#v", pool)
		}
	}
}
