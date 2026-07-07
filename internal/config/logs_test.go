package config

import (
	"path/filepath"
	"testing"
)

func TestLogPaths(t *testing.T) {
	home, err := AppHome()
	if err != nil {
		t.Fatalf("AppHome() error: %v", err)
	}

	logDir, err := LogDir()
	if err != nil {
		t.Fatalf("LogDir() error: %v", err)
	}
	if want := filepath.Join(home, LogsDir); logDir != want {
		t.Fatalf("LogDir() = %q, want %q", logDir, want)
	}

	serverLogPath, err := ServerLogPath()
	if err != nil {
		t.Fatalf("ServerLogPath() error: %v", err)
	}
	if want := filepath.Join(logDir, ServerLogFileName); serverLogPath != want {
		t.Fatalf("ServerLogPath() = %q, want %q", serverLogPath, want)
	}

	llamaLogPath, err := LlamaServerLogPath()
	if err != nil {
		t.Fatalf("LlamaServerLogPath() error: %v", err)
	}
	if want := filepath.Join(logDir, LlamaServerLogFileName); llamaLogPath != want {
		t.Fatalf("LlamaServerLogPath() = %q, want %q", llamaLogPath, want)
	}

	embeddingLogPath, err := EmbeddingWorkerLogPath()
	if err != nil {
		t.Fatalf("EmbeddingWorkerLogPath() error: %v", err)
	}
	if want := filepath.Join(logDir, EmbeddingWorkerLogFileName); embeddingLogPath != want {
		t.Fatalf("EmbeddingWorkerLogPath() = %q, want %q", embeddingLogPath, want)
	}

	diffusersLogPath, err := DiffusersWorkerLogPath()
	if err != nil {
		t.Fatalf("DiffusersWorkerLogPath() error: %v", err)
	}
	if want := filepath.Join(logDir, DiffusersWorkerLogFileName); diffusersLogPath != want {
		t.Fatalf("DiffusersWorkerLogPath() = %q, want %q", diffusersLogPath, want)
	}
}
