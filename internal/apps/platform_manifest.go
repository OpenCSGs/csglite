package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	platformCheckTTL      = 10 * time.Minute
	platformCheckRetryTTL = time.Minute
	platformCheckTimeout  = 10 * time.Second
	// platformCheckingReason keeps the app disabled until the first
	// downloads.json check settles.
	platformCheckingReason = "platform_checking"
)

// schedulePlatformCheckLocked starts a background downloads.json check when
// the previous verdict for spec has expired. The caller must hold m.mu.
func (m *Manager) schedulePlatformCheckLocked(spec appSpec) {
	check := m.platformChecks[spec.id]
	if check == nil {
		check = &platformCheck{}
		m.platformChecks[spec.id] = check
	}
	if check.running || time.Now().Before(check.expiresAt) {
		return
	}
	check.running = true
	go m.runPlatformCheck(spec)
}

func (m *Manager) runPlatformCheck(spec appSpec) {
	ctx, cancel := context.WithTimeout(context.Background(), platformCheckTimeout)
	defer cancel()

	manifest, err := m.fetchDesktopDownloadsManifest(ctx, spec)
	if err != nil {
		log.Printf("AI APP %s: platform availability check failed: %v", spec.id, err)
		// Without a manifest the platform is unconfirmed, so the app stays
		// disabled unless an earlier check already settled it. An existing
		// install is the exception: it must still launch offline.
		_, _, installed := detectInstalled(ctx, spec)
		m.mu.Lock()
		defer m.mu.Unlock()
		check := m.platformChecks[spec.id]
		if check != nil {
			check.running = false
			check.expiresAt = time.Now().Add(platformCheckRetryTTL)
		}
		if installed {
			m.applyPlatformAvailabilityLocked(spec.id, true, "")
		} else if check == nil || !check.settled {
			m.applyPlatformAvailabilityLocked(spec.id, false, platformCheckingReason)
		}
		return
	}

	supported, reason := true, ""
	if _, ok := manifest.latestArtifactFor(runtime.GOOS, runtime.GOARCH); !ok {
		supported, reason = false, desktopPlatformDisabledReason(runtime.GOOS)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if check := m.platformChecks[spec.id]; check != nil {
		check.running = false
		check.settled = true
		check.expiresAt = time.Now().Add(platformCheckTTL)
	}
	m.applyPlatformAvailabilityLocked(spec.id, supported, reason)
}

// applyPlatformAvailabilityLocked updates the spec and visible state of a
// platformManifest app. The caller must hold m.mu.
func (m *Manager) applyPlatformAvailabilityLocked(appID string, supported bool, reason string) {
	for i := range m.specs {
		if m.specs[i].id != appID {
			continue
		}
		previousReason := m.specs[i].disabledReason
		m.specs[i].supported = supported
		m.specs[i].disabledReason = reason

		st := m.states[appID]
		if st == nil || st.running {
			return
		}
		st.info.Supported = supported
		st.info.Disabled = !supported
		st.info.DisabledReason = reason
		if !supported {
			st.info.Status = "idle"
			st.info.Phase = reason
		} else if st.info.Phase == previousReason {
			st.info.Phase = "ready"
		}
		st.detectExpires = time.Time{}
		st.info.UpdatedAt = time.Now()
		return
	}
}

func (m *Manager) fetchDesktopDownloadsManifest(ctx context.Context, spec appSpec) (desktopDownloadsManifest, error) {
	var manifest desktopDownloadsManifest
	if spec.latest == nil {
		return manifest, fmt.Errorf("no downloads manifest configured")
	}
	url := strings.TrimSpace(spec.latest.baseURL)
	if override := strings.TrimSpace(os.Getenv(spec.latest.envVar)); override != "" {
		url = override
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return manifest, err
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return manifest, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return manifest, fmt.Errorf("downloads manifest returned %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, latestVersionResponseLimit))
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

// desktopDownloadsManifest is the release-channel downloads.json shared by
// OpenCSG desktop apps.
type desktopDownloadsManifest struct {
	Latest   string `json:"latest"`
	Versions map[string]struct {
		Artifacts []desktopDownloadArtifact `json:"artifacts"`
	} `json:"versions"`
}

type desktopDownloadArtifact struct {
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	URL      string `json:"url"`
}

// latestArtifactFor reports whether the latest release ships a package the
// installer scripts can handle on goos/goarch.
func (m desktopDownloadsManifest) latestArtifactFor(goos, goarch string) (desktopDownloadArtifact, bool) {
	release, ok := m.Versions[strings.TrimSpace(m.Latest)]
	if !ok {
		return desktopDownloadArtifact{}, false
	}
	for _, artifact := range release.Artifacts {
		if desktopManifestPlatform(artifact.Platform) == goos &&
			desktopManifestArch(artifact.Arch) == goarch &&
			desktopPackageSupported(goos, artifact.URL) {
			return artifact, true
		}
	}
	return desktopDownloadArtifact{}, false
}

func desktopManifestPlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "macos", "darwin", "mac":
		return "darwin"
	case "windows", "win32", "win":
		return "windows"
	case "linux":
		return "linux"
	default:
		return ""
	}
}

func desktopManifestArch(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "arm64", "aarch64":
		return "arm64"
	case "x86_64", "amd64", "x64":
		return "amd64"
	default:
		return ""
	}
}

func desktopPackageSupported(goos, url string) bool {
	url = strings.ToLower(strings.TrimSpace(url))
	if i := strings.IndexAny(url, "?#"); i >= 0 {
		url = url[:i]
	}
	switch goos {
	case "darwin":
		return strings.HasSuffix(url, ".dmg")
	case "windows":
		return strings.HasSuffix(url, ".exe") || strings.HasSuffix(url, ".msi")
	case "linux":
		return strings.HasSuffix(url, ".appimage")
	default:
		return false
	}
}

// desktopPlatformDisabledReason maps a host without a published package to the
// phase key the AI Apps page already renders.
func desktopPlatformDisabledReason(goos string) string {
	switch goos {
	case "linux":
		return "linux_unsupported"
	case "windows":
		return "windows_unsupported"
	default:
		return "platform_unsupported"
	}
}
