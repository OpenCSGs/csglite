ARG BASE_IMAGE=rocm/pytorch:rocm7.2.3_ubuntu24.04_py3.12_pytorch_release_2.10.0
FROM ${BASE_IMAGE}

LABEL org.opencontainers.image.title="csghub-lite ROCm runtime"
LABEL org.opencontainers.image.description="ROCm 7.2.3 Ubuntu 24.04 image that installs csghub-lite on container start"

ENV DEBIAN_FRONTEND=noninteractive \
    INSTALL_DIR=/usr/local/bin \
    CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR=/usr/local/bin \
    CSGHUB_LITE_LLAMA_ROCM_VERSION=7.2 \
    CSGHUB_LITE_FORCE=1

RUN apt-get update && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        coreutils \
        curl \
        findutils \
        gawk \
        grep \
        gzip \
        patchelf \
        procps \
        sed \
        tar \
        unzip \
    && rm -rf /var/lib/apt/lists/*

COPY docker/rocm/entrypoint.sh /usr/local/bin/csghub-lite-docker-entrypoint
RUN chmod +x /usr/local/bin/csghub-lite-docker-entrypoint

EXPOSE 11435
VOLUME ["/root/.csghub-lite"]

ENTRYPOINT ["csghub-lite-docker-entrypoint"]
CMD ["serve", "--listen", "0.0.0.0:11435"]
