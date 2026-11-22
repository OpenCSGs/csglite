FROM nvidia/cuda:13.0.2-base-ubuntu24.04@sha256:605fb0c8acf8674e164d822da8a8521f3a655056e569f0899e72ae940e1fe7dc AS cuda-base

# PyTorch supplies the CUDA math libraries. Retain the CUDA JIT compiler,
# headers and host compiler so FlashInfer can build kernels on first use.
RUN sed -i -e 's|http://archive.ubuntu.com/ubuntu/|https://mirrors.aliyun.com/ubuntu/|g' \
       -e 's|http://security.ubuntu.com/ubuntu/|https://mirrors.aliyun.com/ubuntu/|g' \
       /etc/apt/sources.list.d/ubuntu.sources \
    && apt-get -o Acquire::Retries=3 update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
       ca-certificates g++ libnuma1 python3-dev python3-venv \
       cuda-nvcc-13-0 cuda-cudart-dev-13-0 cuda-cccl-13-0 \
    && rm -rf /var/lib/apt/lists/* \
    && python3 -m venv /opt/freetoken

FROM cuda-base AS runtime
LABEL org.opencontainers.image.title="FreeToken CUDA 13 inference" \
      org.opencontainers.image.version="0.2.2" \
      org.opencontainers.image.source="https://github.com/FlashML-org/FreeToken"
ENV PATH="/opt/freetoken/bin:/usr/local/cuda-13.0/bin:${PATH}" \
    CUDA_HOME="/usr/local/cuda-13.0" \
    NVIDIA_DRIVER_CAPABILITIES="compute,utility" \
    PYTHONUNBUFFERED="1"
ARG PYPI_INDEX=https://mirrors.aliyun.com/pypi/simple/
RUN --mount=type=bind,source=wheels/freetoken-0.2.2-cp312-cp312-manylinux_2_27_x86_64.whl,target=/wheels/freetoken-0.2.2-cp312-cp312-manylinux_2_27_x86_64.whl,readonly \
    --mount=type=cache,target=/root/.cache/uv \
    python -m pip install --no-cache-dir --disable-pip-version-check \
      --index-url "$PYPI_INDEX" --target /tmp/freetoken-bootstrap 'uv==0.9.30' \
    && UV_LINK_MODE=copy /tmp/freetoken-bootstrap/bin/uv pip install \
      --python /opt/freetoken/bin/python --only-binary=:all: --index "$PYPI_INDEX" \
      'torch==2.14.1' '/wheels/freetoken-0.2.2-cp312-cp312-manylinux_2_27_x86_64.whl[accel]' \
    && rm -rf /tmp/freetoken-bootstrap \
    && python -m pip check \
    && python -c "from importlib.metadata import version; import torch; assert version('freetoken') == '0.2.2'; assert torch.version.cuda == '13.0', torch.version.cuda; print(torch.__version__, torch.version.cuda, flush=True)" \
    && nvcc --version \
    && ft --version \
    && ft serve --help >/dev/null

ENV FREETOKEN_HOME="/root/.csghub-lite/cache/freetoken" \
    HF_HOME="/root/.csghub-lite/cache/huggingface" \
    HF_HUB_CACHE="/root/.csghub-lite/cache/huggingface/hub" \
    MODELSCOPE_CACHE="/root/.csghub-lite/cache/modelscope" \
    XDG_CACHE_HOME="/root/.csghub-lite/cache" \
    FLASHINFER_WORKSPACE_BASE="/root/.csghub-lite/cache/flashinfer" \
    TRITON_CACHE_DIR="/root/.csghub-lite/cache/triton" \
    TORCH_EXTENSIONS_DIR="/root/.csghub-lite/cache/torch-extensions" \
    CUDA_CACHE_PATH="/root/.csghub-lite/cache/cuda" \
    TMPDIR="/root/.csghub-lite/tmp" TMP="/root/.csghub-lite/tmp" TEMP="/root/.csghub-lite/tmp"
COPY entrypoint.sh /usr/local/bin/freetoken-entrypoint
EXPOSE 1919
ENTRYPOINT ["/usr/local/bin/freetoken-entrypoint"]
CMD ["--help"]

FROM runtime AS precompiled
RUN --mount=type=bind,source=wheels,target=/wheels,readonly \
    python -m pip install --no-deps --no-cache-dir --disable-pip-version-check \
      '/wheels/freetoken_kernel_cache-0.2.2+cu130-py3-none-linux_x86_64.whl' \
    && python -m pip check
