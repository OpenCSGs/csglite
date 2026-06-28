package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/logutil"
	"github.com/opencsgs/csglite/pkg/api"
)

// ensureServer makes sure a csghub-lite API server is running and returns
// its base URL. If no server is reachable, it spawns one in the background.
func ensureServer(cfg *config.Config) (string, error) {
	baseURL := serverBaseURL(cfg)

	if serverHealthy(baseURL) {
		if strings.TrimSpace(cfg.Token) != "" {
			warnIfTokenSyncFailed(cfg)
		}
		return baseURL, nil
	}

	if err := startBackgroundServer(cfg); err != nil {
		return "", fmt.Errorf("starting background server: %w", err)
	}

	if err := waitForServer(baseURL, 15*time.Second); err != nil {
		return "", err
	}
	if strings.TrimSpace(cfg.Token) != "" {
		warnIfTokenSyncFailed(cfg)
	}

	return baseURL, nil
}

func serverBaseURL(cfg *config.Config) string {
	addr := cfg.ListenAddr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	return "http://" + addr
}

func serverHealthy(baseURL string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(baseURL + "/api/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func startBackgroundServer(cfg *config.Config) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}

	args := []string{"serve"}
	if cfg.ListenAddr != config.DefaultListenAddr {
		args = append(args, "--listen", cfg.ListenAddr)
	}

	cmd := exec.Command(self, args...)
	cmd.Stdin = nil

	if config.FileLoggingEnabled() {
		if path, err := config.ServerLogPath(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not resolve csghub-lite log path: %v\n", err)
		} else if file, err := logutil.OpenAppendFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not open csghub-lite log file %s: %v\n", path, err)
		} else {
			cmd.Stdout = file
			cmd.Stderr = file
			cmd.Env = append(os.Environ(), config.LogStderrEnv+"=0")
			defer file.Close()
		}
	}

	detachProcess(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exec serve: %w", err)
	}

	if err := writePIDFile(cmd.Process.Pid); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write PID file: %v\n", err)
	}

	return nil
}

func waitForServer(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if serverHealthy(baseURL) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for csghub-lite server at %s", baseURL)
}

func pidFilePath() (string, error) {
	home, err := config.AppHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "server.pid"), nil
}

func writePIDFile(pid int) error {
	path, err := pidFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o644)
}

func removePIDFile() error {
	path, err := pidFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ServerPID reads the stored server PID, or returns 0 if unavailable.
func ServerPID() int {
	path, err := pidFilePath()
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

// preloadModel sends a request to the server to eagerly load (and convert if
// necessary) the model, so it is ready before the first chat request.
// It uses SSE streaming to display conversion progress.
func preloadModel(serverURL, modelID string, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype, keepAlive string) error {
	stream := true
	req := api.LoadRequest{
		Model:       modelID,
		Stream:      &stream,
		KeepAlive:   keepAlive,
		NumCtx:      numCtx,
		NumParallel: numParallel,
		CacheTypeK:  cacheTypeK,
		CacheTypeV:  cacheTypeV,
		DType:       dtype,
	}
	if nGPULayers >= 0 {
		req.NGPULayers = &nGPULayers
	}
	body, _ := json.Marshal(req)
	client := &http.Client{Timeout: 0}
	resp, err := client.Post(serverURL+"/api/load", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("load request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("load failed: %s", string(errBody))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var lastStep string

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		var lr api.LoadResponse
		if err := json.Unmarshal([]byte(line[6:]), &lr); err != nil {
			continue
		}

		if strings.HasPrefix(lr.Status, "error") {
			fmt.Fprintf(os.Stderr, "\n")
			return fmt.Errorf("%s", lr.Status)
		}

		if lr.Status == "ready" {
			if lastStep != "" {
				fmt.Fprintf(os.Stderr, "\n")
			}
			return nil
		}

		if lr.Step != "" {
			if lr.Total > 0 {
				pct := lr.Current * 100 / lr.Total
				fmt.Fprintf(os.Stderr, "\r\033[K  %s (%d/%d) %d%%", lr.Step, lr.Current, lr.Total, pct)
			} else if lr.Current > 0 {
				// Heartbeat (e.g. seconds waiting for llama-server while loading a large GGUF).
				fmt.Fprintf(os.Stderr, "\r\033[K  %s (%ds)", lr.Step, lr.Current)
			} else if lr.Step != lastStep {
				if lastStep != "" {
					fmt.Fprintf(os.Stderr, "\n")
				}
				fmt.Fprintf(os.Stderr, "  %s...", lr.Step)
			}
			lastStep = lr.Step
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading load progress: %w", err)
	}
	return nil
}

// detachProcess is defined per-platform in client_unix.go / client_windows.go.
