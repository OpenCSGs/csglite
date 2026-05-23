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

## Ubuntu CUDA Mirror

- When mirroring `llama.cpp` Ubuntu CUDA tarballs to GitLab, first extract and
  compare the existing GitLab reference packages:
  - `b8429/llama-b8429-bin-ubuntu-cuda-12.4-arm64.tar.gz`
  - `b8429/llama-b8429-bin-ubuntu-cuda-12.4-x64.tar.gz`
- Upstream `ggml-org/llama.cpp` does not publish Linux CUDA tarballs. First sync
  matching Ubuntu CUDA assets from
  `https://github.com/hybridgroup/llama-cpp-builder/releases/tag/<tag>` and
  reuse them for the GitLab mirror when available.
- `hybridgroup/llama-cpp-builder` uses
  `llama-<tag>-bin-ubuntu-cuda-<arch>.tar.gz` for CUDA 12.9 and
  `llama-<tag>-bin-ubuntu-cuda-13-<arch>.tar.gz` for CUDA 13.0.88.
- If `hybridgroup/llama-cpp-builder` does not have the target tag or
  architecture, build the missing tarball locally in Docker, then upload it to
  the GitLab generic package registry and add a release link.
- When reusing `hybridgroup/llama-cpp-builder` assets, preserve their published
  names, verify the CUDA version/name mapping, and re-pack only when needed to
  match the reference layout.
- Load `GITLAB_TOKEN` only from `local/secrets.env`. Use direct connection for
  GitLab API/package uploads; do not keep external proxy enabled.
- Build `arm64` and `x64` separately. Their tar layouts must match the GitLab
  reference:
  - `arm64`: tar root `llama-<tag>-bin-ubuntu-cuda-12.4-arm64/` with only
    `bin/{llama-bench,llama-cli,llama-embedding,llama-quantize,llama-server}`
    plus `lib/` symlink chains for `libggml-base`, `libggml-cpu`,
    `libggml-cuda`, `libggml`, `libllama`, and `libmtmd`.
  - `x64`: tar root contains `bin/` and `lib/` directly, with `llama-server` in
    `bin/` and the `libggml-cpu-*` backend variants plus `libggml-cuda.so` in
    `lib/`.
- `arm64` build recipe:
  - Use `BUILD_SHARED_LIBS=ON`, `GGML_CUDA=ON`, `GGML_BACKEND_DL=OFF`,
    `GGML_NATIVE=OFF`.
  - Link and runtime checks need CUDA driver stubs inside the container:
    provide `libcuda.so.1 -> /usr/local/cuda/lib64/stubs/libcuda.so` and feed
    that stub directory to linker flags and `LD_LIBRARY_PATH`.
  - Do not pass host proxy into `apt` for the arm64 container; `ports.ubuntu.com`
    may fail through `host.docker.internal:7890`.
- `x64` build recipe:
  - Use `BUILD_SHARED_LIBS=ON`, `GGML_CUDA=ON`, `GGML_BACKEND_DL=ON`,
    `GGML_CPU_ALL_VARIANTS=ON`.
  - Never pass untyped `-DGGML_BACKEND_DIR=lib`; force it as `STRING` or via a
    preload cache file so CMake does not canonicalize it to `/lib`.
- Before upload, re-pack on macOS to remove `._*` and `.DS_Store` entries while
  preserving symlinks.
- Before upload, verify:
  - tar layout matches the reference package for that architecture.
  - `libggml-cuda.so*` is present.
  - `file` reports the correct architecture.
  - `llama-server --version` runs in a container with the packaged `lib/` on
    `LD_LIBRARY_PATH`; use CUDA stubs if no real driver is present.
- Upload tarballs to
  `https://git-devops.opencsg.com/api/v4/projects/393/packages/generic/llama-cpp/<tag>/...`,
  then create matching release asset links for the same filenames.
