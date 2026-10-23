package convert

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsDecisionCheckpoint(t *testing.T) {
	write := func(dir, rel string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	laya := t.TempDir()
	write(laya, "config.json")
	write(laya, "encoder/config.json")
	write(laya, "rl_agent_config.json")
	julia := t.TempDir()
	write(julia, "encoder/config.json")
	write(julia, "julia_config.json")
	encoderOnly := t.TempDir()
	write(encoderOnly, "encoder/config.json")
	headOnly := t.TempDir()
	write(headOnly, "rl_agent_config.json")

	for name, want := range map[string]bool{laya: true, julia: true, encoderOnly: false, headOnly: false} {
		if got := IsDecisionCheckpoint(name); got != want {
			t.Errorf("IsDecisionCheckpoint(%s) = %t, want %t", name, got, want)
		}
	}
}
