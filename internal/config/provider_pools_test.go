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
	if got := NormalizeProviderPoolPolicy(" semantic "); got != ProviderPoolPolicySemantic {
		t.Fatalf("semantic policy = %q", got)
	}
	if got := NormalizeProviderPoolPolicy("unknown"); got != ProviderPoolPolicyPriorityWeight {
		t.Fatalf("unknown policy = %q", got)
	}
}
