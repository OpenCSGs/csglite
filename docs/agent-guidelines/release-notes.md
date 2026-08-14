# Commit, PR, And Release Notes

## Commit, PR, And Release Text

- For every commit, PR, and release note, describe the specific new feature, fix,
  or behavior change in this work.
- When a commit fixes or relates to a GitHub issue, reference the issue in the
  commit message (for example `Fixes #60.`) so GitHub links the commit to the
  issue automatically.
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

## Release Workflow

- Pushing a `v*` tag to GitHub triggers `.github/workflows/release.yml`.
- The workflow tests the tagged commit, builds the Web UI and all supported
  release archives with `make package`, verifies `dist/checksums.txt`, and
  creates the GitHub release.
- After GitHub publication, the `sync-gitlab` job uses the `gitlab-sync`
  environment to push the tag and publish the same archives and notes to the
  GitLab release.
- The workflow generates initial bullet notes from non-merge commits since the
  previous tag. Review them after publication and replace broad commit lists
  with 1-3 concrete user-facing bullets when needed.
- Always build the Web UI before packaging so release binaries embed
  `internal/server/static` instead of falling back to a missing local `web/dist`.
- When the user asks to "release", "publish a version", or "发版":
  1. Use the requested tag, or increment the latest stable tag by one patch
     version when no version is specified.
  2. Require a clean worktree and ensure the tagged commit is already on
     `origin/main`. Do not include or commit unrelated local changes.
  3. Create the tag locally and push that tag only to `origin` (GitHub).
     The workflow is responsible for creating both releases and syncing the tag
     to GitLab.
  4. Monitor the `Release` workflow through the `sync-gitlab` job, then verify
     that GitHub and GitLab each contain the expected release assets.
- Do not manually build archives, create a GitHub Release, upload assets, or
  push the release tag to GitLab during the normal flow.
- For a manual fallback, build packages from the target tag in a clean checkout
  or temporary worktree with `make package`, then use `gh release create`,
  `gh release upload`, or `scripts/push.sh --skip-build` only when the automated
  workflow cannot be repaired or rerun.
- Follow repository network rules during release work:
  - GitLab and other internal services: direct connection, no proxy.
  - GitHub and other external services: `source ~/.myshrc` before upload
    commands.
