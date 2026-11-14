package config

import (
	"encoding/json"
	"testing"
)

func TestCloudProviderEnabledCompatibility(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{}`, true},
		{`{"cloud_provider_enabled":null}`, true},
		{`{"cloud_provider_enabled":true}`, true},
		{`{"cloud_provider_enabled":false}`, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var cfg Config
			if err := json.Unmarshal([]byte(tc.body), &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.IsCloudProviderEnabled() != tc.want {
				t.Fatal("unexpected enabled state")
			}
			data, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var restored Config
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if restored.IsCloudProviderEnabled() != tc.want {
				t.Fatal("enabled state changed after round trip")
			}
		})
	}
}
