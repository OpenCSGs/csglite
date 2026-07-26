# Cross-Platform Compatibility

csghub-lite must run on macOS, Linux, and Windows. Every code change must
consider all three platforms.

## Go And Scripts

- Changes in Go, shell, or PowerShell must work on macOS, Linux, and Windows.
- Never hardcode path separators, home directories, or binary suffixes; use
  `filepath.Join()`, `os.UserHomeDir()`, `exec.LookPath()`, `runtime.GOOS`, and
  `os.PathListSeparator`.
- `exec.LookPath()` alone is not sufficient for third-party tools used by the
  desktop GUI. Finder and Windows Explorer may launch `csglite-client` with a
  minimal `PATH`; resolve standard application install locations or support an
  explicit path override, then execute the resolved absolute path.
- Do not assume `bash`, `sudo`, `/usr/local/bin`, or Unix-only commands exist on
  Windows.
- If install or uninstall behavior changes, update both `scripts/install.sh`
  and `scripts/install.ps1`.
- If the UI or docs show install commands, either provide platform-specific
  variants or clearly label them as previews or OS-specific examples.

## Paths

- Always use `filepath.Join()`, never hardcode `/` or `\` separators.
- Use `os.UserHomeDir()` instead of hardcoding `$HOME` or `%USERPROFILE%`.

```go
// Bad
path := home + "/.csghub-lite/models"

// Good
path := filepath.Join(home, ".csghub-lite", "models")
```

## Binary And Library Names

- Binaries: `llama-server` on Unix, `llama-server.exe` on Windows. Use
  `exec.LookPath()` which handles this automatically.
- Shared libraries differ per platform:
  - macOS: `.dylib`
  - Linux: `.so`, `.so.*`
  - Windows: `.dll`
- When scanning for libraries, check all three suffix families using
  `runtime.GOOS`.

## Process And Environment

- `sudo` does not exist on Windows. Use `runtime.GOOS` to branch:
  - Unix: `sudo` for privilege escalation.
  - Windows: `cmd /C` or prompt the user to run as Administrator.
- Signal handling differs on Windows; check `syscall.SIGTERM` and
  `syscall.SIGINT` use carefully.
- Library search paths differ:
  - macOS: `DYLD_LIBRARY_PATH`
  - Linux: `LD_LIBRARY_PATH`
  - Windows: `PATH`
- Use `os.PathListSeparator` instead of hardcoding `:` or `;`.

## Desktop Client

- Any macOS or Windows platform feature must be tested through
  `csglite-client`, not only through a terminal-launched `csghub-lite`.
- Account for the desktop sidecar's authenticated dynamic loopback port. Before
  adding containers, subprocesses, or external apps, verify that they can reach
  the API without weakening loopback binding or desktop authentication.
- Browser behavior is not automatically equivalent in Tauri WebView. Route
  external links through the system browser and verify new-window, clipboard,
  file-selection, and download behavior on both macOS and Windows.
- Hide or replace server-only controls in desktop mode. Core self-upgrade is
  disabled there; desktop releases must use the signed Tauri updater.
- Coordinate desktop protocol or runtime changes with `csglite-client`, update
  its pinned csglite commit, and test first launch, restart, shutdown, and update.

## Default Install Locations

| Platform | csghub-lite | llama-server |
| --- | --- | --- |
| macOS | `/usr/local/bin` | same dir or `/opt/homebrew/bin` |
| Linux | `/usr/local/bin` | same dir |
| Windows | `$HOME\bin` | same dir |

## Testing Checklist

When modifying platform-sensitive code, verify:

1. Path construction uses `filepath.Join()`.
2. Binary/library lookups cover all three OS variants.
3. Permission escalation branches on `runtime.GOOS`.
4. Environment variable names are platform-appropriate.
5. Install script changes are reflected in both `.sh` and `.ps1`.
6. Finder/Explorer launches can resolve required third-party executables.
7. Desktop loopback, WebView, sidecar lifecycle, and updater behavior remain valid.
