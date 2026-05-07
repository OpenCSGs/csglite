# Commit, PR, And Release Notes

## Commit, PR, And Release Text

- For every commit, PR, and release note, describe the specific new feature, fix,
  or behavior change in this work.
- Do not use `Full Changelog` style summaries by default.
- GitHub release notes must include explicit user-facing feature and fix
  bullets. Do not publish a GitHub release that only contains auto-generated
  changelog text, commit lists, or a `Full Changelog` link.
- Do not dump broad commit inventories when the user wants a concise release
  summary.
- Prefer 1-3 concrete bullets that explain what was added, fixed, or changed for
  users.
- If there are multiple unrelated changes, group them by user-facing outcome
  instead of listing every touched file.
- If uncertain, ask which newly added or fixed behavior should be highlighted
  before drafting final release text.

## Manual Local Release

- Do not rely on GitHub Actions tag workflows to publish releases for this
  repository.
- Preferred release flow: push code and tag first, build release archives
  locally, then upload artifacts directly.
- Build release packages from the target tag in a clean checkout or temporary
  worktree with `make package`.
- Always build the web UI before packaging so release binaries embed
  `internal/server/static` instead of falling back to a missing local `web/dist`.
- Publish GitHub releases manually with `gh release create` or
  `gh release upload` after local packaging.
- Publish GitLab release assets manually or via `scripts/push.sh --skip-build`
  after local packaging is complete.
- Follow repository network rules during release work:
  - GitLab and other internal services: direct connection, no proxy.
  - GitHub and other external services: `source ~/.myshrc` before upload
    commands.
