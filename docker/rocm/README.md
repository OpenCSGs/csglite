# 构建固定版本的 CSGLite ROCm 镜像

`docker/rocm/Dockerfile` 是 ROCm 运行时引导镜像。它包含 ROCm、Python
转换环境和启动脚本，但不会在 `docker build` 阶段下载 CSGLite。容器首次
启动时，入口脚本会把指定版本的 `csghub-lite` 和 `llama-server` 安装到
持久化目录 `/root/.csghub-lite`。

## 默认运行环境

- 基础镜像：ROCm 7.2.2、Ubuntu 22.04、Python 3.10
- llama-server ROCm 系列：7.2
- 服务端口：11435
- 数据目录：`/root/.csghub-lite`

ROCm 镜像主要用于 Linux AMD GPU 主机。宿主机必须提供兼容的 AMD 驱动、
`/dev/kfd` 和 `/dev/dri`。

## 构建固定版本

从仓库根目录执行：

```bash
docker build \
  --platform linux/amd64 \
  --build-arg CSGHUB_LITE_VERSION=v0.9.4 \
  --build-arg CSGHUB_LITE_LLAMA_CPP_TAG=b10549 \
  --build-arg CSGHUB_LITE_INSTALL_POLICY=if-version-mismatch \
  --build-arg CSGHUB_LITE_LLAMA_ROCM_VERSION=7.2 \
  -f docker/rocm/Dockerfile \
  -t csghub-lite-rocm:0.9.4-rocm7.2 \
  .
```

关键参数：

| 构建参数 | 作用 |
| --- | --- |
| `CSGHUB_LITE_VERSION` | 固定 CSGLite 版本，建议使用完整的 `v*` 版本号。 |
| `CSGHUB_LITE_LLAMA_CPP_TAG` | 固定 llama.cpp/llama-server 版本。 |
| `CSGHUB_LITE_INSTALL_POLICY` | 固定版本应使用 `if-version-mismatch`。 |
| `CSGHUB_LITE_LLAMA_ROCM_VERSION` | 选择 ROCm llama-server 系列。 |
| `BASE_IMAGE` | 可选，替换默认 ROCm 基础镜像。 |

`if-version-mismatch` 很重要：如果挂载的数据卷里已有其他版本，容器启动时
会重新安装构建参数指定的版本。默认的 `if-missing` 只检查文件是否存在，
不会纠正已有数据卷中的版本。

## 使用 Docker Compose

```yaml
services:
  csghub-lite-rocm:
    image: csghub-lite-rocm:0.9.4-rocm7.2
    platform: linux/amd64
    pull_policy: never
    container_name: csghub-lite-rocm
    restart: always
    ports:
      - "11435:11435"
    devices:
      - /dev/kfd
      - /dev/dri
    group_add:
      - video
    ipc: host
    security_opt:
      - seccomp=unconfined
    volumes:
      - ./data/csghub-lite:/root/.csghub-lite
```

启动并验证：

```bash
docker compose up -d
docker exec csghub-lite-rocm csghub-lite --version
curl -fsS http://localhost:11435/api/health
```

首次启动需要下载安装固定版本，因此必须能够访问安装源。安装完成后，
二进制、模型和配置会保存在挂载的数据目录中。

## 硬件兼容参数

以下参数与具体 AMD GPU 有关，不应无条件复制到所有机器：

```yaml
environment:
  HSA_OVERRIDE_GFX_VERSION: "11.0.0"
  HIP_VISIBLE_DEVICES: "0"
```

只有在 GPU 架构检测不正确时才设置 `HSA_OVERRIDE_GFX_VERSION`。错误的值
可能导致 ROCm 初始化失败。多 GPU 主机可通过 `HIP_VISIBLE_DEVICES` 选择
设备。

## 固定版本注意事项

1. 不要把镜像标签改成 `latest`。
2. 不要设置 `CSGHUB_LITE_INSTALL_ALWAYS=1`。
3. 运行时传入的同名环境变量会覆盖镜像内的构建默认值。
4. Web UI 手动升级后，当前容器中的二进制可能暂时变化；
   `if-version-mismatch` 会在下次容器启动时恢复指定版本。
5. 若镜像需要跨机器分发，可推送不可变标签，并在部署时进一步使用
   `image@sha256:<digest>` 固定镜像内容。

该镜像是“固定启动版本”的引导镜像，不是完全离线镜像。如需完全离线运行，
还需要把对应的 CSGLite 和 ROCm llama-server 安装包放到内部镜像源，并通过
`CSGHUB_LITE_INSTALL_URL` 指向该安装源。
