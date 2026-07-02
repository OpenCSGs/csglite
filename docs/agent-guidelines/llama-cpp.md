# llama.cpp Rules

## Version Lockstep

When syncing or changing the bundled `llama.cpp` converter version, keep these
aligned in the same task:

- `internal/convert/bundled_converter.go` `BundledConverterLLamacppRef`.
- User-facing `gguf-py` install/download hints that reference a llama.cpp tag.
- Installer defaults in `scripts/install.sh` and `scripts/install.ps1` for
  downloaded `llama-server`.
- Local inference architecture support tables:
  - `internal/convert/arch_llama.go` HF architecture to GGUF architecture
    mappings.
  - `internal/convert/arch_support.go` bundled `convert_hf_to_gguf.py`
    `@ModelBase.register(...)` coverage, including HF architecture to
    llama.cpp/GGUF runtime architecture mappings.
  - `internal/model/manifest.go` embedding, vision, and Diffusers pipeline
    detection lists.

Users should get a consistent llama.cpp version for:

- bundled `convert_hf_to_gguf.py`
- matching `gguf-py`
- downloaded `llama-server`

Do not sync only the converter and leave installer defaults on an older tag. If
an exact mirrored binary tag is unavailable, either mirror it as part of the task
or explicitly choose the fallback tag and update all three surfaces together.

When upgrading the bundled llama.cpp converter, inspect the new
`convert_hf_to_gguf.py` registry and update the Go mapping tables in the same
task. SafeTensors support depends on mapping HuggingFace architecture names
such as `Qwen2ForCausalLM` to the llama.cpp/GGUF runtime architecture such as
`qwen2`; do not rely on matching architecture names directly. Add or update
tests for newly supported architectures and local inference support decisions.

## Binary sync policy

When bumping the pinned llama.cpp tag, use **two paths**. Do not mix them.

### Official upstream (`ggml-org/llama.cpp`)

Use the official GitHub release tag for everything upstream publishes:

- **Bundled converter** — `scripts/sync-llama-converter.sh --tag <tag>` (source:
  `ggml-org/llama.cpp` at that tag).
- **Installer prebuilts** that exist on the official release page — macOS CPU,
  Ubuntu CPU x64/arm64, Windows CPU/CUDA where `ggml-org` ships tarballs/zip,
  etc. Download from
  `https://github.com/ggml-org/llama.cpp/releases/download/<tag>/…`, verify, then
  mirror the same filenames to GitLab generic packages (`llama-cpp/<tag>/`) and
  release assets when needed.

`scripts/install.sh` / `scripts/install.ps1` already prefer GitLab mirrors but
fall back to GitHub; keep GitLab filenames aligned with official release names.

### Ubuntu Linux CUDA — local Docker build only

Upstream **does not** publish Linux CUDA tarballs. **Do not** treat
`hybridgroup/llama-cpp-builder` (or any third-party CI image) as the sync source
for GitLab mirrors: those builds often target Ubuntu 24.04 and break on 22.04
(`GLIBC_2.38`, `GLIBCXX_3.4.32`).

For each new tag, after version lockstep is updated:

1. Build with `scripts/llama-build/` (Ubuntu 22.04, pinned CUDA devel image).
2. Verify on 22.04 (see below).
3. Upload `llama-<tag>-bin-ubuntu-cuda-x64.tar.gz` and
   `llama-<tag>-bin-ubuntu-cuda-arm64.tar.gz` to GitLab.

Use `hybridgroup/llama-cpp-builder` only as an optional **layout reference** (tar
paths, CUDA naming), not as a binary to re-upload.

## Ubuntu CUDA Mirror

Linux CUDA packages are **built locally**, then mirrored to GitLab generic
packages (project `393`, package `llama-cpp`) so `scripts/install.sh` can
download them on Ubuntu 22.04 hosts.

**Use the checked-in scripts:** [`scripts/llama-build/README.md`](../../scripts/llama-build/README.md)

```sh
make llama-cuda-rebuild-all LLAMA_TAG=b9158   # or ./scripts/llama-build/rebuild-upload-all.sh
```

### Why Ubuntu 22.04 in Docker

Packages built on Ubuntu **24.04** (or hybridgroup amd64 CI images) link against
`GLIBC_2.38` / `GLIBCXX_3.4.32` and fail on 22.04 with errors like
`version 'GLIBC_2.38' not found`. Always compile inside
`nvidia/cuda:12.9.1-devel-ubuntu22.04` for GitLab mirrors intended for 22.04
users.

### Local Docker images (reuse)

Keep build images on the machine and reuse them across tasks. **Do not** delete
`Docker.raw`, run `docker system prune -a`, or remove pinned images unless the
user explicitly asks to reclaim disk space.

| Image | Platforms |
|-------|-----------|
| `nvidia/cuda:12.9.1-devel-ubuntu22.04` | `linux/amd64` (x64), `linux/arm64` |

On Apple Silicon, pull **both** platforms (same tag, different manifests). Prefer
`docker image inspect --platform …` and `docker run --pull=never` when the image
is already present.

Workdirs (`scripts/llama-build/work/`) are gitignored; only scripts are committed.

### GitLab packages (b9158 reference)

| Arch | Filename | Tar layout |
|------|----------|------------|
| x64 | `llama-<tag>-bin-ubuntu-cuda-x64.tar.gz` | `bin/llama-server` + `lib/` at tar root |
| arm64 | `llama-<tag>-bin-ubuntu-cuda-arm64.tar.gz` | `llama-<tag>-bin-ubuntu-cuda-arm64/{bin,lib}/` |

`scripts/install.sh` also tries legacy names such as
`llama-<tag>-bin-ubuntu-cuda-12.4-<arch>.tar.gz`; new mirrors for b9158+ use the
names above (no `12.4` infix).

Older layout references for comparison only:

- `b8429/llama-b8429-bin-ubuntu-cuda-12.4-arm64.tar.gz`
- `b8429/llama-b8429-bin-ubuntu-cuda-12.4-x64.tar.gz`

### Build recipes (22.04 container)

**x64**

- `BUILD_SHARED_LIBS=ON`, `GGML_CUDA=ON`, `GGML_BACKEND_DL=ON`,
  `GGML_CPU_ALL_VARIANTS=ON`, `GGML_NATIVE=OFF`
- `CMAKE_CUDA_ARCHITECTURES=80;86;89` (Ampere + Ada; larger `libggml-cuda.so`)
- `-DGGML_BACKEND_DIR:STRING=lib` (never untyped `-DGGML_BACKEND_DIR=lib`)
- Stock GCC 11 is sufficient
- Pack: `cp -a build/bin/lib*.so*` into `lib/` (preserve symlinks)

**arm64**

- Same CMake flags as x64 except CUDA arch default `87` (change only when user
  requests more Jetson/datacenter SKUs)
- Ubuntu 22.04 **stock GCC cannot build** `armv9.2` CPU backends → install
  **GCC 14** (`ppa:ubuntu-toolchain-r/test`), `CC=gcc-14`, `CXX=g++-14`
- Bundle `/usr/lib/aarch64-linux-gnu/libstdc++.so.6*` and `libgcc_s.so.1*` into
  package `lib/` so binaries run on stock 22.04
- CUDA stub: `libcuda.so.1 -> /usr/local/cuda/lib64/stubs/libcuda.so` and
  `LD_LIBRARY_PATH` / `LDFLAGS` for link + `llama-server --version` check
- Pack five binaries:
  `llama-bench`, `llama-cli`, `llama-embedding`, `llama-quantize`, `llama-server`
- Do **not** pass host `http_proxy` into the arm64 container (`apt` may fail via
  `host.docker.internal:7890`)

### Verify before upload

1. Tar layout matches the reference package for that arch (see
   `compare-with-gitlab.sh`).
2. `libggml-cuda.so` present; `file` reports correct architecture.
3. In the **22.04 CUDA image** on the matching platform, with packaged `lib/` on
   `LD_LIBRARY_PATH`, `llama-server --version` succeeds (no `GLIBC_2.38` / missing
   `GLIBCXX_3.4.32` on ref or new build).

### Linux NVIDIA runtime dependencies

The Ubuntu CUDA tarballs bundle llama.cpp / ggml shared libraries such as
`libggml-cuda.so`, but they do **not** bundle NVIDIA CUDA runtime libraries such
as `libcudart.so.*`, `libcublas.so.*`, or `libcublasLt.so.*`. A host can have a
working driver and `nvidia-smi` while still missing these userspace libraries;
llama.cpp then logs `no usable GPU found` and only lists CPU devices.

`scripts/install.sh` should keep checking `libggml-cuda.so` with `ldd` after a
Linux NVIDIA CUDA package install. If `libcudart` / `libcublas` are missing on a
supported Ubuntu host, it may add NVIDIA's official CUDA APT repository and
install `cuda-libraries-<major>-<minor>` unless
`CSGHUB_LITE_AUTO_INSTALL_CUDA_LIBS=0`.

### Upload

- Load `GITLAB_TOKEN` from `local/secrets.env` only; `unset` proxy before GitLab.
- URL:
  `https://git-devops.opencsg.com/api/v4/projects/393/packages/generic/llama-cpp/<tag>/<filename>`
- Re-download and compare SHA256 after upload when validating.
- Release `b9158` already links these filenames; replacing the generic package
  updates the release asset.

### macOS repack

If re-tarring on macOS, strip `._*` and `.DS_Store` while preserving symlinks.
Prefer packing inside the Linux container to avoid extra metadata.

### Parallel builds

Use **separate** source trees (`work/src-amd64`, `work/src-arm64`) so x64 and
arm64 builds can run concurrently. On Apple Silicon, amd64 runs under emulation
and is much slower than native arm64.

### Pitfalls (fixed in scripts)

| Issue | Mitigation |
|-------|------------|
| Disk full / Docker engine dead | Free space without deleting pinned images; restart Docker Desktop |
| Only one platform of CUDA image pulled | `docker pull --platform linux/amd64` and `linux/arm64` |
| x64 linked to GLIBC 2.38 | Build in 22.04, not 24.04 |
| arm64 `-march=armv9.2-a` compile error | GCC 14 |
| arm64 `GLIBCXX_3.4.32 not found` at runtime | Bundle GCC 14 `libstdc++` in tarball |
| Missing `libggml-base.so.0` at runtime | `cp -a build/bin/lib*.so*` (not `libggml-*.so` glob alone) |
