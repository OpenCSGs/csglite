// Package upgrade provides automatic update functionality for csghub-lite
package upgrade

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
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
)

const (
	githubRepo      = "OpenCSGs/csglite"
	githubAPIURL    = "https://api.github.com/repos"
	gitlabHost      = "https://git-devops.opencsg.com"
	gitlabAPIURL    = gitlabHost + "/api/v4/projects"
	gitlabCSGHUBID  = "392"
	binaryName      = "csghub-lite"
	requestTimeout  = 30 * time.Second
	downloadTimeout = 5 * time.Minute
)

// currentVersion stores the application version, set via SetVersion
var currentVersion = "dev"

// SetVersion sets the current application version
func SetVersion(v string) {
	currentVersion = v
}

// Region represents the deployment region
type Region string

const (
	RegionCN   Region = "CN"
	RegionINTL Region = "INTL"
)

// Result contains information about an available update
type Result struct {
	CurrentVersion  string    `json:"current_version"`
	LatestVersion   string    `json:"latest_version"`
	Available       bool      `json:"available"`
	ReleaseNotes    string    `json:"release_notes"`
	PublishedAt     time.Time `json:"published_at"`
	DownloadURL     string    `json:"download_url"`
	GitLabMirrorURL string    `json:"gitlab_mirror_url"`
}

// Progress represents download progress
type Progress struct {
	Downloaded int64
	Total      int64
}

// ProgressFunc is a callback for download progress
type ProgressFunc func(Progress)

// GitHubRelease represents a GitHub release
type GitHubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// GitLabPackage represents a GitLab package
type GitLabPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Updater handles the upgrade process
type Updater struct {
	currentVersion string
	region         Region
	httpClient     *http.Client
}

// NewUpdater creates a new Updater instance
func NewUpdater(currentVersion string) *Updater {
	return &Updater{
		currentVersion: currentVersion,
		region:         detectRegion(),
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
	}
}

// WithRegion sets a specific region for the updater
func (u *Updater) WithRegion(region Region) *Updater {
	u.region = region
	return u
}

// detectRegion detects the region based on IP or environment variable
func detectRegion() Region {
	if region := os.Getenv("CSGHUB_LITE_REGION"); region != "" {
		return Region(region)
	}

	// Try to detect region via IP
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://ipinfo.io/country")
	if err != nil {
		// Default to CN if detection fails
		return RegionCN
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return RegionCN
	}

	country := strings.TrimSpace(string(body))
	if country == "CN" {
		return RegionCN
	}
	return RegionINTL
}

// getOS returns the current operating system
func getOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "darwin"
	case "linux":
		return "linux"
	case "windows":
		return "windows"
	default:
		return runtime.GOOS
	}
}

// getArch returns the current architecture
func getArch() string {
	switch runtime.GOARCH {
	case "amd64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

// getArchiveExtension returns the archive extension for the current OS
func getArchiveExtension() string {
	if runtime.GOOS == "windows" {
		return "zip"
	}
	return "tar.gz"
}

// Check checks if a new version is available
func Check() (*Result, error) {
	return CheckWithVersion(currentVersion)
}

// CheckWithVersion checks if a new version is available for a specific version
func CheckWithVersion(currentVersion string) (*Result, error) {
	updater := NewUpdater(currentVersion)
	return updater.CheckForUpdate(context.Background())
}

// CheckForUpdate checks if a new version is available
func (u *Updater) CheckForUpdate(ctx context.Context) (*Result, error) {
	var latestVersion string
	var release *GitHubRelease
	var err error

	// Try GitLab first for CN region, GitHub first for INTL
	if u.region == RegionCN {
		latestVersion, err = u.getLatestVersionFromGitLab(ctx)
		if err != nil {
			// Fallback to GitHub
			release, err = u.getLatestReleaseFromGitHub(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get latest version: %w", err)
			}
			latestVersion = release.TagName
		}
	} else {
		release, err = u.getLatestReleaseFromGitHub(ctx)
		if err != nil {
			// Fallback to GitLab
			latestVersion, err = u.getLatestVersionFromGitLab(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get latest version: %w", err)
			}
		} else {
			latestVersion = release.TagName
		}
	}

	// Normalize versions for comparison
	currentVer := normalizeVersion(u.currentVersion)
	latestVer := normalizeVersion(latestVersion)

	hasUpdate := compareVersions(latestVer, currentVer) > 0

	result := &Result{
		CurrentVersion: u.currentVersion,
		LatestVersion:  latestVersion,
		Available:      hasUpdate,
	}

	if release != nil {
		result.ReleaseNotes = release.Body
		result.PublishedAt = release.PublishedAt
	}

	// Generate download URLs
	osName := getOS()
	arch := getArch()
	ext := getArchiveExtension()
	archiveName := fmt.Sprintf("%s_%s_%s-%s.%s", binaryName, strings.TrimPrefix(latestVersion, "v"), osName, arch, ext)

	result.DownloadURL = fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", githubRepo, latestVersion, archiveName)
	result.GitLabMirrorURL = fmt.Sprintf("%s/%s/packages/generic/%s/%s/%s", gitlabAPIURL, gitlabCSGHUBID, binaryName, strings.TrimPrefix(latestVersion, "v"), archiveName)

	return result, nil
}

// Apply downloads and installs the update
func Apply(result *Result, progress ProgressFunc) error {
	return ApplyWithVersion(result, progress)
}

// ApplyWithVersion applies the update with version context
func ApplyWithVersion(result *Result, progress ProgressFunc) error {
	updater := NewUpdater(result.CurrentVersion)
	return updater.PerformUpgradeWithProgress(context.Background(), result, progress)
}

// PerformUpgrade downloads and installs the latest version
func (u *Updater) PerformUpgrade(ctx context.Context, result *Result) error {
	return u.PerformUpgradeWithProgress(ctx, result, nil)
}

// PerformUpgradeWithProgress downloads and installs the latest version with progress callback
func (u *Updater) PerformUpgradeWithProgress(ctx context.Context, result *Result, progress ProgressFunc) error {
	// Ensure temp directory exists (macOS may clean /var/folders)
	tmpBase := os.TempDir()
	if err := os.MkdirAll(tmpBase, 0o700); err != nil {
		return fmt.Errorf("failed to create temp base directory: %w", err)
	}
	tmpDir, err := os.MkdirTemp(tmpBase, "csghub-lite-upgrade-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Determine which URL to try first based on region
	var urls []string
	if u.region == RegionCN {
		urls = []string{result.GitLabMirrorURL, result.DownloadURL}
	} else {
		urls = []string{result.DownloadURL, result.GitLabMirrorURL}
	}

	// Download the archive
	archivePath := filepath.Join(tmpDir, "archive")
	if err := u.downloadFileWithProgress(ctx, urls, archivePath, progress); err != nil {
		return fmt.Errorf("failed to download update: %w", err)
	}

	// Extract the archive
	binaryPath, err := u.extractArchive(archivePath, tmpDir)
	if err != nil {
		return fmt.Errorf("failed to extract archive: %w", err)
	}

	// Get current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Install the new binary
	if err := u.installBinary(binaryPath, execPath); err != nil {
		return fmt.Errorf("failed to install binary: %w", err)
	}

	return nil
}

// getLatestReleaseFromGitHub fetches the latest release from GitHub
func (u *Updater) getLatestReleaseFromGitHub(ctx context.Context) (*GitHubRelease, error) {
	url := fmt.Sprintf("%s/%s/releases/latest", githubAPIURL, githubRepo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	// Add GitHub token if available
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	return &release, nil
}

// getLatestVersionFromGitLab fetches the latest version from GitLab
func (u *Updater) getLatestVersionFromGitLab(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/%s/packages?package_name=%s&per_page=1&sort=desc", gitlabAPIURL, gitlabCSGHUBID, binaryName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	// Add GitLab token if available
	if token := os.Getenv("GITLAB_TOKEN"); token != "" {
		req.Header.Set("PRIVATE-TOKEN", token)
	}

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitLab API returned status %d", resp.StatusCode)
	}

	var packages []GitLabPackage
	if err := json.NewDecoder(resp.Body).Decode(&packages); err != nil {
		return "", err
	}

	if len(packages) == 0 {
		return "", fmt.Errorf("no packages found")
	}

	return "v" + packages[0].Version, nil
}

// normalizeVersion removes the 'v' prefix if present
func normalizeVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}

// compareVersions compares two semantic versions
// Returns: -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2
func compareVersions(v1, v2 string) int {
	v1Parts := strings.Split(v1, ".")
	v2Parts := strings.Split(v2, ".")

	maxLen := len(v1Parts)
	if len(v2Parts) > maxLen {
		maxLen = len(v2Parts)
	}

	for i := 0; i < maxLen; i++ {
		var n1, n2 int
		if i < len(v1Parts) {
			fmt.Sscanf(v1Parts[i], "%d", &n1)
		}
		if i < len(v2Parts) {
			fmt.Sscanf(v2Parts[i], "%d", &n2)
		}

		if n1 < n2 {
			return -1
		} else if n1 > n2 {
			return 1
		}
	}

	return 0
}

// downloadFileWithProgress downloads a file from multiple URLs with progress callback
func (u *Updater) downloadFileWithProgress(ctx context.Context, urls []string, dest string, progress ProgressFunc) error {
	client := &http.Client{Timeout: downloadTimeout}

	var lastErr error
	for _, url := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			lastErr = err
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("download failed with status %d from %s", resp.StatusCode, url)
			resp.Body.Close()
			continue
		}

		out, err := os.Create(dest)
		if err != nil {
			lastErr = err
			resp.Body.Close()
			continue
		}

		// Get content length for progress
		total := resp.ContentLength
		var downloaded int64

		buf := make([]byte, 32*1024) // 32KB buffer
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				_, writeErr := out.Write(buf[:n])
				if writeErr != nil {
					out.Close()
					resp.Body.Close()
					lastErr = writeErr
					break
				}
				downloaded += int64(n)

				if progress != nil {
					progress(Progress{
						Downloaded: downloaded,
						Total:      total,
					})
				}
			}
			if err != nil {
				if err == io.EOF {
					out.Close()
					resp.Body.Close()
					return nil
				}
				out.Close()
				resp.Body.Close()
				lastErr = err
				break
			}
		}
	}

	return fmt.Errorf("all download attempts failed: %w", lastErr)
}

// extractArchive extracts the downloaded archive and returns the path to the binary
func (u *Updater) extractArchive(archivePath, destDir string) (string, error) {
	ext := getArchiveExtension()

	if ext == "zip" {
		return u.extractZip(archivePath, destDir)
	}
	return u.extractTarGz(archivePath, destDir)
}

// extractZip extracts a zip archive
func (u *Updater) extractZip(archivePath, destDir string) (string, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer r.Close()

	var binaryPath string

	for _, f := range r.File {
		// Skip directories
		if f.FileInfo().IsDir() {
			continue
		}

		// Extract the file
		destPath := filepath.Join(destDir, filepath.Base(f.Name))
		outFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return "", err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return "", err
		}

		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()

		if err != nil {
			return "", err
		}

		// Check if this is the binary we're looking for
		if filepath.Base(f.Name) == binaryName || filepath.Base(f.Name) == binaryName+".exe" {
			binaryPath = destPath
		}
	}

	if binaryPath == "" {
		return "", fmt.Errorf("binary not found in archive")
	}

	return binaryPath, nil
}

// extractTarGz extracts a tar.gz archive
func (u *Updater) extractTarGz(archivePath, destDir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var binaryPath string

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		// Extract the file
		destPath := filepath.Join(destDir, filepath.Base(header.Name))
		outFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode))
		if err != nil {
			return "", err
		}

		if _, err := io.Copy(outFile, tr); err != nil {
			outFile.Close()
			return "", err
		}
		outFile.Close()

		// Check if this is the binary we're looking for
		if filepath.Base(header.Name) == binaryName {
			binaryPath = destPath
		}
	}

	if binaryPath == "" {
		return "", fmt.Errorf("binary not found in archive")
	}

	return binaryPath, nil
}

// installBinary replaces the current binary with the new one
func (u *Updater) installBinary(newBinary, currentBinary string) error {
	// Make the new binary executable
	if err := os.Chmod(newBinary, 0755); err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		return u.installBinaryWindows(newBinary, currentBinary)
	}

	// Move the new binary to the current location
	if err := os.Rename(newBinary, currentBinary); err != nil {
		// If rename fails, try copy
		if err := u.copyFile(newBinary, currentBinary); err != nil {
			return fmt.Errorf("failed to replace binary: %w", err)
		}
	}

	return nil
}

func (u *Updater) installBinaryWindows(newBinary, currentBinary string) error {
	stageDir := filepath.Join(os.TempDir(), fmt.Sprintf("csghub-lite-upgrade-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return fmt.Errorf("creating Windows upgrade staging directory: %w", err)
	}

	stagedBinary := filepath.Join(stageDir, filepath.Base(currentBinary))
	if err := u.copyFile(newBinary, stagedBinary); err != nil {
		return fmt.Errorf("staging Windows upgrade binary: %w", err)
	}

	scriptPath := filepath.Join(stageDir, "apply-upgrade.ps1")
	script, err := windowsUpgradeScript(os.Getpid(), currentBinary, stagedBinary, stageDir, windowsRestartArgs())
	if err != nil {
		return err
	}
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return fmt.Errorf("writing Windows upgrade script: %w", err)
	}

	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		if powershell, err = exec.LookPath("powershell"); err != nil {
			if powershell, err = exec.LookPath("pwsh.exe"); err != nil {
				if powershell, err = exec.LookPath("pwsh"); err != nil {
					return fmt.Errorf("PowerShell not found for Windows upgrade: %w", err)
				}
			}
		}
	}

	cmd := exec.Command(powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting Windows upgrade helper: %w", err)
	}

	// Give HTTP/CLI callers time to report success, then release the locked exe
	// so the helper can replace it and restart the original command.
	go func() {
		time.Sleep(1500 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}

func windowsRestartArgs() []string {
	if len(os.Args) <= 1 {
		return []string{"serve"}
	}
	first := strings.ToLower(os.Args[1])
	if first == "upgrade" {
		return nil
	}
	return append([]string(nil), os.Args[1:]...)
}

func windowsUpgradeScript(pid int, target, stagedBinary, stageDir string, restartArgs []string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		wd = filepath.Dir(target)
	}

	var args []string
	for _, arg := range restartArgs {
		args = append(args, psSingleQuote(arg))
	}
	argList := "@()"
	if len(args) > 0 {
		argList = "@(" + strings.Join(args, ", ") + ")"
	}

	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$target = %s
$staged = %s
$stageDir = %s
$workingDir = %s
$restartArgs = %s
$log = Join-Path $stageDir 'upgrade.log'
function Log([string]$message) {
  Add-Content -Path $log -Value ("[{0}] {1}" -f (Get-Date -Format o), $message)
}
try {
  Log 'Waiting for csghub-lite to exit'
  for ($i = 0; $i -lt 240; $i++) {
    $proc = Get-Process -Id %d -ErrorAction SilentlyContinue
    if ($null -eq $proc) { break }
    Start-Sleep -Milliseconds 500
  }
  $proc = Get-Process -Id %d -ErrorAction SilentlyContinue
  if ($null -ne $proc) {
    throw 'Timed out waiting for csghub-lite to exit'
  }

  $old = "$target.old"
  if (Test-Path -LiteralPath $old) {
    Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
  }
  if (Test-Path -LiteralPath $target) {
    Move-Item -LiteralPath $target -Destination $old -Force
  }
  Copy-Item -LiteralPath $staged -Destination $target -Force
  if (Test-Path -LiteralPath $old) {
    Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
  }
  Log 'Installed new csghub-lite binary'

  if ($restartArgs.Count -gt 0) {
    Start-Process -FilePath $target -ArgumentList $restartArgs -WorkingDirectory $workingDir
    Log 'Restarted csghub-lite'
  }
} catch {
  Log ("Upgrade failed: " + $_.Exception.Message)
  exit 1
}
Start-Sleep -Seconds 1
Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $MyInvocation.MyCommand.Path -Force -ErrorAction SilentlyContinue
`, psSingleQuote(target), psSingleQuote(stagedBinary), psSingleQuote(stageDir), psSingleQuote(wd), argList, pid, pid), nil
}

func psSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// copyFile copies a file from src to dst
func (u *Updater) copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Get source file permissions
	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// Restart restarts the application with the new binary
func Restart() error {
	return RestartAfter(0)
}

// RestartAfter starts a replacement process and exits the current process after
// delay. On Unix, a small shell helper waits for this process to exit before
// starting the new binary so the server port can be rebound cleanly.
func RestartAfter(delay time.Duration) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command(execPath, os.Args[1:]...)
	} else {
		shell, err := exec.LookPath("sh")
		if err != nil {
			return fmt.Errorf("finding shell for restart helper: %w", err)
		}
		args := append([]string{
			"-c",
			`while kill -0 "$CSGHUB_LITE_RESTART_PARENT" 2>/dev/null; do sleep 1; done; exec "$0" "$@"`,
			execPath,
		}, os.Args[1:]...)
		cmd = exec.Command(shell, args...)
		cmd.Env = append(os.Environ(), fmt.Sprintf("CSGHUB_LITE_RESTART_PARENT=%d", os.Getpid()))
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	if delay <= 0 {
		requestGracefulExit()
		return nil
	}
	go func() {
		time.Sleep(delay)
		requestGracefulExit()
	}()
	return nil
}

func requestGracefulExit() {
	if runtime.GOOS != "windows" {
		if proc, err := os.FindProcess(os.Getpid()); err == nil {
			_ = proc.Signal(os.Interrupt)
			time.Sleep(10 * time.Second)
		}
	}
	os.Exit(0)
}

// GetLatestVersion fetches the latest version string
func (u *Updater) GetLatestVersion(ctx context.Context) (string, error) {
	result, err := u.CheckForUpdate(ctx)
	if err != nil {
		return "", err
	}
	return result.LatestVersion, nil
}
