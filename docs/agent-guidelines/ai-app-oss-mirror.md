# AI App OSS Mirror

This document covers mirroring AI app releases to the StarHub OSS bucket.

## Supported Apps

| App | Source | Sync Script |
|-----|--------|-------------|
| claude-code | Anthropic GCS | `scripts/sync-claude-code-oss.sh` |
| open-code | GitHub: anomalyco/opencode | `scripts/sync-ai-app-oss.sh --app open-code` |
| codex | GitHub: openai/codex | `scripts/sync-ai-app-oss.sh --app codex` |
| antigravity | Google Antigravity platform manifests | `scripts/sync-ai-app-oss.sh --app antigravity` |

## Sync Workflow

1. **Check for updates first**: Before syncing, compare the upstream version with
   the mirrored `latest` to avoid unnecessary downloads.

   ```bash
   # Check Claude Code upstream version
   curl -fsSL https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases/latest
   
   # Check mirrored version
   curl -fsSL https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/claude-code-releases/latest
   
   # Check GitHub release version (for open-code, codex)
   gh release view --repo anomalyco/opencode --json tagName --jq '.tagName'
   gh release view --repo openai/codex --json tagName --jq '.tagName'

   # Check Antigravity CLI upstream version
   curl -fsSL https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/darwin_arm64.json
   ```

2. **Sync apps individually**: Sync one app at a time to avoid timeout issues.
   Each app download can take several minutes.

   ```bash
   source ~/.myshrc  # Load proxy for upstream downloads
   
   # Sync individual apps
   ./scripts/sync-ai-app-oss.sh --app claude-code
   ./scripts/sync-ai-app-oss.sh --app open-code
   ./scripts/sync-ai-app-oss.sh --app codex
   ./scripts/sync-ai-app-oss.sh --app antigravity
   ```

3. **Skip if already synced**: If the mirrored `latest` matches the upstream
   version, skip that app to save time.

## Credentials and Proxy

- Load OSS credentials only from `local/secrets.env` and keep them local-only.
  Use the `STARHUB_OSS_*` and `STARHUB_CLAUDE_*` variables already defined
  there.
- Use proxy only for upstream external downloads (Anthropic/GCS, GitHub).
  Upload to Alibaba OSS directly without proxy; sync scripts handle this split.
- Run `source ~/.myshrc` before syncing to enable proxy for external downloads.

## Mirror Layout

Each app follows this versioned layout:
```
<app>-releases/latest
<app>-releases/<version>/manifest.json
<app>-releases/<version>/<platform>/<binary>
```

## Platform Coverage

- **claude-code**: Mirror all platforms from upstream manifest (darwin-arm64,
  darwin-x64, linux-arm64, linux-arm64-musl, linux-x64, linux-x64-musl,
  win32-arm64, win32-x64)
- **open-code**: darwin-arm64, darwin-x64, linux-arm64, linux-arm64-musl,
  linux-x64, linux-x64-musl, win32-arm64, win32-x64
- **codex**: darwin-arm64, darwin-x64, linux-arm64, linux-x64, win32-arm64,
  win32-x64 (uses musl builds for Linux)
- **antigravity**: darwin-arm64, darwin-x64, linux-arm64, linux-x64,
  win32-arm64, win32-x64

## Safety Rules

- Do not update `latest` until all artifacts for that version, plus the
  rewritten `manifest.json`, have uploaded successfully.
- Keep the mirrored `manifest.json` compatible with the install scripts in
  `internal/apps/scripts/<app>-install.sh` and `internal/apps/scripts/<app>-install.ps1`.
- If multiple Python versions are installed, run the sync script with a Python
  that can import `oss2`.

## Updating Asset Names

When upstream changes asset naming conventions (e.g., codex renamed from
`codex-aarch64-unknown-linux-gnu.tar.gz` to `codex-aarch64-unknown-linux-musl.tar.gz`):

1. Update `scripts/sync-ai-app-oss.sh` asset configuration for that app
2. Update corresponding install scripts in `internal/apps/scripts/`
3. Test the sync with `--app <name>` before committing
