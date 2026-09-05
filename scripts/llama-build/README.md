# llama.cpp Ubuntu 22.04 CUDA mirror builds

Scripts to build `llama-<tag>-bin-ubuntu-cuda-{x64,arm64}.tar.gz` on **Ubuntu 22.04**
inside Docker and upload to GitLab generic packages.

Canonical rules: [`docs/agent-guidelines/llama-cpp.md`](../../docs/agent-guidelines/llama-cpp.md).

## Policy (read first)

| Artifact | Source |
|----------|--------|
| Converter, CPU/macOS/Windows assets upstream publishes | **Official** `ggml-org/llama.cpp` GitHub releases → mirror to GitLab |
| **Ubuntu Linux CUDA** (`*-ubuntu-cuda-x64.tar.gz`, `*-ubuntu-cuda-arm64.tar.gz`) | **This directory** — Docker build on 22.04, then upload to GitLab |

Do **not** sync Ubuntu CUDA binaries from `hybridgroup/llama-cpp-builder`; use it
only to compare tar layout if needed.

## Prerequisites

- Docker Desktop (engine running). On Apple Silicon, pull **both** platforms of the
  pinned image (scripts do this automatically when missing).
- ~30GB free disk for images + two build trees.
- `local/secrets.env` with `GITLAB_TOKEN` for uploads (`unset` proxy before GitLab).
- GitHub clone: `source ~/.myshrc` if you need a proxy for `git clone`.

**Do not** run `docker system prune -a` or delete `Docker.raw` to save space unless
you intend to re-pull images.

## Quick start

```sh
# From repo root; default tag b10549
make llama-cuda-rebuild-all

# Or explicitly:
./scripts/llama-build/rebuild-upload-all.sh b10549

# One architecture only:
./scripts/llama-build/rebuild-upload-x64.sh b10549
./scripts/llama-build/rebuild-upload-arm64.sh b10549
```

Artifacts: `scripts/llama-build/work/out/*.tar.gz` (gitignored).

Compare with GitLab before/after upload:

```sh
./scripts/llama-build/compare-with-gitlab.sh x64 b10549
./scripts/llama-build/compare-with-gitlab.sh arm64 b10549
```

## Pinned Docker image

| Image | Platforms |
|-------|-----------|
| `nvidia/cuda:12.9.1-devel-ubuntu22.04` | `linux/amd64`, `linux/arm64` |

Scripts reuse local images (`docker image inspect` / `--pull=never`).

## Package layout (b10549 reference)

| Arch | Tarball | Root layout |
|------|---------|-------------|
| x64 | `llama-<tag>-bin-ubuntu-cuda-x64.tar.gz` | `bin/llama-server` + `lib/*.so*` at tar root |
| arm64 | `llama-<tag>-bin-ubuntu-cuda-arm64.tar.gz` | `llama-<tag>-bin-ubuntu-cuda-arm64/{bin,lib}/` with five CLI tools |

Upload URL pattern:

`https://git-devops.opencsg.com/api/v4/projects/393/packages/generic/llama-cpp/<tag>/<filename>`

## Build knobs

| Setting | x64 | arm64 |
|---------|-----|-------|
| `CMAKE_CUDA_ARCHITECTURES` | `80;86;89` (override via env) | `87` (Jetson-class default) |
| Toolchain | Ubuntu 22.04 GCC 11 | GCC **14** (PPA) + bundled `libstdc++.so.6*` |
| CMake | `GGML_CUDA=ON`, `GGML_BACKEND_DL=ON`, `GGML_CPU_ALL_VARIANTS=ON` | same |

Override CUDA arch for a one-off build:

```sh
docker run ... -e CMAKE_CUDA_ARCHITECTURES="80;86;89" ...
```

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `image does not provide platform linux/amd64` | `docker pull --platform linux/amd64 nvidia/cuda:12.9.1-devel-ubuntu22.04` |
| arm64: `unknown value 'armv9.2-a' for -march` | Use GCC 14 (already in `build-ubuntu22-cuda-arm64.sh`) |
| arm64: `GLIBCXX_3.4.32 not found` on 22.04 | Ensure `libstdc++.so.6*` is copied into package `lib/` |
| x64: `GLIBC_2.38 not found` on 22.04 | Rebuild in 22.04 image, not 24.04 |
| `libggml-base.so.0: cannot open shared object` | Pack with `cp -a build/bin/lib*.so*` (keeps symlinks) |
| apt fails in arm64 container | Do not pass host `http_proxy` into the container |
| Apple Silicon amd64 build very slow | Expected (QEMU); arm64 build is native |

## Files

| File | Role |
|------|------|
| `common.sh` | Docker/GitLab helpers |
| `build-ubuntu22-cuda-*.sh` | In-container compile + pack |
| `rebuild-upload-*.sh` | Host driver: clone, docker run, verify, upload |
| `compare-with-gitlab.sh` | Diff layout + runtime vs remote package |
