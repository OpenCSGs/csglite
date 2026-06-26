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
	"runtime"
	"strings"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/pkg/api"
	"github.com/spf13/cobra"
)

const aiAppInstallWaitTimeout = 25 * time.Minute

type launchTarget struct {
	AppID       string
	DisplayName string
	Binaries    []string
}

type launchOptions struct {
	SkipConfirm bool
	Model       string
	Gateway     string
}

const launchSupportedApps = "claude-code, open-code, open-code-review/ocr, codex, pi, openclaw, csgclaw, dify, anythingllm"
const claudeDangerouslySkipPermissionsFlag = "dangerously-skip-permissions"

func newLaunchCmd() *cobra.Command {
	var opts launchOptions
	var claudeDangerouslySkipPermissions bool

	cmd := &cobra.Command{
		Use:     "launch APP [-- APP_ARGS...]",
		Aliases: []string{"lanuch"},
		Short:   "Launch an AI app CLI, installing it first if needed",
		Long: `Launch an AI app command-line tool managed by CSGHub Lite.

If the selected app is not installed yet, the CLI will prompt to install it
first using the same AI Apps installer backend as the web UI.

Supported apps: ` + launchSupportedApps + `

Use ` + "`--`" + ` to pass through arguments to the launched app binary.`,
		Example: `  csghub-lite launch claude-code
  csghub-lite launch codex --model Qwen/Qwen2.5-Coder-7B
  csghub-lite launch ocr --model glm-5.1-1
  csghub-lite launch open-code-review -- review --format json
  csghub-lite launch pi
  csghub-lite launch csgclaw
  csghub-lite launch open-code -- --help
  csghub-lite launch anythingllm
  csghub-lite launch claude-code --gateway http://192.168.1.18:11435
  csghub-lite launch pi --gateway 192.168.1.18:11435 --model Qwen/Qwen3-8B`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("APP is required\n\nRun 'csghub-lite launch --help' for supported apps and examples.")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLaunch(cmd, args, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.SkipConfirm, "yes", "y", false, "Install without confirmation if the app is missing")
	cmd.Flags().StringVar(&opts.Model, "model", "", "Use a specific local model when launching the app")
	cmd.Flags().StringVar(&opts.Gateway, "gateway", "", "Use a remote csghub-lite gateway URL (e.g. http://192.168.1.18:11435)")
	cmd.Flags().BoolVar(&claudeDangerouslySkipPermissions, claudeDangerouslySkipPermissionsFlag, false, "Pass --dangerously-skip-permissions to Claude Code")
	return cmd
}

func runLaunch(cmd *cobra.Command, args []string, opts launchOptions) error {
	target, err := resolveLaunchTarget(args[0])
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	var serverURL string
	if opts.Gateway != "" {
		// Use remote gateway if specified
		serverURL = strings.TrimRight(strings.TrimSpace(opts.Gateway), "/")
		if !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
			serverURL = "http://" + serverURL
		}
		fmt.Fprintf(os.Stderr, "Using remote gateway: %s\n", serverURL)

		// Verify the gateway is reachable
		if _, err := getAIApps(serverURL); err != nil {
			return fmt.Errorf("connecting to gateway %s: %w", serverURL, err)
		}
	} else {
		// Use local server
		serverURL, err = ensureAIAppsServer(cfg)
		if err != nil {
			return fmt.Errorf("starting server: %w", err)
		}
	}

	app, err := getAIAppInfo(serverURL, target.AppID)
	if err != nil {
		return err
	}

	if app.Disabled || !app.Supported {
		return fmt.Errorf("%s is currently disabled in AI Apps", target.DisplayName)
	}

	if !app.Installed {
		ok, err := confirmAIAppInstall(target.DisplayName, opts.SkipConfirm)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Aborted.")
			return nil
		}

		if _, err := requestAIAppInstall(serverURL, target.AppID); err != nil {
			return err
		}
		app, err = waitForAIAppInstall(serverURL, target)
		if err != nil {
			return err
		}
	}

	modelID, err := resolveLaunchModel(serverURL, app.ModelID, opts.Model, opts.SkipConfirm, strings.TrimSpace(cfg.Token) != "")
	if err != nil {
		return err
	}

	passClaudeSkipPermissions, err := userRequestedClaudeSkipPermissions(cmd)
	if err != nil {
		return err
	}
	userArgs, err := launchUserArgs(target, args[1:], passClaudeSkipPermissions)
	if err != nil {
		return err
	}

	prepared, err := prepareLaunchExecution(target, serverURL, modelID, userArgs)
	if err != nil {
		return err
	}

	fmt.Printf("Using model %s\n", modelID)
	fmt.Printf("Launching %s...\n", target.DisplayName)
	return launchProcess(prepared.Binary, prepared.Args, prepared.Env)
}

func userRequestedClaudeSkipPermissions(cmd *cobra.Command) (bool, error) {
	if cmd == nil {
		return false, nil
	}
	flag := cmd.Flags().Lookup(claudeDangerouslySkipPermissionsFlag)
	if flag == nil || !flag.Changed {
		return false, nil
	}
	value, err := cmd.Flags().GetBool(claudeDangerouslySkipPermissionsFlag)
	if err != nil {
		return false, err
	}
	return value, nil
}

func launchUserArgs(target launchTarget, args []string, passClaudeSkipPermissions bool) ([]string, error) {
	userArgs := append([]string{}, args...)
	if !passClaudeSkipPermissions {
		return userArgs, nil
	}
	if target.AppID != "claude-code" {
		return nil, fmt.Errorf("--dangerously-skip-permissions is only supported for Claude Code")
	}
	return appendArgsIfMissing(userArgs, "--dangerously-skip-permissions"), nil
}

func ensureAIAppsServer(cfg *config.Config) (string, error) {
	serverURL, err := ensureServer(cfg)
	if err != nil {
		return "", err
	}

	if _, err := getAIApps(serverURL); err == nil {
		return serverURL, nil
	}

	if serverHealthy(serverURL) {
		fmt.Fprintln(os.Stderr, "Restarting csghub-lite service to enable AI Apps CLI support...")
		if err := requestServerShutdown(serverURL); err != nil {
			return "", fmt.Errorf("restarting stale service: %w", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if !serverHealthy(serverURL) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	if err := startBackgroundServer(cfg); err != nil {
		return "", fmt.Errorf("restarting service with AI Apps support: %w", err)
	}
	if err := waitForServer(serverURL, 15*time.Second); err != nil {
		return "", err
	}
	if _, err := getAIApps(serverURL); err != nil {
		return "", fmt.Errorf("AI Apps API is unavailable after restarting the service: %w", err)
	}

	return serverURL, nil
}

func resolveLaunchTarget(name string) (launchTarget, error) {
	switch normalizeLaunchAppName(name) {
	case "claude", "claudecode":
		return launchTarget{
			AppID:       "claude-code",
			DisplayName: "Claude Code",
			Binaries:    []string{"claude"},
		}, nil
	case "opencode", "opcode", "opencodeai":
		return launchTarget{
			AppID:       "open-code",
			DisplayName: "OpenCode",
			Binaries:    []string{"opencode"},
		}, nil
	case "opencodereview", "openreview", "ocr":
		return launchTarget{
			AppID:       "open-code-review",
			DisplayName: "Open Code Review",
			Binaries:    []string{"ocr"},
		}, nil
	case "codex":
		return launchTarget{
			AppID:       "codex",
			DisplayName: "Codex",
			Binaries:    []string{"codex"},
		}, nil
	case "pi", "picoding", "picodingagent":
		return launchTarget{
			AppID:       "pi",
			DisplayName: "Pi",
			Binaries:    []string{"pi"},
		}, nil
	case "openclaw":
		return launchTarget{
			AppID:       "openclaw",
			DisplayName: "OpenClaw",
			Binaries:    []string{"openclaw"},
		}, nil
	case "csgclaw":
		return launchTarget{
			AppID:       "csgclaw",
			DisplayName: "CSGClaw",
			Binaries:    []string{"csgclaw"},
		}, nil
	case "dify":
		return launchTarget{
			AppID:       "dify",
			DisplayName: "Dify",
		}, nil
	case "anythingllm":
		return launchTarget{
			AppID:       "anythingllm",
			DisplayName: "AnythingLLM",
		}, nil
	default:
		return launchTarget{}, fmt.Errorf("unknown AI app %q (supported: %s)\n\nRun 'csghub-lite launch --help' for supported apps and examples.", name, launchSupportedApps)
	}
}

func normalizeLaunchAppName(name string) string {
	replacer := strings.NewReplacer("-", "", "_", "", " ", "")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(name)))
}

func confirmAIAppInstall(name string, skipConfirm bool) (bool, error) {
	if skipConfirm {
		return true, nil
	}

	info, err := os.Stdin.Stat()
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return false, fmt.Errorf("%s is not installed; rerun with --yes to install it non-interactively", name)
	}

	fmt.Printf("%s is not installed. Install it now? [Y/n] ", name)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false, scanner.Err()
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "" || answer == "y" || answer == "yes", nil
}

func getAIApps(serverURL string) ([]api.AIAppInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(serverURL + "/api/apps")
	if err != nil {
		return nil, fmt.Errorf("querying AI apps: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("querying AI apps: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload api.AIAppsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decoding AI apps response: %w", err)
	}
	return payload.Apps, nil
}

func getAIAppInfo(serverURL, appID string) (api.AIAppInfo, error) {
	apps, err := getAIApps(serverURL)
	if err != nil {
		return api.AIAppInfo{}, err
	}
	for _, app := range apps {
		if app.ID == appID {
			return app, nil
		}
	}
	return api.AIAppInfo{}, fmt.Errorf("AI app %q was not found", appID)
}

func getLaunchModels(serverURL string) ([]api.ModelInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(serverURL + "/api/tags?refresh=1")
	if err != nil {
		return nil, fmt.Errorf("querying AI app models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("querying AI app models: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload api.TagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decoding AI app models response: %w", err)
	}
	return payload.Models, nil
}

func requestAIAppInstall(serverURL, appID string) (api.AIAppInfo, error) {
	body, _ := json.Marshal(api.AIAppInstallRequest{AppID: appID})
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(serverURL+"/api/apps/install", "application/json", bytes.NewReader(body))
	if err != nil {
		return api.AIAppInfo{}, fmt.Errorf("starting %s install: %w", appID, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var info api.AIAppInfo
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &info)
	}
	if len(respBody) > 0 && resp.StatusCode < 400 {
		if err := json.Unmarshal(respBody, &info); err != nil {
			return api.AIAppInfo{}, fmt.Errorf("decoding install response: %w", err)
		}
	}

	if resp.StatusCode >= 400 {
		if info.ID != "" && info.Disabled {
			return api.AIAppInfo{}, fmt.Errorf("%s is currently disabled in AI Apps", appID)
		}
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = resp.Status
		}
		return api.AIAppInfo{}, fmt.Errorf("starting %s install: %s", appID, msg)
	}

	if len(respBody) == 0 {
		return api.AIAppInfo{}, fmt.Errorf("decoding install response: %w", err)
	}

	return info, nil
}

func waitForAIAppInstall(serverURL string, target launchTarget) (api.AIAppInfo, error) {
	deadline := time.Now().Add(aiAppInstallWaitTimeout)
	lastLine := ""

	for time.Now().Before(deadline) {
		app, err := getAIAppInfo(serverURL, target.AppID)
		if err != nil {
			return api.AIAppInfo{}, err
		}

		line := renderAIAppInstallLine(target, app)
		if line != lastLine {
			fmt.Fprintf(os.Stderr, "\r\033[K%s", line)
			lastLine = line
		}

		switch app.Status {
		case "installed":
			if lastLine != "" {
				fmt.Fprintln(os.Stderr)
			}
			return app, nil
		case "failed":
			if lastLine != "" {
				fmt.Fprintln(os.Stderr)
			}
			if app.LastError != "" {
				if app.LogPath != "" {
					return api.AIAppInfo{}, fmt.Errorf("%s install failed: %s (log: %s)", target.DisplayName, app.LastError, app.LogPath)
				}
				return api.AIAppInfo{}, fmt.Errorf("%s install failed: %s", target.DisplayName, app.LastError)
			}
			return api.AIAppInfo{}, fmt.Errorf("%s install failed", target.DisplayName)
		}

		time.Sleep(1 * time.Second)
	}

	fmt.Fprintln(os.Stderr)
	return api.AIAppInfo{}, fmt.Errorf("timed out waiting for %s installation after %s", target.DisplayName, aiAppInstallWaitTimeout)
}

func renderAIAppInstallLine(target launchTarget, app api.AIAppInfo) string {
	if app.ProgressMode == "percent" && app.Progress > 0 {
		return fmt.Sprintf("Installing %s: %s (%d%%)", target.DisplayName, app.Phase, app.Progress)
	}
	if app.Phase != "" {
		return fmt.Sprintf("Installing %s: %s", target.DisplayName, app.Phase)
	}
	return fmt.Sprintf("Installing %s...", target.DisplayName)
}

func resolveLaunchBinary(candidates []string) (string, error) {
	ensureCommonAppBinDirsOnPath()

	for _, name := range candidates {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}

	for _, dir := range commonAppBinDirs() {
		for _, name := range candidates {
			if path, ok := lookupBinaryInDir(dir, name); ok {
				return path, nil
			}
		}
	}

	return "", fmt.Errorf("command not found")
}

func ensureCommonAppBinDirsOnPath() {
	current := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	seen := make(map[string]struct{}, len(current))
	for _, item := range current {
		seen[item] = struct{}{}
	}

	var updated []string
	updated = append(updated, current...)
	for _, dir := range commonAppBinDirs() {
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		updated = append([]string{dir}, updated...)
		seen[dir] = struct{}{}
	}
	_ = os.Setenv("PATH", strings.Join(updated, string(os.PathListSeparator)))
}

func commonAppBinDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if home != "" {
		dirs = append([]string{
			filepath.Join(home, "bin"),
			filepath.Join(home, ".local", "bin"),
		}, dirs...)
	}

	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			dirs = append([]string{filepath.Join(appData, "npm")}, dirs...)
		}
	}

	unique := make([]string, 0, len(dirs))
	seen := map[string]struct{}{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		unique = append(unique, dir)
	}
	return unique
}

func lookupBinaryInDir(dir, name string) (string, bool) {
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = []string{"", ".exe", ".cmd", ".bat", ".ps1"}
	}

	for _, ext := range exts {
		path := filepath.Join(dir, name+ext)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path, true
		}
	}

	return "", false
}
