package upgrade

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/scripts"
)

// llamaAlignTimeout bounds a single alignment run. Downloading a llama.cpp
// release is the slow part — a Linux CUDA asset plus its runtime can exceed a
// gigabyte — so the bound is generous: the run is in the background and its
// caller cancels it when the server stops.
const llamaAlignTimeout = 30 * time.Minute

// AlignLlamaRuntime brings the llama-server runtime csghub-lite manages in line
// with the llama.cpp tag this binary is pinned to. Replacing the csghub-lite
// binary does not touch the runtime, so a copy installed by an older release
// keeps its old build and cannot load newer architectures — the Laya decision
// model (ModernBertDecisionModel) needs b11541 or later.
//
// Callers run this best-effort in the background at start-up: the returned
// error is meant for the log, and the next start retries. It is a no-op when
// llama-server is absent, was not installed by csghub-lite, already matches the
// pinned tag, or reports no comparable build id.
func AlignLlamaRuntime(ctx context.Context, tempDir string) error {
	if v := strings.TrimSpace(os.Getenv("CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER")); v != "" && v != "1" {
		log.Printf("LLAMA: skipping runtime alignment (CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER=%s)", v)
		return nil
	}

	binary := inference.ResolveLlamaServerBinary()
	if binary == "" {
		return nil
	}
	if !managedLlamaRuntime(binary) {
		log.Printf("LLAMA: %s was not installed by csghub-lite; leaving its version alone", binary)
		return nil
	}

	pinned := pinnedLlamaBuildID()
	if pinned == 0 {
		return fmt.Errorf("cannot read a llama.cpp build id from the pinned tag %q", convert.BundledConverterLLamacppRef)
	}
	local := inference.LlamaServerBuildID()
	if local == 0 {
		log.Printf("LLAMA: cannot read the build id of %s; skipping runtime alignment", binary)
		return nil
	}
	if local == pinned {
		return nil
	}

	dir := filepath.Dir(binary)
	if !canReplaceLlamaServer(dir) {
		// install.sh escalates to sudo for a directory it cannot write, and
		// sudo prompts on the terminal rather than on stdin: a background
		// alignment must never block on that, so leave it to a manual run.
		log.Printf("LLAMA: %s is not writable; run the installer as root to align llama-server to %s", dir, convert.BundledConverterLLamacppRef)
		return nil
	}

	log.Printf("LLAMA: aligning llama-server b%d to b%d (pinned by this build)", local, pinned)
	if err := installLlamaRuntime(ctx, tempDir, binary); err != nil {
		return err
	}

	// The installer exits 0 when it had nothing to install (no matching asset,
	// failed download), so confirm the copy on disk before claiming success.
	inference.ResetLlamaServerVersionCache()
	if got := inference.LlamaServerBuildID(); got != pinned {
		return fmt.Errorf("llama-server in %s reports %q after alignment, want build %d", dir, inference.LlamaServerVersion(), pinned)
	}
	log.Printf("LLAMA: llama-server is now %s in %s", convert.BundledConverterLLamacppRef, dir)
	return nil
}

// installLlamaRuntime runs the embedded installer in engine-only mode so the
// asset selection, mirror fallback and post-processing stay identical to a
// fresh install.
func installLlamaRuntime(ctx context.Context, tempDir, binary string) error {
	dir := filepath.Dir(binary)

	scriptPath, scriptDir, err := writeLlamaInstallScript(tempDir)
	if err != nil {
		return err
	}
	// scriptDir also serves as TMPDIR below, so this cleanup removes whatever a
	// failed or interrupted download left behind.
	defer os.RemoveAll(scriptDir)

	if runtime.GOOS == "windows" {
		// Windows locks a running executable, so install.ps1 cannot replace
		// llama-server.exe while a csghub-lite managed copy is still running.
		stopManagedLlamaServer(binary)
	}

	runCtx, cancel := context.WithTimeout(ctx, llamaAlignTimeout)
	defer cancel()

	cmd, err := llamaInstallCommand(runCtx, scriptPath)
	if err != nil {
		return err
	}
	// Capture the installer's output in a file instead of a pipe. A pipe would
	// make Run wait until every process holding it exits, and the installer
	// forks subshells: a cancelled run has to return promptly so its cleanup
	// still happens.
	logPath := filepath.Join(scriptDir, "install.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", logPath, err)
	}
	defer logFile.Close()

	cmd.Env = llamaInstallEnv(dir, scriptDir)
	cmd.Stdin = nil // no TTY: nothing may prompt for input while the server runs
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Run(); err != nil {
		output, _ := os.ReadFile(logPath)
		return fmt.Errorf("running %s: %w\n%s", filepath.Base(scriptPath), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// writeLlamaInstallScript writes the embedded installer into a fresh
// subdirectory of tempDir and returns its path and that directory. Reusing the
// shipped script keeps this path on the same llama.cpp tag as the lockstep test
// that pins scripts/install.sh; the directory doubles as the installer's TMPDIR,
// so the caller's cleanup also removes a failed download.
func writeLlamaInstallScript(tempDir string) (string, string, error) {
	name := "install.sh"
	content := scripts.InstallSh
	if runtime.GOOS == "windows" {
		name = "install.ps1"
		content = scripts.InstallPs1
	}

	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return "", "", fmt.Errorf("creating %s: %w", tempDir, err)
	}
	dir, err := os.MkdirTemp(tempDir, ".engine-align-*")
	if err != nil {
		return "", "", fmt.Errorf("creating engine alignment directory: %w", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		os.RemoveAll(dir)
		return "", "", fmt.Errorf("writing %s: %w", name, err)
	}
	return path, dir, nil
}

// llamaInstallCommand runs the installer with the engine-only flag so it does
// not download or replace the csghub-lite binary that is already running.
func llamaInstallCommand(ctx context.Context, scriptPath string) (*exec.Cmd, error) {
	if runtime.GOOS == "windows" {
		powershell, err := lookupPowerShell()
		if err != nil {
			return nil, err
		}
		return exec.CommandContext(ctx, powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath, "-EngineOnly"), nil
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		return nil, fmt.Errorf("finding shell for engine alignment: %w", err)
	}
	return exec.CommandContext(ctx, shell, scriptPath, "--engine-only"), nil
}

// llamaInstallEnv pins the installers to the runtime inference resolves and
// keeps their downloads under the csghub-lite storage root.
func llamaInstallEnv(dir, tempDir string) []string {
	env := os.Environ()
	// The installer runs in the background with no terminal, so it must not try
	// to escalate: CSGHUB_LITE_NONINTERACTIVE makes install.sh skip the steps
	// that need root instead of calling sudo, and the auto-install switches stay
	// off so it never starts an apt-get/dnf it cannot finish. Steps skipped this
	// way (patchelf, NVIDIA CUDA runtime packages) are reported in the install
	// log, and a manual installer run can still do them.
	for _, kv := range [][2]string{
		{"CSGHUB_LITE_NONINTERACTIVE", "1"},
		{"CSGHUB_LITE_AUTO_INSTALL_PATCHELF", "0"},
		{"CSGHUB_LITE_AUTO_INSTALL_CUDA_LIBS", "0"},
		{"CSGHUB_LITE_LLAMA_CPP_TAG", convert.BundledConverterLLamacppRef},
		{"CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR", dir},
		{"TMPDIR", tempDir},
		{"TMP", tempDir},
		{"TEMP", tempDir},
	} {
		env = setEnv(env, kv[0], kv[1])
	}
	// install.ps1 has no install-dir variable and both installers look the
	// runtime up through PATH, so the managed copy has to win that lookup.
	path := dir
	if current := envValue(env, "PATH"); current != "" {
		path += string(os.PathListSeparator) + current
	}
	return setEnv(env, "PATH", path)
}

// setEnv replaces name in env, matching case-insensitively so Windows does not
// end up with duplicate PATH or TEMP entries under different casing.
func setEnv(env []string, name, value string) []string {
	for i, kv := range env {
		if key, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(key, name) {
			env[i] = key + "=" + value
			return env
		}
	}
	return append(env, name+"="+value)
}

func envValue(env []string, name string) string {
	for _, kv := range env {
		if key, value, ok := strings.Cut(kv, "="); ok && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

// stopManagedLlamaServer stops llama-server processes running binary. Only
// Windows needs this: elsewhere the installer replaces the file in place and
// the next model load picks up the new runtime.
func stopManagedLlamaServer(binary string) {
	powershell, err := lookupPowerShell()
	if err != nil {
		log.Printf("LLAMA: cannot stop llama-server before aligning it: %v", err)
		return
	}

	script := fmt.Sprintf(`$target = %s
Get-CimInstance Win32_Process -Filter "Name='llama-server.exe'" -ErrorAction SilentlyContinue |
  Where-Object { $_.ExecutablePath -and ($_.ExecutablePath -ieq $target) } |
  ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
Start-Sleep -Milliseconds 500`, psSingleQuote(binary))

	cmd := exec.Command(powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("LLAMA: stopping llama-server failed: %v\n%s", err, strings.TrimSpace(string(out)))
	}
}

// lookupPowerShell finds the PowerShell host used for Windows-only steps.
func lookupPowerShell() (string, error) {
	var lastErr error
	for _, name := range []string{"powershell.exe", "powershell", "pwsh.exe", "pwsh"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return path, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("PowerShell not found for Windows upgrade: %w", lastErr)
}

// canReplaceLlamaServer reports whether files in dir can be replaced without
// escalating privileges. install.sh falls back to sudo for a directory it
// cannot write, and sudo prompts on the terminal instead of on stdin, so the
// caller checks first and leaves that upgrade to a manual installer run.
func canReplaceLlamaServer(dir string) bool {
	probe, err := os.CreateTemp(dir, ".csghub-lite-write-check-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}

// managedLlamaRuntime reports whether csghub-lite may replace the llama-server
// at path. There is no engine manifest yet, so this is a location heuristic:
// copies installed by install.sh / install.ps1 land next to the csghub-lite
// binary or in the per-user bin directories those installers target. A
// Homebrew keg, the macOS app bundle's private runtime, or a user's own build
// is left at its own version.
func managedLlamaRuntime(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	resolved := path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		resolved = real
	}

	sep := string(filepath.Separator)
	if strings.Contains(resolved, sep+"Cellar"+sep) {
		return false
	}
	if strings.Contains(resolved, ".app"+sep+"Contents"+sep) {
		return false
	}

	dir := filepath.Dir(resolved)
	if configured := strings.TrimSpace(os.Getenv("CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR")); configured != "" && sameDir(dir, configured) {
		return true
	}

	var candidates []string
	if exePath, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exePath))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "bin"),
		)
	}
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates, "/opt/homebrew/bin")
	case "linux":
		candidates = append(candidates, "/usr/local/bin")
	case "windows":
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, "AppData", "Local", "Programs", "csghub-lite"))
		}
	}

	for _, candidate := range candidates {
		if sameDir(dir, candidate) {
			return true
		}
	}
	return false
}

func sameDir(a, b string) bool {
	a = resolveDir(a)
	b = resolveDir(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func resolveDir(dir string) string {
	dir = filepath.Clean(dir)
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}

// pinnedLlamaBuildID returns the build number of the llama.cpp tag this binary
// is pinned to ("b11541" -> 11541), or 0 when the tag is not numeric.
func pinnedLlamaBuildID() int {
	ref := strings.TrimPrefix(strings.TrimSpace(convert.BundledConverterLLamacppRef), "b")
	id, err := strconv.Atoi(ref)
	if err != nil {
		return 0
	}
	return id
}
