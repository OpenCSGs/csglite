# csglite-binary is a BuildKit context prepared by build.sh (static Linux binary).
ARG BASE_IMAGE=lmsysorg/sglang:v0.5.19-cu129-runtime
FROM ${BASE_IMAGE}

ARG CSGHUB_LITE_VERSION=dev
LABEL org.opencontainers.image.title="CSGLite + SGLang" \
      org.opencontainers.image.source="https://github.com/OpenCSGs/csglite" \
      org.opencontainers.image.version=${CSGHUB_LITE_VERSION}

USER root
ENV HOME=/root \
    PYTHONUNBUFFERED=1 \
    CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER=0 \
    SGLANG_MODEL_PATH=/root/.csghub-lite/models/serving \
    SGLANG_MODEL_NAME=local-sglang \
    HF_HOME=/root/.csghub-lite/cache/huggingface \
    XDG_CACHE_HOME=/root/.csghub-lite/cache \
    SGLANG_CACHE_DIR=/root/.csghub-lite/cache/sglang \
    SGLANG_JIT_CACHE_DIR=/root/.csghub-lite/cache/sglang/jit \
    FLASHINFER_WORKSPACE_BASE=/root/.csghub-lite/cache/sglang \
    TORCHINDUCTOR_CACHE_DIR=/root/.csghub-lite/cache/inductor \
    SGLANG_WEIGHT_CACHE_SOCKET_TEMPLATE=/root/.csghub-lite/tmp/sglang_weight_cache_{device_uuid}.sock \
    SGLANG_WEIGHT_CACHE_READY_TEMPLATE=/root/.csghub-lite/tmp/sglang_weight_cache_{device_uuid}.ready \
    TRITON_CACHE_DIR=/root/.csghub-lite/cache/triton \
    TORCH_EXTENSIONS_DIR=/root/.csghub-lite/cache/torch-extensions \
    CUDA_CACHE_PATH=/root/.csghub-lite/cache/cuda \
    TMPDIR=/root/.csghub-lite/tmp TMP=/root/.csghub-lite/tmp TEMP=/root/.csghub-lite/tmp

COPY --from=csglite-binary /csghub-lite /usr/local/bin/csghub-lite
COPY docker/sglang/launcher.py /opt/csglite-sglang/launcher.py
# Do not install or replace the GPU stack inherited from the chosen base image.
RUN python3 -c "import sglang, torch; print('SGLang:', sglang.__file__); print('PyTorch:', torch.__version__)" \
    && python3 -c "import torch; assert not torch.version.cuda or int(torch.version.cuda.split('.')[0]) == 12, 'This image requires CUDA 12.x, not CUDA 13+'" \
    && csghub-lite --version

EXPOSE 11435
EXPOSE 11438
VOLUME ["/root/.csghub-lite"]
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=10s --start-period=15m --retries=3 \
    CMD ["python3", "/opt/csglite-sglang/launcher.py", "health"]
ENTRYPOINT ["python3", "/opt/csglite-sglang/launcher.py"]
