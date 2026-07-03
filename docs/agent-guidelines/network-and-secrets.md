# Network And Secrets

The dev environment requires proxy for external sites but direct access for
internal sites.

## Proxy Rules

- GitHub, PyPI, and other external sites: run `source ~/.myshrc` first to enable
  proxy.
- GitLab at `git-devops.opencsg.com` and other internal sites: use direct
  connection. If proxy was previously enabled, run `unset https_proxy` before
  accessing.

## Git Push Workflow

This repo has two remotes: `gitlab` (internal) and `origin` (GitHub). When the
user asks to push or says "commit and push", push to **both** remotes:

1. Push to GitLab first, no proxy needed: `git push gitlab main`.
2. Push to GitHub second, with proxy: `source ~/.myshrc && git push origin main`.

Do not push to only one remote unless the user explicitly requests it.

## Download And Upload Workflow

- Downloading from GitHub releases: `source ~/.myshrc` before `curl` or `wget`.
- Uploading to GitLab: `unset https_proxy`, then use GitLab API.

## GitLab API Token

- When a task requires GitLab API authentication, always load the token from
  `local/secrets.env`.
- Preferred source: the main checkout's `local/secrets.env` with
  `GITLAB_TOKEN="glpat-..."`.
- If `GITLAB_TOKEN` is unset, source `local/secrets.env` before running GitLab
  API or release upload commands.
- When publishing from a clean release worktree or temporary checkout, source
  the main branch checkout's `local/secrets.env` explicitly before GitLab API or
  release upload commands.
- Never hardcode, paste, or commit GitLab tokens in commands, code, docs, commit
  messages, or chat output.
- Keep `local/secrets.env` local-only; it is gitignored.
- `scripts/push.sh` auto-sources `local/secrets.env` when `GITLAB_TOKEN` is
  unset.

```sh
if [ -z "${GITLAB_TOKEN:-}" ] && [ -f "./local/secrets.env" ]; then
  . "./local/secrets.env"
fi
```
