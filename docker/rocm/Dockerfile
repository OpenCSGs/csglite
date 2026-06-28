ARG BASE_IMAGE=docker.1ms.run/rocm/pytorch:rocm7.2.2_ubuntu22.04_py3.10_pytorch_release_2.10.0
FROM ${BASE_IMAGE}

LABEL org.opencontainers.image.title="csghub-lite ROCm runtime"
LABEL org.opencontainers.image.description="ROCm 7.2.3 Ubuntu 24.04 image that downloads csghub-lite and ROCm llama-server on container start"
LABEL org.opencontainers.image.source="https://github.com/OpenCSGs/csglite"

ENV DEBIAN_FRONTEND=noninteractive \
    INSTALL_DIR=/root/.csghub-lite/bin \
    CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR=/root/.csghub-lite/bin \
    CSGHUB_LITE_INSTALL_POLICY=if-missing \
    CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER=1 \
    CSGHUB_LITE_LLAMA_ROCM_VERSION=7.2 \
    CSGHUB_LITE_FORCE=1 \
    PIP_INDEX_URL=https://mirrors.tuna.tsinghua.edu.cn/pypi/web/simple \
    CSGHUB_LITE_TORCH_PACKAGE=torch==2.12.0+cpu \
    CSGHUB_LITE_TORCH_INDEX_URL=https://download.pytorch.org/whl/cpu \
    PATH=/root/.csghub-lite/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# Use a China-accessible Ubuntu mirror for OS packages and skip third-party
# apt sources from the base image that are not needed for this install step.
RUN set -eux; \
    for source in /etc/apt/sources.list /etc/apt/sources.list.d/*.list /etc/apt/sources.list.d/*.sources; do \
        [ -f "${source}" ] || continue; \
        sed -i \
            -e 's|http://archive.ubuntu.com/ubuntu|http://mirrors.aliyun.com/ubuntu|g' \
            -e 's|http://security.ubuntu.com/ubuntu|http://mirrors.aliyun.com/ubuntu|g' \
            -e 's|https://archive.ubuntu.com/ubuntu|http://mirrors.aliyun.com/ubuntu|g' \
            -e 's|https://security.ubuntu.com/ubuntu|http://mirrors.aliyun.com/ubuntu|g' \
            "${source}"; \
    done; \
    for source in /etc/apt/sources.list.d/*deadsnakes* /etc/apt/sources.list.d/*rocm* /etc/apt/sources.list.d/*amdgpu*; do \
        [ -e "${source}" ] || continue; \
        mv "${source}" "${source}.disabled"; \
    done

RUN apt-get update && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        coreutils \
        curl \
        findutils \
        gawk \
        grep \
        gzip \
        libgomp1 \
        libstdc++6 \
        patchelf \
        procps \
        python3-venv \
        sed \
        tar \
        unzip \
    && rm -rf /var/lib/apt/lists/*

RUN set -eux; \
    python3 -m venv /opt/csghub-lite/python-converter; \
    /opt/csghub-lite/python-converter/bin/python -m pip install --no-cache-dir --upgrade \
        --index-url "${PIP_INDEX_URL}" \
        pip; \
    /opt/csghub-lite/python-converter/bin/python -m pip install --no-cache-dir --upgrade \
        --index-url "${CSGHUB_LITE_TORCH_INDEX_URL}" \
        "${CSGHUB_LITE_TORCH_PACKAGE}"; \
    /opt/csghub-lite/python-converter/bin/python -m pip install --no-cache-dir --upgrade \
        --index-url "${PIP_INDEX_URL}" \
        safetensors \
        transformers \
        sentencepiece; \
    /opt/csghub-lite/python-converter/bin/python -c "import safetensors, sentencepiece, torch, transformers"

COPY docker/rocm/entrypoint.sh /usr/local/bin/csghub-lite-docker-entrypoint
RUN chmod +x /usr/local/bin/csghub-lite-docker-entrypoint

EXPOSE 11435
VOLUME ["/root/.csghub-lite"]
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=5m --retries=3 \
    CMD curl -fsS http://127.0.0.1:11435/api/health || exit 1

ENTRYPOINT ["csghub-lite-docker-entrypoint"]
CMD ["serve", "--listen", "0.0.0.0:11435"]
