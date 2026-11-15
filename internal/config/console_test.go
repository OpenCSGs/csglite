package config

import (
	"encoding/json"
	"testing"
)

func TestConsoleAuthConfigCompatibilityAndOverride(t *testing.T) {
	for _, tc := range []struct{ persisted, env, want string }{
		{`{}`, "", ConsoleAuthAuto},
		{`{"console":{"auth_mode":"required"}}`, "", ConsoleAuthRequired},
		{`{"console":{"auth_mode":"required"}}`, "auto", ConsoleAuthAuto},
		{`{}`, "required", ConsoleAuthRequired},
		{`{}`, "false", ""},
		{`{"console":{"auth_mode":"off"}}`, "", ""},
	} {
		t.Run(tc.persisted+tc.env, func(t *testing.T) {
			t.Setenv(EnvConsoleAuthMode, tc.env)
			var cfg Config
			if err := json.Unmarshal([]byte(tc.persisted), &cfg); err != nil {
				t.Fatal(err)
			}
			original := cfg.Console.AuthMode
			mode, err := cfg.EffectiveConsoleAuthMode()
			if mode != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("mode=%q err=%v", mode, err)
			}
			data, err := json.Marshal(&cfg)
			if err != nil {
				t.Fatal(err)
			}
			var restored Config
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if restored.Console.AuthMode != original {
				t.Fatal("environment override was persisted")
			}
		})
	}
}
