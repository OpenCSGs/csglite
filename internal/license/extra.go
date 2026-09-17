package license

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Extra is the parsed form of DataBody.Extra: {"features": {...}, "limits": {...}}.
type Extra struct {
	Features map[string]bool
	Limits   map[string]int
}

// ParseExtra follows starhub-server's ValidateLicenseExtraForImport: unknown
// top-level fields and unknown keys are reported as warnings and ignored so a
// newer issuer can still serve an older CSGLite, while a known key of the
// wrong type is an error.
func ParseExtra(extra string, defs []FeatureDefinition) (Extra, []string, error) {
	out := Extra{Features: map[string]bool{}, Limits: map[string]int{}}
	if extra == "" {
		return out, nil, nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(extra), &raw); err != nil {
		return out, nil, fmt.Errorf("invalid license extra JSON: %w", err)
	}

	known := make(map[string]FeatureType, len(defs))
	for _, def := range defs {
		known[def.Key] = def.Type
	}

	var warnings []string
	for field := range raw {
		if field != "features" && field != "limits" {
			warnings = append(warnings, fmt.Sprintf("unknown top-level field %q in license extra", field))
		}
	}

	var sections struct {
		Features map[string]json.RawMessage `json:"features"`
		Limits   map[string]json.RawMessage `json:"limits"`
	}
	if err := json.Unmarshal([]byte(extra), &sections); err != nil {
		return out, nil, fmt.Errorf("invalid license extra JSON: %w", err)
	}

	for key, value := range sections.Features {
		switch known[key] {
		case FeatureTypeBoolean:
			var parsed bool
			if err := json.Unmarshal(value, &parsed); err != nil {
				return out, nil, fmt.Errorf("feature flag %q in license extra is not a boolean: %w", key, err)
			}
			out.Features[key] = parsed
		case FeatureTypeInt:
			return out, nil, fmt.Errorf("feature flag %q in license extra is an integer limit, not a boolean", key)
		default:
			warnings = append(warnings, fmt.Sprintf("unknown feature flag %q in license extra", key))
		}
	}

	for key, value := range sections.Limits {
		switch known[key] {
		case FeatureTypeInt:
			var parsed int
			if err := json.Unmarshal(value, &parsed); err != nil {
				return out, nil, fmt.Errorf("limit %q in license extra is not an integer: %w", key, err)
			}
			out.Limits[key] = parsed
		case FeatureTypeBoolean:
			return out, nil, fmt.Errorf("limit %q in license extra is a boolean feature, not an integer", key)
		default:
			warnings = append(warnings, fmt.Sprintf("unknown limit %q in license extra", key))
		}
	}

	sort.Strings(warnings)
	return out, warnings, nil
}
