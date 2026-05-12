package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
)

func TestRestartBackgroundServiceRestartsAndWaits(t *testing.T) {
	oldStop := stopBackgroundServiceForRestart
	oldStart := startBackgroundServerForRestart
	oldWait := waitForServerForRestart
	defer func() {
		stopBackgroundServiceForRestart = oldStop
		startBackgroundServerForRestart = oldStart
		waitForServerForRestart = oldWait
	}()

	cfg := &config.Config{ListenAddr: ":14567"}
	var calls []string

	stopBackgroundServiceForRestart = func() error {
		calls = append(calls, "stop")
		return nil
	}
	startBackgroundServerForRestart = func(got *config.Config) error {
		if got != cfg {
			t.Fatalf("start config = %#v, want original config", got)
		}
		calls = append(calls, "start")
		return nil
	}
	waitForServerForRestart = func(baseURL string, timeout time.Duration) error {
		if baseURL != "http://127.0.0.1:14567" {
			t.Fatalf("baseURL = %q, want http://127.0.0.1:14567", baseURL)
		}
		if timeout != 15*time.Second {
			t.Fatalf("timeout = %s, want 15s", timeout)
		}
		calls = append(calls, "wait")
		return nil
	}

	if err := restartBackgroundService(cfg); err != nil {
		t.Fatalf("restartBackgroundService returned error: %v", err)
	}

	if want := []string{"stop", "start", "wait"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestRestartBackgroundServiceWrapsStopError(t *testing.T) {
	oldStop := stopBackgroundServiceForRestart
	oldStart := startBackgroundServerForRestart
	oldWait := waitForServerForRestart
	defer func() {
		stopBackgroundServiceForRestart = oldStop
		startBackgroundServerForRestart = oldStart
		waitForServerForRestart = oldWait
	}()

	stopBackgroundServiceForRestart = func() error {
		return errors.New("boom")
	}
	startBackgroundServerForRestart = func(*config.Config) error {
		t.Fatal("start should not be called after stop failure")
		return nil
	}
	waitForServerForRestart = func(string, time.Duration) error {
		t.Fatal("wait should not be called after stop failure")
		return nil
	}

	err := restartBackgroundService(&config.Config{ListenAddr: config.DefaultListenAddr})
	if err == nil || !strings.Contains(err.Error(), "stopping existing service: boom") {
		t.Fatalf("error = %v, want wrapped stop error", err)
	}
}
