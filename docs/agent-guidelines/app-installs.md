# User-Scope App Installs

## Installer Scope

- AI app installers should default to the current user's writable directories,
  such as `~/.local/bin`, `~/.local/share/<app>`, or platform equivalents.
- Do not use system-wide install locations or commands that normally require
  root/Admin privileges unless the app cannot work without them.
- Do not require users to run `sudo csghub-lite ...` for AI app installation.
  Running as root changes HOME/config/server ownership and can break the normal
  user workflow.
- If elevated permissions are unavoidable, explain why in code comments or
  user-facing logs, and keep the privileged step as narrow as possible.
- For npm-based apps, prefer a user-owned `--prefix` plus a launcher in the
  user's bin directory over `npm install -g` to the default global prefix.
- Update uninstallers to remove only the user-owned files that the installer
  created.

## Install Detection

csghub-lite must detect apps that users installed **outside** the managed
installer flow. Detection powers the AI Apps page status, update checks, and
the install short-circuit that avoids re-downloading an app that is already
present.

### Managed vs External

- `installed=true` means the app can be launched from this machine.
- `managed=true` means csghub-lite's installer created the runtime files and
  uninstaller may remove them.
- External installs (`installed=true`, `managed=false`) must still be detected,
  but install/uninstall actions must not delete files the user installed
  elsewhere.

### Source Of Truth

- Detection profiles live in `internal/apps/detect.go` as
  `installDetectProfiles`.
- Desktop launch resolution for Codex App lives in `internal/apps/codex_app.go`.
- Every supported script-based app must have a profile entry. The test
  `TestInstallDetectProfilesCoverSupportedApps` enforces this.

### CLI Apps

CLI apps use `installDetectMode=cli`. Detection order:

1. `exec.LookPath(binaryName)` using the server process `PATH`.
2. Common user/system bin directories:
   - `~/bin`
   - `~/.local/bin`
   - `/opt/homebrew/bin`
   - `/usr/local/bin`
   - Windows: `%APPDATA%/npm`
3. App-specific runtime fallbacks declared in `installDetectProfiles`:
   - `versionedShare`: `~/.local/share/<dir>/versions/*/<binary>`
   - `shareBinRel`: `~/.local/share/<path>/<binary>`
   - `libBundleName`: `~/.local/lib/<name>/*/<name>/bin/<binary>`

Current CLI apps:

| App ID | Binary | Runtime fallback |
|--------|--------|------------------|
| `claude-code` | `claude` | `~/.local/share/claude/versions/*` |
| `open-code` | `opencode` | `~/.local/share/opencode/versions/*` |
| `open-code-review` | `ocr` | `~/.local/share/open-code-review/versions/*` |
| `codex` | `codex` | `~/.local/share/codex/versions/*` |
| `openclaw` | `openclaw` | PATH/common bins only |
| `csgclaw` | `csgclaw` | `~/.local/lib/csgclaw/*/<bundle>/bin` |
| `pi` | `pi` | `~/.local/share/pi-coding-agent/bin` |

### Desktop Apps

Desktop apps use `installDetectMode=desktop`. Codex App detection order:

1. Managed launcher in `~/.local/bin`:
   - macOS/Linux: `codex-app`
   - Windows: `codex-app.cmd`, then `codex-app.exe`
2. Managed `~/.local/share/codex-app/launch-target` when the target still
   exists.
3. macOS app bundles:
   - `~/Applications/Codex.app`
   - `/Applications/Codex.app`
4. Windows managed runtime exe under
   `~/.local/share/codex-app/versions/*/*.exe` when launcher metadata is
   missing.
5. Windows external installs:
   - Registry Uninstall entries (`HKCU`/`HKLM`, including `WOW6432Node`) whose
     `DisplayName` contains "codex", resolved via `DisplayIcon` or
     `InstallLocation`.
   - Common install directories: `%LOCALAPPDATA%\Programs\Codex`,
     `%LOCALAPPDATA%\Programs\codex-app`, `%LOCALAPPDATA%\Codex`,
     `%ProgramFiles%\Codex`, `%ProgramFiles(x86)%\Codex`.

`CodexAppLaunchTarget()` uses the same resolution order for the Open action.

Manual override: `POST /api/apps/path` (UI: "Specify Install Location" in the
Codex App drawer) writes the user-provided path to
`~/.local/share/codex-app/launch-target`, which sits near the top of the
detection order. Manual paths stay `managed=false`.

### Adding A New App

When adding a script-based AI app:

1. Add the app spec in `internal/apps/manager.go`.
2. Add a matching `installDetectProfiles` entry in `internal/apps/detect.go`.
3. If the app is desktop-style rather than CLI, implement detection/launch in a
   dedicated helper file (follow `codex_app.go`).
4. Add tests in `internal/apps/detect_test.go` and/or `codex_app_test.go` that
   cover:
   - managed install detection
   - at least one realistic external install location
   - launch/open path resolution when behavior differs from install detection
5. Update the tables in this document.
6. Keep installer scripts writing the same metadata paths that detection expects
   (`launch-target`, `version`, launcher names, runtime dirs).

Do not rely only on the managed launcher existing. Users commonly install via
Homebrew, vendor installers, drag-and-drop, or manual PATH setup.

### Tests To Run

```bash
go test ./internal/apps/...
go test ./internal/server/... -run 'CodexApp|AppOpen'
```
