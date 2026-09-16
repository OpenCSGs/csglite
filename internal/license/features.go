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
	// DefaultValue applies when a valid Enterprise license does not mention
	// the key in Extra. Without a valid license every boolean is false and
	// every limit is 0.
	DefaultValue any
	// NavItem is the web UI navigation id the feature unlocks, or empty.
	NavItem string
	// Since is the CSGLite version in which the feature became license-gated.
	Since string
}

// Catalog entries. Nothing in the repository gates on these yet; they are
// declared so that the issuer registry and this binary agree on the names
// before the first route is gated.
var (
	FeatureProviderPools = FeatureDefinition{
		Key: featurePrefix + "provider_pools", Type: FeatureTypeBoolean, DefaultValue: true,
		NavItem: "ai-gateway", Since: "0.10.0",
	}
	FeatureObservability = FeatureDefinition{
		Key: featurePrefix + "observability", Type: FeatureTypeBoolean, DefaultValue: true,
		NavItem: "observability", Since: "0.10.0",
	}
	FeatureRemoteAPIKeys = FeatureDefinition{
		Key: featurePrefix + "remote_api_keys", Type: FeatureTypeBoolean, DefaultValue: true,
		Since: "0.10.0",
	}
	FeatureAIApps = FeatureDefinition{
		Key: featurePrefix + "ai_apps", Type: FeatureTypeBoolean, DefaultValue: true,
		NavItem: "ai-apps", Since: "0.10.0",
	}
	FeatureRealtimeVoice = FeatureDefinition{
		Key: featurePrefix + "realtime_voice", Type: FeatureTypeBoolean, DefaultValue: true,
		Since: "0.10.0",
	}
	FeatureImageGeneration = FeatureDefinition{
		Key: featurePrefix + "image_generation", Type: FeatureTypeBoolean, DefaultValue: true,
		NavItem: "images", Since: "0.10.0",
	}
	// QuotaMaxProviderPools caps configured provider pools; 0 means unlimited.
	QuotaMaxProviderPools = FeatureDefinition{
		Key: quotaPrefix + "max_provider_pools", Type: FeatureTypeInt, DefaultValue: 0,
		Since: "0.10.0",
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
}

// Catalog returns a copy of every registered feature definition.
func Catalog() []FeatureDefinition {
	return append([]FeatureDefinition(nil), catalog...)
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
		default:
			return fmt.Errorf("feature %q has unsupported type %q", def.Key, def.Type)
		}
		if strings.TrimSpace(def.Since) == "" {
			return fmt.Errorf("feature %q must record the version it was gated in", def.Key)
		}
	}
	return nil
}
