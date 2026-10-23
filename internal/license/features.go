package license

import (
	"fmt"
	"strings"
)

// FeatureType is the value type of a catalog entry, matching starhub-server's
// common/types.FeatureType.
type FeatureType string

const (
	FeatureTypeBoolean FeatureType = "boolean"
	FeatureTypeInt     FeatureType = "int"
)

const (
	// Keys share one flat namespace with CSGHub's own features inside the
	// issuer's registry, so every CSGLite key carries a product segment.
	featurePrefix = "feature.lite."
	quotaPrefix   = "quota.lite."
)

// FeatureDefinition is one entry of the catalog: the single source of truth
// for which capabilities are license-gated. The Key must be registered
// verbatim in starhub-server's common/types/feature_registry.go, otherwise the
// issuer refuses to sign it into License.Extra.
type FeatureDefinition struct {
	// Key is "feature.lite.<name>" for booleans or "quota.lite.<name>" for
	// integer limits.
	Key  string
	Type FeatureType
	// Gated marks a feature as enterprise-only. Only gated features consult
	// the license: without a valid license a gated boolean is false and a
	// gated limit is 0. Everything else is always enabled, so development and
	// the Community edition are never blocked by a feature nobody has decided
	// to sell yet. Flipping Gated to true is the act of moving a feature to EE.
	Gated bool
	// DefaultValue applies to a gated feature when a valid Enterprise license
	// does not mention the key in Extra.
	DefaultValue any
	// CommunityValue is what an unlicensed instance gets for a gated integer
	// limit. Booleans are simply off when unlicensed, so this only applies to
	// FeatureTypeInt, and a gated limit must set it to a non-zero value:
	// 0 means unlimited, which would leave Community users less restricted
	// than paying ones. ValidateCatalog rejects a gated limit that leaves it 0.
	CommunityValue int
	// NavItem is the web UI navigation id the feature unlocks, or empty.
	NavItem string
	// Since is the CSGLite version in which the feature became license-gated.
	Since string
}

// Catalog entries. Only QuotaMaxClusterNodes is gated; every boolean is
// enabled regardless of license. The ungated entries are declared so that the
// issuer registry and this binary agree on the names before a feature moves
// to EE.
var (
	FeatureProviderPools = FeatureDefinition{
		Key: featurePrefix + "provider_pools", Type: FeatureTypeBoolean, Gated: false, DefaultValue: true,
		NavItem: "ai-gateway", Since: "0.10.0",
	}
	FeatureObservability = FeatureDefinition{
		Key: featurePrefix + "observability", Type: FeatureTypeBoolean, Gated: false, DefaultValue: true,
		NavItem: "observability", Since: "0.10.0",
	}
	FeatureRemoteAPIKeys = FeatureDefinition{
		Key: featurePrefix + "remote_api_keys", Type: FeatureTypeBoolean, Gated: false, DefaultValue: true,
		Since: "0.10.0",
	}
	FeatureAIApps = FeatureDefinition{
		Key: featurePrefix + "ai_apps", Type: FeatureTypeBoolean, Gated: false, DefaultValue: true,
		NavItem: "ai-apps", Since: "0.10.0",
	}
	FeatureRealtimeVoice = FeatureDefinition{
		Key: featurePrefix + "realtime_voice", Type: FeatureTypeBoolean, Gated: false, DefaultValue: true,
		Since: "0.10.0",
	}
	FeatureImageGeneration = FeatureDefinition{
		Key: featurePrefix + "image_generation", Type: FeatureTypeBoolean, Gated: false, DefaultValue: true,
		NavItem: "images", Since: "0.10.0",
	}
	// QuotaMaxProviderPools caps configured provider pools; 0 means unlimited.
	QuotaMaxProviderPools = FeatureDefinition{
		Key: quotaPrefix + "max_provider_pools", Type: FeatureTypeInt, Gated: false, DefaultValue: 0,
		Since: "0.10.0",
	}
	// FeatureLANCluster is the LAN compute cluster: discovery, pairing and
	// request routing across CSGLite nodes. The feature itself is open to
	// every edition; what the license controls is how many nodes may join
	// (QuotaMaxClusterNodes), so there is one implementation and no CE/EE
	// code fork. See docs/guides/lan-cluster-design.md.
	FeatureLANCluster = FeatureDefinition{
		Key: featurePrefix + "lan_cluster", Type: FeatureTypeBoolean, Gated: false, DefaultValue: true,
		NavItem: "cluster", Since: "0.13.0",
	}
	// FeatureClusterModelSpan is running one model across several machines
	// because it fits on none of them. Unlike the cluster itself it is gated:
	// it exists for models a single box cannot hold, which is not a situation
	// two community nodes are in, and it trades away both speed and
	// redundancy for capacity.
	FeatureClusterModelSpan = FeatureDefinition{
		Key: featurePrefix + "cluster_model_span", Type: FeatureTypeBoolean, Gated: true, DefaultValue: true,
		Since: "0.13.0",
	}
	// QuotaMaxClusterNodes caps the members of a LAN cluster, this node
	// included. The Community edition may run two machines; an Enterprise
	// license lifts the cap (0 = unlimited) or sets a tier in Extra.limits.
	QuotaMaxClusterNodes = FeatureDefinition{
		Key: quotaPrefix + "max_cluster_nodes", Type: FeatureTypeInt, Gated: true, DefaultValue: 0,
		CommunityValue: 2, Since: "0.13.0",
	}
)

var catalog = []FeatureDefinition{
	FeatureProviderPools,
	FeatureObservability,
	FeatureRemoteAPIKeys,
	FeatureAIApps,
	FeatureRealtimeVoice,
	FeatureImageGeneration,
	QuotaMaxProviderPools,
	FeatureLANCluster,
	FeatureClusterModelSpan,
	QuotaMaxClusterNodes,
}

// Catalog returns a copy of every registered feature definition.
func Catalog() []FeatureDefinition {
	return append([]FeatureDefinition(nil), catalog...)
}

// GatedCatalog returns only the entries that require a license.
func GatedCatalog() []FeatureDefinition {
	var out []FeatureDefinition
	for _, def := range catalog {
		if def.Gated {
			out = append(out, def)
		}
	}
	return out
}

// Lookup returns the catalog entry for key.
func Lookup(key string) (FeatureDefinition, bool) {
	for _, def := range catalog {
		if def.Key == key {
			return def, true
		}
	}
	return FeatureDefinition{}, false
}

// ValidateCatalog enforces the naming and typing rules the issuer registry
// applies, so a definition that would be rejected upstream fails here first.
func ValidateCatalog(defs []FeatureDefinition) error {
	seen := make(map[string]struct{}, len(defs))
	for _, def := range defs {
		if def.Key == "" {
			return fmt.Errorf("feature key is empty")
		}
		if _, dup := seen[def.Key]; dup {
			return fmt.Errorf("duplicate feature key %q", def.Key)
		}
		seen[def.Key] = struct{}{}
		switch def.Type {
		case FeatureTypeBoolean:
			if !strings.HasPrefix(def.Key, featurePrefix) {
				return fmt.Errorf("boolean feature %q must use the %s* namespace", def.Key, featurePrefix)
			}
			if _, ok := def.DefaultValue.(bool); !ok {
				return fmt.Errorf("boolean feature %q must have a bool default", def.Key)
			}
		case FeatureTypeInt:
			if !strings.HasPrefix(def.Key, quotaPrefix) {
				return fmt.Errorf("integer limit %q must use the %s* namespace", def.Key, quotaPrefix)
			}
			if _, ok := def.DefaultValue.(int); !ok {
				return fmt.Errorf("integer limit %q must have an int default", def.Key)
			}
			if def.Gated && def.CommunityValue == 0 {
				return fmt.Errorf("gated limit %q must set CommunityValue to a non-zero cap; 0 means unlimited and would leave Community users less restricted than licensed ones", def.Key)
			}
		default:
			return fmt.Errorf("feature %q has unsupported type %q", def.Key, def.Type)
		}
		if !def.Gated && def.CommunityValue != 0 {
			return fmt.Errorf("feature %q is not gated, so CommunityValue is never used; leave it 0", def.Key)
		}
		if strings.TrimSpace(def.Since) == "" {
			return fmt.Errorf("feature %q must record the version it was gated in", def.Key)
		}
	}
	return nil
}
