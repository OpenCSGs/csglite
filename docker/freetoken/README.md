# FreeToken A800 benchmark image

The image runs FreeToken 0.2.2 on Linux x86-64 with CUDA 13.0. It packages the
official FreeToken runtime and precompiled kernel wheels and retains the CUDA
compiler for kernels needed at runtime. The container uses Python 3.12 for the
upstream wheels. The host scripts support Python 3.9 and later.

## Build

Run from the repository root with Docker Buildx and Linux x86-64 build support.
On the mac-mini builder, load the development network settings first.

```bash
source ~/.myshrc
python3 docker/freetoken/build.py
```

The build verifies both FreeToken wheel hashes and caches the wheels under
`~/.csghub-lite/tmp/freetoken-v0.2.2-cu130-a800/wheels`. Use `--image` to
choose the resulting local tag.

## Run the A800 comparison

On a Linux host with an A800 and the NVIDIA Container Toolkit, set
`MODEL_DIR` to an existing local Qwen3.8-27B BF16 checkpoint directory. The
container reads the weights from that directory and uses a separate writable
directory for caches.

```bash
mkdir -p "$HOME/.csghub-lite/freetoken-benchmark"
docker run --rm --gpus 1 --shm-size=8g -p 127.0.0.1:1919:1919 \
  -v "$MODEL_DIR:/models/Qwen3.8-27B:ro" \
  -v "$HOME/.csghub-lite/freetoken-benchmark:/root/.csghub-lite" \
  csglite:freetoken-v0.2.2-cu130-a800 \
  serve --model /models/Qwen3.8-27B --served-model-name Qwen3.8-27B-A800 \
  --host 0.0.0.0 --dtype bfloat16 --max-seq-len-override 8192 \
  --max-running-requests 4 --memory-ratio 0.82 \
  --spec-algo mtp --spec-num-steps 3 --enable-timings-report
```

In another terminal, run the Chinese operations prompt with one warm-up and
three measured requests of up to 384 generated tokens each.

```bash
python3 docker/freetoken/benchmark.py http://127.0.0.1:1919 Qwen3.8-27B-A800
```

The original run measured 53.16 token/s for FreeToken and 56.03 token/s for
SGLang. The runs used two same-model A800 80GB cards on the same node, the
same Qwen3.8-27B BF16 weights, and MTP with three draft tokens. The benchmark
reports decode speed separately from time to first token.

The published test image is
`opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/csglite:freetoken-v0.2.2-cu130-a800`.

[FreeToken release notes](https://github.com/FlashML-org/FreeToken/releases/tag/v0.2.2)
