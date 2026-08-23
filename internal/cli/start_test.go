package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
)

func TestStartCmdAliases(t *testing.T) {
	cmd := NewRootCmd("test")
	for _, name := range []string{"start", "start-service", "start-server", "up"} {
		found, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find(%s) error: %v", name, err)
		}
		if found.Name() != "start" {
			t.Fatalf("Find(%s) = %q, want start", name, found.Name())
		}
	}
}

func TestStartBackgroundServiceStartsAndWaits(t *testing.T) {
	oldHealthy := serverHealthyForStart
	oldStart := startBackgroundServerForStart
	oldWait := waitForServerForStart
	defer func() {
		serverHealthyForStart = oldHealthy
		startBackgroundServerForStart = oldStart
		waitForServerForStart = oldWait
	}()

	cfg := &config.Config{ListenAddr: ":14567"}
	var calls []string
	serverHealthyForStart = func(baseURL string) bool {
		if baseURL != "http://127.0.0.1:14567" {
			t.Fatalf("healthy baseURL = %q", baseURL)
		}
		calls = append(calls, "healthy")
		return false
	}
	startBackgroundServerForStart = func(got *config.Config) error {
		if got != cfg {
			t.Fatalf("start config = %#v, want original config", got)
		}
		calls = append(calls, "start")
		return nil
	}
	waitForServerForStart = func(baseURL string, timeout time.Duration) error {
		if baseURL != "http://127.0.0.1:14567" {
			t.Fatalf("baseURL = %q, want http://127.0.0.1:14567", baseURL)
		}
		if timeout != 15*time.Second {
			t.Fatalf("timeout = %s, want 15s", timeout)
		}
		calls = append(calls, "wait")
		return nil
	}

	if err := startBackgroundService(cfg); err != nil {
		t.Fatalf("startBackgroundService returned error: %v", err)
	}
	if want := []string{"healthy", "start", "wait"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestStartBackgroundServiceSkipsWhenAlreadyRunning(t *testing.T) {
	oldHealthy := serverHealthyForStart
	oldStart := startBackgroundServerForStart
	oldWait := waitForServerForStart
	defer func() {
		serverHealthyForStart = oldHealthy
		startBackgroundServerForStart = oldStart
		waitForServerForStart = oldWait
	}()

	serverHealthyForStart = func(string) bool { return true }
	startBackgroundServerForStart = func(*config.Config) error {
		t.Fatal("start should not be called when the service is already running")
		return nil
	}
	waitForServerForStart = func(string, time.Duration) error {
		t.Fatal("wait should not be called when the service is already running")
		return nil
	}

	if err := startBackgroundService(&config.Config{ListenAddr: config.DefaultListenAddr}); err != nil {
		t.Fatalf("startBackgroundService returned error: %v", err)
	}
}

func TestStartBackgroundServiceWrapsStartError(t *testing.T) {
	oldHealthy := serverHealthyForStart
	oldStart := startBackgroundServerForStart
	oldWait := waitForServerForStart
	defer func() {
		serverHealthyForStart = oldHealthy
		startBackgroundServerForStart = oldStart
		waitForServerForStart = oldWait
	}()

	serverHealthyForStart = func(string) bool { return false }
	startBackgroundServerForStart = func(*config.Config) error {
		return errors.New("boom")
	}
	waitForServerForStart = func(string, time.Duration) error {
		t.Fatal("wait should not be called after start failure")
		return nil
	}

	err := startBackgroundService(&config.Config{ListenAddr: config.DefaultListenAddr})
	if err == nil || !strings.Contains(err.Error(), "starting service: boom") {
		t.Fatalf("error = %v, want wrapped start error", err)
	}
}
