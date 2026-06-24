# Agent Guidelines

These files are the canonical source for persistent coding-agent guidance in
this repository. They are written for any coding agent, not only Cursor.

## Maintenance

- Add or update shared rules here first.
- Keep tool-specific files as small adapters that point back to these files.
- If a rule is critical to correctness or releases, prefer adding an automated
  check in tests, scripts, CI, or a Makefile target.
- Keep each guideline focused on one concern and avoid duplicating the same
  rule in multiple places.

## Files

- `api-swagger-sync.md` - local API and OpenAPI sync requirements.
- `app-installs.md` - user-scoped AI app installer behavior.
- `ai-app-oss-mirror.md` - AI app OSS mirror workflow (claude-code, open-code, codex).
- `cross-platform.md` - macOS, Linux, and Windows compatibility rules.
- `frontend-i18n.md` - web UI localization rules.
- `go-conventions.md` - Go project structure and coding conventions.
- `llama-cpp.md` - llama.cpp version lockstep and CUDA mirror rules
  (`scripts/llama-build/` for Docker rebuilds).
- `local-preview.md` - local preview port and redeploy workflow.
- `model-source-routing.md` - preserve local/cloud model source across inference paths.
- `network-and-secrets.md` - proxy, GitLab, and local secret handling.
- `python.md` - Python 3.9+ compatibility for Python code and dependencies.
- `release-notes.md` - commit, PR, and release note conventions.
- `storage.md` - storage root and temporary file placement rules.
