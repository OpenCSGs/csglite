package config

import (
	"fmt"
	"os"
	"strings"
)

const EnvConsoleAuthMode = "CSGHUB_LITE_CONSOLE_AUTH_MODE"

const (
	ConsoleAuthAuto     = "auto"
	ConsoleAuthRequired = "required"
)

// ConsoleConfig controls management access, independently of inference API keys.
type ConsoleConfig struct {
	AuthMode string `json:"auth_mode,omitempty"`
}

func ParseConsoleAuthMode(value string) (string, error) {
	switch value = strings.TrimSpace(value); value {
	case "", ConsoleAuthAuto:
		return ConsoleAuthAuto, nil
	case ConsoleAuthRequired:
		return ConsoleAuthRequired, nil
	default:
		return "", fmt.Errorf("invalid console auth mode %q: expected auto or required", value)
	}
}

// EffectiveConsoleAuthMode applies a process override without persisting it.
// Desktop sessions use their own automatic authentication in either mode.
func (c *Config) EffectiveConsoleAuthMode() (string, error) {
	if value := strings.TrimSpace(os.Getenv(EnvConsoleAuthMode)); value != "" {
		return ParseConsoleAuthMode(value)
	}
	if c == nil {
		return ConsoleAuthAuto, nil
	}
	return ParseConsoleAuthMode(c.Console.AuthMode)
}
