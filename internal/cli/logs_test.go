package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
)

func TestLogsSelectsLastMatchingHistoricalLines(t *testing.T) {
	setupCLIConfigHome(t)
	path, err := config.ServerLogPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	const model = "Qwen/Qwen-Image-2.1"
	first := "MODEL " + model + ": requested\n"
	last := "IMAGE JOB 1: failed model=\"" + model + "\"\n"
	similar := "MODEL " + model + "-Turbo: ignored\nMODEL huggingface/" + model + ": ignored\n"
	history := "MODEL " + model + ": old\n" + first + strings.Repeat("other model\n", 16000) + last + similar
	if err := os.WriteFile(path, []byte(history), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := runModelCLI([]string{"logs", model, "--tail", "2"}, "")
	if err != nil || got != first+last {
		t.Fatalf("got %q, err %v", got, err)
	}
	got, err = runModelCLI([]string{"logs", "-n", "0"}, "")
	if err != nil || got != "" {
		t.Fatalf("zero tail: %q %v", got, err)
	}
	got, err = runModelCLI([]string{"logs", "-n", "2"}, "")
	if err != nil || got != similar {
		t.Fatalf("unfiltered: %q %v", got, err)
	}
	if _, err := runModelCLI([]string{"logs", "-n", "-1"}, ""); err == nil {
		t.Fatal("accepted negative tail")
	}
}

type logLines chan string

func (lines logLines) Write(b []byte) (int, error) {
	lines <- string(b)
	return len(b), nil
}

func TestFollowLogsCompletesPartialLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := io.WriteString(file, "MODEL A: ready\nMODEL A: par"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lines := make(logLines, 8)
	done := make(chan error, 1)
	go func() { done <- readServerLog(ctx, lines, path, modelLogFilter([]string{"A"}), 1, true) }()
	expect := func(want string) {
		t.Helper()
		select {
		case got := <-lines:
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		case err := <-done:
			t.Fatalf("exited early: %v", err)
		case <-ctx.Done():
			t.Fatal("timed out waiting for log output")
		}
	}
	expect("MODEL A: ready\n")
	if _, err := io.WriteString(file, "tial\nMODEL AB: ignore\nMODEL A: done\n"); err != nil {
		t.Fatal(err)
	}
	expect("MODEL A: partial\n")
	expect("MODEL A: done\n")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("exit error: %v", err)
	}
}
