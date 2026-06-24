# Agent Instructions

This repository keeps agent guidance in a tool-neutral form. Treat this file as
the entry point for all coding agents, including Cursor, Codex, Claude Code,
Copilot, and Aider.

## Canonical Rule Source

- Full project rules live in `docs/agent-guidelines/`.
- Tool-specific files such as `.cursor/rules/*.mdc`, `CLAUDE.md`, and
  `.github/copilot-instructions.md` are adapters only.
- When adding or changing persistent rules, update `docs/agent-guidelines/`
  first, then update tool adapters only if their metadata or pointers need to
  change.
- Before working in an area covered by the Rule Index, read the relevant
  guideline file and follow it for the task.
- Prefer enforcing critical rules with tests, scripts, CI, or Makefile targets
  instead of relying only on agent memory.

## Must-Follow Rules

- Keep local HTTP API changes in sync with `openapi/local-api.json`; run
  `go test ./internal/server` after API doc changes.
- csghub-lite must work on macOS, Linux, and Windows. Use platform-aware path,
  binary, library, process, and environment handling.
- Do not commit or paste secrets. Load GitLab and OSS credentials only from
  `local/secrets.env`.
- Follow the repository network split: external sites such as GitHub need
  `source ~/.myshrc`; internal GitLab access should be direct.
- Use only `http://localhost:11435` for local preview. Do not keep extra
  frontend preview servers running.
- If frontend files change, build `web`, sync `web/dist` into
  `internal/server/static`, then restart the single backend preview.
- Store all runtime files, including temporary files and subprocess temp output,
  under the csghub-lite storage root, defaulting to `~/.csghub-lite`.
- Keep commit, PR, and release notes concise and focused on concrete user-facing
  changes.
- When syncing llama.cpp binaries: use official `ggml-org/llama.cpp` GitHub
  releases for upstream-published assets; build Ubuntu Linux CUDA packages only
  via `scripts/llama-build/` (see `docs/agent-guidelines/llama-cpp.md`).

## Rule Index

- `docs/agent-guidelines/api-swagger-sync.md`
- `docs/agent-guidelines/app-installs.md`
- `docs/agent-guidelines/ai-app-oss-mirror.md`
- `docs/agent-guidelines/cross-platform.md`
- `docs/agent-guidelines/frontend-i18n.md`
- `docs/agent-guidelines/go-conventions.md`
- `docs/agent-guidelines/llama-cpp.md`
- `docs/agent-guidelines/local-preview.md`
- `docs/agent-guidelines/model-source-routing.md`
- `docs/agent-guidelines/network-and-secrets.md`
- `docs/agent-guidelines/python.md`
- `docs/agent-guidelines/release-notes.md`
- `docs/agent-guidelines/storage.md`
