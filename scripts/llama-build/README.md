# llama.cpp Ubuntu 22.04 CUDA/ROCm mirror builds

Scripts to build `llama-<tag>-bin-ubuntu-cuda-{x64,arm64}.tar.gz` and
`llama-<tag>-bin-ubuntu-rocm-<series>-x64.tar.gz` on **Ubuntu 22.04** inside
Docker and upload to GitLab generic packages.

Canonical rules: [`docs/agent-guidelines/llama-cpp.md`](../../docs/agent-guidelines/llama-cpp.md).

## Policy (read first)

| Artifact | Source |
|----------|--------|
| Converter, CPU/macOS/Windows assets upstream publishes | **Official** `ggml-org/llama.cpp` GitHub releases → mirror to GitLab |
| **Ubuntu Linux CUDA** (`*-ubuntu-cuda-x64.tar.gz`, `*-ubuntu-cuda-arm64.tar.gz`) | **This directory** — Docker build on 22.04, then upload to GitLab |
| **Ubuntu Linux ROCm** (`*-ubuntu-rocm-<series>-x64.tar.gz`) | **This directory** — Docker build on 22.04, then upload to GitLab |

Do **not** sync Ubuntu CUDA binaries from `hybridgroup/llama-cpp-builder`; use it
only to compare tar layout if needed.

Do **not** mirror upstream's `llama-<tag>-bin-ubuntu-rocm-*.tar.gz` either.
Upstream started publishing a Linux ROCm asset at b10830, but its release
workflow builds that job on `ubuntu-24.04`, so the result needs `GLIBC_2.38` /
`GLIBCXX_3.4.32` and cannot start on the 22.04 ROCm runtime image in
`docker/rocm/Dockerfile`. The ROCm series in the filename must also match that
runtime image (currently 7.2), not whatever series upstream happens to ship.

## Prerequisites

- Docker Desktop (engine running). On Apple Silicon, pull **both** platforms of the
  pinned image (scripts do this automatically when missing).
- ~30GB free disk for images + two CUDA build trees. A ROCm build needs
  considerably more: the `rocm/dev-ubuntu-22.04` image alone is ~7.3GB, and
  `libggml-hip.so` carries device code for every requested `gfx` target (~590MB
  for the default ten), so budget ~60GB for its tree.
- `local/secrets.env` with `GITLAB_TOKEN` for uploads (`unset` proxy before GitLab).
- GitHub clone: `source ~/.myshrc` if you need a proxy for `git clone`.

**Do not** run `docker system prune -a` or delete `Docker.raw` to save space unless
you intend to re-pull images.

## Quick start

```sh
# From repo root; default tag b10830
make llama-cuda-rebuild-all

# Or explicitly:
./scripts/llama-build/rebuild-upload-all.sh b10830

# One architecture only:
./scripts/llama-build/rebuild-upload-x64.sh b10830
./scripts/llama-build/rebuild-upload-arm64.sh b10830

# ROCm x64 (separate target; not part of rebuild-upload-all.sh)
make llama-rocm-rebuild-x64
./scripts/llama-build/rebuild-upload-rocm-x64.sh b10830

# Build and verify without publishing
LLAMA_BUILD_SKIP_UPLOAD=1 ./scripts/llama-build/rebuild-upload-rocm-x64.sh b10830
```

Artifacts: `scripts/llama-build/work/out/*.tar.gz` (gitignored).

Compare with GitLab before/after upload:

```sh
./scripts/llama-build/compare-with-gitlab.sh x64 b10830
./scripts/llama-build/compare-with-gitlab.sh arm64 b10830
./scripts/llama-build/compare-with-gitlab.sh rocm-x64 b10830
```

## Pinned Docker images

| Image | Platforms | Used for |
|-------|-----------|----------|
| `nvidia/cuda:12.9.1-devel-ubuntu22.04` | `linux/amd64`, `linux/arm64` | CUDA build + verify |
| `rocm/dev-ubuntu-22.04:7.2.2-complete` | `linux/amd64` | ROCm build + verify |
| `ubuntu:22.04` | `linux/amd64` | ROCm glibc-baseline check |

Scripts reuse local images (`docker image inspect` / `--pull=never`). Override with
`LLAMA_BUILD_CUDA_IMAGE`, `LLAMA_BUILD_ROCM_IMAGE`, `LLAMA_BUILD_UBUNTU_IMAGE`.

Keep `LLAMA_BUILD_ROCM_IMAGE` and `LLAMA_BUILD_ROCM_SERIES` in lockstep with the
base image in `docker/rocm/Dockerfile`; the series string ends up in the package
filename that `scripts/install.sh` looks for.

## Package layout (b10830 reference)

| Arch | Tarball | Root layout |
|------|---------|-------------|
| x64 | `llama-<tag>-bin-ubuntu-cuda-x64.tar.gz` | `bin/llama-server` + `lib/*.so*` at tar root |
| arm64 | `llama-<tag>-bin-ubuntu-cuda-arm64.tar.gz` | `llama-<tag>-bin-ubuntu-cuda-arm64/{bin,lib}/` with five CLI tools |
| rocm x64 | `llama-<tag>-bin-ubuntu-rocm-<series>-x64.tar.gz` | `bin/llama-server` + `lib/*.so*` at tar root |

`scripts/install.sh` flattens whichever layout it downloads into a single
directory, and `ggml_backend_load_all` searches the executable's own directory,
so backend `.so` files still load after the flattening.

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

### ROCm x64

| Setting | Value |
|---------|-------|
| `GPU_TARGETS` | `gfx908;gfx942;gfx1030;gfx1100;gfx1101;gfx1102;gfx1150;gfx1151;gfx1200;gfx1201` |
| Toolchain | ROCm 7.2.2 clang from the dev image (`CMAKE_HIP_COMPILER=$(hipconfig -l)/clang`) |
| CMake | 3.31.6, **not** 4.x — CMake 4 removed `cmake_minimum_required(<3.5)` support that ROCm config packages still declare |
| CMake flags | `GGML_HIP=ON`, `HIP_PLATFORM=amd`, `GGML_BACKEND_DL=ON`, `GGML_CPU_ALL_VARIANTS=ON`, `GGML_NATIVE=OFF` |

The default `GPU_TARGETS` reproduces the coverage of the last mirrored ROCm
package (b9158, rocm-7.2). Build time scales with this list, so narrow it when
you only need one card:

```sh
GPU_TARGETS="gfx1100" ./scripts/llama-build/rebuild-upload-rocm-x64.sh b10830
```

Read the target off the host with `rocminfo | grep gfx`.

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `image does not provide platform linux/amd64` | `docker pull --platform linux/amd64 nvidia/cuda:12.9.1-devel-ubuntu22.04` |
| arm64: `unknown value 'armv9.2-a' for -march` | Use GCC 14 (already in `build-ubuntu22-cuda-arm64.sh`) |
| arm64: `GLIBCXX_3.4.32 not found` on 22.04 | Ensure `libstdc++.so.6*` is copied into package `lib/` |
| x64: `GLIBC_2.38 not found` on 22.04 | Rebuild in 22.04 image, not 24.04 |
| `libggml-base.so.0: cannot open shared object` | Pack with `cp -a build/bin/lib*.so*` (keeps symlinks) |
| apt fails in arm64 container | Do not pass host `http_proxy` into the container |
| Apple Silicon amd64 build very slow | Expected (QEMU); arm64 build is native. A multi-target ROCm build is impractical this way — use an x86_64 Linux builder or narrow `GPU_TARGETS` |
| ROCm: `Compatibility with CMake < 3.5 has been removed` | A CMake 4.x leaked into the container; the script pins 3.31.6 via `CMAKE_VERSION` |
| ROCm: `--version` exits non-zero in the container | Expected without `/dev/kfd`; the `ldd` and glibc-baseline checks are the gates. Validate for real on an AMD GPU host |
| ROCm: installed but `no usable GPU found` on the host | Series mismatch — the package's `libggml-hip.so` needs the host's `libamdhip64`/`librocblas`/`libhipblas` sonames |

## Files

| File | Role |
|------|------|
| `common.sh` | Docker/GitLab helpers |
| `build-ubuntu22-cuda-*.sh` | In-container CUDA compile + pack |
| `build-ubuntu22-rocm-x64.sh` | In-container ROCm compile + pack |
| `rebuild-upload-*.sh` | Host driver: clone, docker run, verify, upload |
| `compare-with-gitlab.sh` | Diff layout + runtime vs remote package |
