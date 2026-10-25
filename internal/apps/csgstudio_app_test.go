package apps

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestDesktopDownloadsManifestLatestArtifactFor(t *testing.T) {
	manifest := desktopDownloadsManifest{
		Latest: "0.2.0",
		Versions: map[string]struct {
			Artifacts []desktopDownloadArtifact `json:"artifacts"`
		}{
			"0.1.0": {Artifacts: []desktopDownloadArtifact{
				{Platform: "windows", Arch: "x86_64", URL: "https://example.com/CSGStudio_0.1.0.exe"},
			}},
			"0.2.0": {Artifacts: []desktopDownloadArtifact{
				{Platform: "macos", Arch: "arm64", URL: "https://example.com/CSGStudio_0.2.0_macos_arm64.dmg"},
				{Platform: "linux", Arch: "x86_64", URL: "https://example.com/CSGStudio_0.2.0_amd64.deb"},
				{Platform: "linux", Arch: "aarch64", URL: "https://example.com/CSGStudio_0.2.0.AppImage?sig=1"},
			}},
		},
	}

	for _, tc := range []struct {
		goos, goarch string
		want         bool
	}{
		{"darwin", "arm64", true},
		{"darwin", "amd64", false},
		// Only an older release ships Windows.
		{"windows", "amd64", false},
		// The installer cannot handle a .deb.
		{"linux", "amd64", false},
		{"linux", "arm64", true},
	} {
		if _, got := manifest.latestArtifactFor(tc.goos, tc.goarch); got != tc.want {
			t.Errorf("latestArtifactFor(%s, %s) = %v, want %v", tc.goos, tc.goarch, got, tc.want)
		}
	}
}

func TestCSGStudioPlatformAvailabilityFollowsManifest(t *testing.T) {
	hostArtifact := fmt.Sprintf(`{"platform":%q,"arch":%q,"url":%q}`,
		hostManifestPlatform(), hostManifestArch(), "https://example.com/CSGStudio"+hostPackageExt())
	otherArtifact := `{"platform":"unknown-os","arch":"arm64","url":"https://example.com/CSGStudio.dmg"}`

	for _, tc := range []struct {
		name       string
		status     int
		artifact   string
		installed  bool
		supported  bool
		wantReason string
	}{
		{name: "host published", status: http.StatusOK, artifact: hostArtifact, supported: true},
		{name: "host missing", status: http.StatusOK, artifact: otherArtifact, wantReason: desktopPlatformDisabledReason(runtime.GOOS)},
		{name: "manifest unavailable", status: http.StatusInternalServerError, wantReason: platformCheckingReason},
		{name: "manifest unavailable but installed", status: http.StatusInternalServerError, installed: true, supported: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := setTempHome(t)
			if tc.installed {
				writeManagedCSGStudio(t, home)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status != http.StatusOK {
					w.WriteHeader(tc.status)
					return
				}
				_, _ = fmt.Fprintf(w, `{"latest":"0.2.0","versions":{"0.2.0":{"version":"0.2.0","artifacts":[%s]}}}`, tc.artifact)
			}))
			defer server.Close()
			t.Setenv(csgstudioManifestEnvVar, server.URL+"/downloads.json")

			mgr := NewManager(nil)
			info := waitForPlatformCheck(t, mgr, csgstudioAppID)
			if info.Supported != tc.supported || info.Disabled == tc.supported || info.DisabledReason != tc.wantReason {
				t.Fatalf("info = supported:%v disabled:%v reason:%q, want supported:%v reason:%q",
					info.Supported, info.Disabled, info.DisabledReason, tc.supported, tc.wantReason)
			}
			if !tc.supported {
				if _, err := mgr.Install(csgstudioAppID); err == nil {
					t.Fatal("Install() succeeded on a platform the manifest does not publish")
				}
			}
		})
	}
}

func TestCSGStudioKeepsSettledVerdictWhenManifestBecomesUnreadable(t *testing.T) {
	setTempHome(t)
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = fmt.Fprint(w, `{"latest":"0.2.0","versions":{"0.2.0":{"artifacts":[]}}}`)
	}))
	defer server.Close()
	t.Setenv(csgstudioManifestEnvVar, server.URL)

	mgr := NewManager(nil)
	want := desktopPlatformDisabledReason(runtime.GOOS)
	if info := waitForPlatformCheck(t, mgr, csgstudioAppID); info.DisabledReason != want {
		t.Fatalf("first check reason = %q, want %q", info.DisabledReason, want)
	}

	fail.Store(true)
	mgr.mu.Lock()
	mgr.platformChecks[csgstudioAppID].expiresAt = time.Time{}
	mgr.mu.Unlock()
	info := waitForPlatformCheck(t, mgr, csgstudioAppID)
	if !info.Disabled || info.DisabledReason != want {
		t.Fatalf("after failed recheck = disabled:%v reason:%q, want disabled with %q", info.Disabled, info.DisabledReason, want)
	}
}

func TestDetectCSGStudioFromManagedLaunchTarget(t *testing.T) {
	home := setTempHome(t)
	target := writeManagedCSGStudio(t, home)

	installPath, version, ok := detectCSGStudioInstall()
	if !ok || installPath != target || version != "0.2.0" {
		t.Fatalf("detectCSGStudioInstall() = (%q, %q, %v), want (%q, 0.2.0, true)", installPath, version, ok, target)
	}
	if !looksLikeCSGStudioInstall(installPath) {
		t.Fatal("managed CSGStudio target should be reported as managed")
	}
	if got, err := CSGStudioLaunchTarget(); err != nil || got != target {
		t.Fatalf("CSGStudioLaunchTarget() = (%q, %v), want %q", got, err, target)
	}
}

func TestDetectCSGStudioManagedBundleInApplications(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS app bundle install")
	}
	home := setTempHome(t)
	bundle := filepath.Join(home, "Applications", csgstudioAppBundleName)
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatalf("mkdir app bundle: %v", err)
	}
	root := csgstudioRuntimeRoot(home)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir runtime root: %v", err)
	}
	for name, value := range map[string]string{"launch-target": bundle, "managed-bundle": bundle, "version": "0.2.0"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	installPath, version, ok := detectCSGStudioInstall()
	if !ok || installPath != bundle || version != "0.2.0" {
		t.Fatalf("detectCSGStudioInstall() = (%q, %q, %v), want (%q, 0.2.0, true)", installPath, version, ok, bundle)
	}
	if !looksLikeCSGStudioInstall(installPath) {
		t.Fatal("bundle recorded in managed-bundle should be reported as managed")
	}

	// A manual path override elsewhere must not inherit the managed flag.
	other := filepath.Join(home, "Other", csgstudioAppBundleName)
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("mkdir other bundle: %v", err)
	}
	if err := SetCSGStudioLaunchTarget(other); err != nil {
		t.Fatalf("SetCSGStudioLaunchTarget: %v", err)
	}
	if looksLikeCSGStudioInstall(other) {
		t.Fatal("manual CSGStudio path must not be reported as managed")
	}
}

func writeManagedCSGStudio(t *testing.T, home string) string {
	t.Helper()
	root := csgstudioRuntimeRoot(home)
	target := filepath.Join(root, "versions", "0.2.0", csgstudioAppBundleName)
	if runtime.GOOS != "darwin" {
		target = filepath.Join(root, "versions", "0.2.0", "CSGStudio"+map[bool]string{true: ".exe", false: ".AppImage"}[runtime.GOOS == "windows"])
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir target dir: %v", err)
		}
		if err := os.WriteFile(target, []byte("stub"), 0o755); err != nil {
			t.Fatalf("write target: %v", err)
		}
	} else if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir app bundle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "launch-target"), []byte(target+"\n"), 0o644); err != nil {
		t.Fatalf("write launch target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "version"), []byte("0.2.0\n"), 0o644); err != nil {
		t.Fatalf("write version: %v", err)
	}
	return target
}

func TestDetectCSGStudioFromMacApplications(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS app bundle detection")
	}
	home := setTempHome(t)
	bundle := filepath.Join(home, "Applications", csgstudioAppBundleName)
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatalf("mkdir app bundle: %v", err)
	}

	installPath, _, ok := detectCSGStudioInstall()
	if !ok || installPath != bundle {
		t.Fatalf("detectCSGStudioInstall() = (%q, %v), want %q", installPath, ok, bundle)
	}
	if looksLikeCSGStudioInstall(installPath) {
		t.Fatal("external CSGStudio install must not be reported as managed")
	}
}

func waitForPlatformCheck(t *testing.T, mgr *Manager, appID string) api.AIAppInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := mgr.Get(context.Background(), appID); err != nil {
			t.Fatalf("Get(%s): %v", appID, err)
		}
		mgr.mu.RLock()
		check := mgr.platformChecks[appID]
		done := check != nil && !check.running && !check.expiresAt.IsZero()
		mgr.mu.RUnlock()
		if done {
			// Re-read so the state reflects the finished check.
			got, err := mgr.Get(context.Background(), appID)
			if err != nil {
				t.Fatalf("Get(%s): %v", appID, err)
			}
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s platform check did not settle", appID)
	return api.AIAppInfo{}
}

func hostManifestPlatform() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return runtime.GOOS
}

func hostManifestArch() string {
	if runtime.GOARCH == "amd64" {
		return "x86_64"
	}
	return runtime.GOARCH
}

func hostPackageExt() string {
	switch runtime.GOOS {
	case "darwin":
		return ".dmg"
	case "windows":
		return ".exe"
	default:
		return ".AppImage"
	}
}
