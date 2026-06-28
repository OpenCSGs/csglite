FROM ubuntu:24.04

LABEL org.opencontainers.image.title="csghub-lite runtime"
LABEL org.opencontainers.image.description="Ubuntu 24.04 image that downloads csghub-lite and llama-server on container start"
LABEL org.opencontainers.image.source="https://github.com/OpenCSGs/csglite"

ENV DEBIAN_FRONTEND=noninteractive \
    INSTALL_DIR=/root/.csghub-lite/bin \
    CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR=/root/.csghub-lite/bin \
    CSGHUB_LITE_INSTALL_POLICY=if-missing \
    CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER=1 \
    CSGHUB_LITE_FORCE=1 \
    PATH=/root/.csghub-lite/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

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
        sed \
        tar \
        unzip \
    && rm -rf /var/lib/apt/lists/*

# Reuse the runtime installer shared with the ROCm image.
COPY docker/rocm/entrypoint.sh /usr/local/bin/csghub-lite-docker-entrypoint
RUN chmod +x /usr/local/bin/csghub-lite-docker-entrypoint

EXPOSE 11435
VOLUME ["/root/.csghub-lite"]
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=5m --retries=3 \
    CMD curl -fsS http://127.0.0.1:11435/api/health || exit 1

ENTRYPOINT ["csghub-lite-docker-entrypoint"]
CMD ["serve", "--listen", "0.0.0.0:11435"]
