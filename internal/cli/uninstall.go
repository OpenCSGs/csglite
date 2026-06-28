package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/spf13/cobra"
)

var stopBackgroundServiceForUninstall = stopBackgroundServiceIfRunning

func newUninstallCmd() *cobra.Command {
	var yes bool
	var removeAll bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove csghub-lite and llama-server",
		Long: `Remove csghub-lite and its local inference dependencies:

  - csghub-lite binary
  - llama-server binary and shared libraries

By default, local data under ~/.csghub-lite is preserved, including:

  - Configuration
  - Downloaded models and datasets
  - Logs

Use --all to also remove ~/.csghub-lite and everything in it.
Use --yes to skip confirmation prompts.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(yes, removeAll)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation prompts")
	cmd.Flags().BoolVar(&removeAll, "all", false, "Also remove local data under ~/.csghub-lite")
	return cmd
}

func runUninstall(skipConfirm, removeAll bool) error {
	appHome, err := config.AppHome()
	if err != nil {
		return fmt.Errorf("resolving app home: %w", err)
	}

	csghubBin, _ := exec.LookPath("csghub-lite")
	llamaBin := findInstalledLlamaServer()
	llamaLibs := findLlamaLibs(llamaBin)

	fmt.Println("The following will be removed:")
	fmt.Println()
	if csghubBin != "" {
		fmt.Printf("  Binary:       %s\n", csghubBin)
	}
	if llamaBin != "" {
		fmt.Printf("  llama-server: %s\n", llamaBin)
	}
	for _, lib := range llamaLibs {
		fmt.Printf("  Library:      %s\n", lib)
	}
	if removeAll {
		fmt.Printf("  Data dir:     %s\n", appHome)
	} else {
		fmt.Printf("  Data dir:     %s (preserved; use --all to remove)\n", appHome)
	}
	fmt.Println()

	if !skipConfirm {
		if removeAll {
			fmt.Print("Are you sure you want to uninstall and remove all local data? [y/N] ")
		} else {
			fmt.Print("Are you sure you want to uninstall binaries and keep local data? [y/N] ")
		}
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			return nil
		}
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "y" && answer != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	if err := stopBackgroundServiceForUninstall(); err != nil {
		return fmt.Errorf("stopping csghub-lite service before uninstall: %w", err)
	}

	var errors []string

	for _, lib := range llamaLibs {
		if err := removeFile(lib); err != nil {
			errors = append(errors, fmt.Sprintf("remove %s: %v", lib, err))
		} else {
			fmt.Printf("  Removed %s\n", lib)
		}
	}
	if llamaBin != "" {
		if err := removeFile(llamaBin); err != nil {
			errors = append(errors, fmt.Sprintf("remove %s: %v", llamaBin, err))
		} else {
			fmt.Printf("  Removed %s\n", llamaBin)
		}
	}

	if removeAll {
		if info, err := os.Stat(appHome); err == nil && info.IsDir() {
			if err := os.RemoveAll(appHome); err != nil {
				errors = append(errors, fmt.Sprintf("remove %s: %v", appHome, err))
			} else {
				fmt.Printf("  Removed %s\n", appHome)
			}
		}
	}

	// On Windows, clean the install directory from User PATH
	if runtime.GOOS == "windows" {
		cleanWindowsPath(csghubBin, llamaBin)
	}

	// Self-delete last
	if csghubBin != "" {
		if err := removeFile(csghubBin); err != nil {
			errors = append(errors, fmt.Sprintf("remove %s: %v", csghubBin, err))
		} else {
			fmt.Printf("  Removed %s\n", csghubBin)
		}
	}

	fmt.Println()
	if len(errors) > 0 {
		fmt.Println("Completed with errors:")
		for _, e := range errors {
			fmt.Printf("  - %s\n", e)
		}
		fmt.Println()
		if runtime.GOOS == "windows" {
			fmt.Println("Some files may require running the command as Administrator.")
		} else {
			fmt.Println("Some files may require manual removal with sudo.")
		}
		return fmt.Errorf("uninstall completed with %d error(s)", len(errors))
	}

	if removeAll {
		fmt.Println("csghub-lite has been completely uninstalled, including local data.")
	} else {
		fmt.Printf("csghub-lite and llama-server have been uninstalled. Local data was preserved at %s.\n", appHome)
	}
	return nil
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil {
		if os.IsPermission(err) {
			return elevatedRemove(path)
		}
		return err
	}
	return nil
}

func elevatedRemove(path string) error {
	fmt.Printf("  Requires elevated privileges to remove %s\n", path)
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/C", "del", "/F", "/Q", path)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	cmd := exec.Command("sudo", "rm", "-f", path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func findInstalledLlamaServer() string {
	if path, err := exec.LookPath("llama-server"); err == nil {
		return path
	}
	home, _ := os.UserHomeDir()
	var locations []string
	switch runtime.GOOS {
	case "windows":
		if home != "" {
			locations = append(locations, filepath.Join(home, "bin", "llama-server.exe"))
		}
		locations = append(locations, `C:\llama.cpp\build\bin\Release\llama-server.exe`)
	default:
		locations = []string{
			"/usr/local/bin/llama-server",
			"/opt/homebrew/bin/llama-server",
		}
		if home != "" {
			locations = append(locations, filepath.Join(home, "bin", "llama-server"))
		}
	}
	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			return loc
		}
	}
	return ""
}

// findLlamaLibs returns shared libraries co-located with llama-server.
func findLlamaLibs(llamaBin string) []string {
	if llamaBin == "" {
		return nil
	}
	dir := filepath.Dir(llamaBin)
	var libs []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if isLlamaLib(e.Name()) {
			libs = append(libs, filepath.Join(dir, e.Name()))
		}
	}
	return libs
}

func isLlamaLib(name string) bool {
	prefixes := []string{"libllama", "libggml", "libmtmd", "libllava",
		"llama", "ggml", "mtmd", "llava"}
	lower := strings.ToLower(name)
	for _, prefix := range prefixes {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		if strings.HasSuffix(lower, ".dylib") ||
			strings.Contains(lower, ".so") ||
			strings.HasSuffix(lower, ".dll") {
			return true
		}
	}
	return false
}

// cleanWindowsPath removes the install directories from the Windows User PATH.
func cleanWindowsPath(csghubBin, llamaBin string) {
	dirs := map[string]bool{}
	if csghubBin != "" {
		dirs[filepath.Dir(csghubBin)] = true
	}
	if llamaBin != "" {
		dirs[filepath.Dir(llamaBin)] = true
	}
	if len(dirs) == 0 {
		return
	}

	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		`[Environment]::GetEnvironmentVariable("Path","User")`)
	out, err := cmd.Output()
	if err != nil {
		return
	}
	userPath := strings.TrimSpace(string(out))
	if userPath == "" {
		return
	}

	var kept []string
	changed := false
	for _, part := range strings.Split(userPath, ";") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if dirs[p] {
			fmt.Printf("  Removed %s from User PATH\n", p)
			changed = true
			continue
		}
		kept = append(kept, p)
	}
	if !changed {
		return
	}

	newPath := strings.Join(kept, ";")
	setCmd := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf(`[Environment]::SetEnvironmentVariable("Path","%s","User")`, newPath))
	if err := setCmd.Run(); err != nil {
		fmt.Printf("  Warning: failed to update User PATH: %v\n", err)
	}
}
