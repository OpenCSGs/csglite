package config

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	LogsDir                    = "logs"
	ServerLogFileName          = "csghub-lite.log"
	LlamaServerLogFileName     = "llama-server.log"
	EmbeddingWorkerLogFileName = "embedding-worker.log"
	DiffusersWorkerLogFileName = "diffusers-worker.log"

	LogStderrEnv          = "CSGHUB_LITE_LOG_STDERR"
	DisableFileLoggingEnv = "CSGHUB_LITE_DISABLE_FILE_LOGGING"
)

func LogDir() (string, error) {
	home, err := AppHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, LogsDir), nil
}

func ServerLogPath() (string, error) {
	return logPath(ServerLogFileName)
}

func LlamaServerLogPath() (string, error) {
	return logPath(LlamaServerLogFileName)
}

func EmbeddingWorkerLogPath() (string, error) {
	return logPath(EmbeddingWorkerLogFileName)
}

func DiffusersWorkerLogPath() (string, error) {
	return logPath(DiffusersWorkerLogFileName)
}

func LogStderrEnabled() bool {
	return strings.TrimSpace(os.Getenv(LogStderrEnv)) != "0"
}

func FileLoggingEnabled() bool {
	return strings.TrimSpace(os.Getenv(DisableFileLoggingEnv)) == ""
}

func logPath(name string) (string, error) {
	dir, err := LogDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
