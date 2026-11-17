# CSGLite + SGLang Docker

将 CSGLite 加入已预装 SGLang 的镜像。一个容器运行两个进程，通过容器内部
OpenAI 接口连接。启动时注册 `SGLang` provider 及选中的模型，直接在 CSGLite
聊天模型列表里选择 `local-sglang [SGLang]`。这条路径直读 Safetensors，不转换
GGUF。模型库里单独下载的模型不会自动变为 SGLang 服务模型。

Docker 部署面向 Linux NVIDIA 和 AMD 主机，macOS 不在本方案的运行支持范围内。
构建机仍可使用 macOS，交叉编译并构建 Linux 镜像。

## NVIDIA 验证镜像

标签：`opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/csglite:sglang-v0.5.19-cu129-min`。
仅支持 `linux/amd64`；包含 CSGLite、SGLang 0.5.19、PyTorch 2.13.0+cu129 和 CUDA 12.9。
已上传国内仓库，实际模型推理待 NVIDIA 主机验收。镜像不包含模型权重。

| 发布信息 | 实测值 |
| --- | --- |
| 镜像源码 / 固定发布标签 | `0acc330` / `sglang-v0.5.19-cu129-min-0acc330` |
| 已验证的启动安装版本 | Lite `v1.0.5`；运行版本由 `CSGHUB_LITE_VERSION` 控制 |
| 下载体积（registry layer 合计） | 13,328,134,665 字节，约 13.33GB；比上游约 17.16GB 减少约 22% |
| 解包体积 | 约 31GB；Docker Desktop 29 连同压缩内容存储显示约 44.1GB，不含模型和新增运行缓存 |
| 镜像索引 digest | `sha256:f47c67c5f689c210bb9ffaf08ef322bd1c636fd97cd2827b3b836960da13405f` |
| linux/amd64 digest | `sha256:205607e2765e40bb7638a9e108feb537c497ddfc583f4e6cfec50949c5d58a84` |
| 上游 linux/amd64 digest | `sha256:5412cdedcacf3e05c329e24d097988b9cae15ad1cf69289fc8014df94e40972b` |

移除 154 个可选 Python 包和约 3.32GB 静态数学库，保留 208 个运行/JIT 依赖包及预编译算子。

从仓库根目录启动，无需本地构建或执行 pip 安装：

```bash
export CSGLITE_SGLANG_IMAGE=opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/csglite:sglang-v0.5.19-cu129-min
export MODEL_DIR=/absolute/path/to/complete-hf-model
docker compose -f docker/sglang/compose.cuda.yaml pull
docker compose -f docker/sglang/compose.cuda.yaml up -d
docker compose -f docker/sglang/compose.cuda.yaml logs -f
```

也可直接运行镜像，替换宿主模型目录和持久化数据目录：

```bash
docker run -d --name csglite-sglang --gpus '"device=0"' --shm-size=8g \
  --init --restart unless-stopped --stop-timeout 30 \
  -p 127.0.0.1:11435:11435 \
  -v "$HOME/.csghub-lite/docker-sglang:/root/.csghub-lite" \
  -v /absolute/path/to/complete-hf-model:/root/.csghub-lite/models/serving:ro \
  -e SGLANG_EXTRA_ARGS='["--mem-fraction-static","0.8","--context-length","8192"]' \
  opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/csglite:sglang-v0.5.19-cu129-min
```

模型就绪后访问 `http://localhost:11435`，选择 `local-sglang [SGLang]`。
入群配置见下方「加入现有集群」，使用现有集群环境变量。

## 构建与 NVIDIA 部署（CUDA 12.9）

宿主需安装兼容 CUDA 12.9 的 NVIDIA 驱动、Docker 和 NVIDIA Container Toolkit。
按 NVIDIA 的 CUDA 12.9 兼容说明检查驱动；不能把能运行早期 CUDA 12 的驱动视为
一定满足镜像全部算子的要求。默认基础镜像固定为
`lmsysorg/sglang:v0.5.19-cu129-runtime`，不使用 CUDA 13。

默认使用 `Dockerfile.cuda` 构建文本推理专用镜像：按已安装包的依赖图保留 SGLang
默认运行依赖、CUDA 12 和预编译算子，移除仅由 diffusion/all/dev extras 引入的包，
删除源码仓库、文档、下载缓存及 CUDA 数学库的静态链接副本，保留动态库；
最后从清理后的文件系统重新打包，避免旧镜像层
继续携带被删除的内容。保留 NVCC、GCC、Ninja 和头文件供 Triton、FlashInfer、
DeepGEMM 首次推理 JIT 使用；不承诺图像/视频生成运行时。
移除与保留的 Python 包版本清单在 `/opt/csglite/runtime-packages.json`。
预编译 kernel 缓存保存在 `/opt/csglite/runtime-cache`，启动时补齐到持久化缓存，
保留用户已有文件。精简包的实际 GPU 推理仍需 NVIDIA 主机验证。

从仓库根目录执行；构建机需要 Go（版本满足 go.mod）、Docker BuildKit，以及访问
Go 模块和基础镜像的网络。外网访问遵循仓库规则先 `source ~/.myshrc`。

```bash
bash docker/sglang/build.sh
export MODEL_DIR=/absolute/path/to/Qwen3-0.6B
docker compose -f docker/sglang/compose.cuda.yaml up -d
```

`MODEL_DIR` 必须是完整的本地 HF 模型目录（config.json、tokenizer、全部权重分片），
无需在启动时联网下载模型。默认占用第 0 张 GPU、8K 上下文、显存比例 0.8，模型
本身需支持该上下文且能放入显存。用 `GPU_ID` 选择 GPU，用 `MODEL_NAME` 设置服务名。
页面地址为 `http://localhost:11435`。通过 Compose logs 查看加载进度。

构建脚本交叉编译仓库当前源码作为内置回退版本，嵌入现有 `internal/server/static`。
运行时按 `CSGHUB_LITE_VERSION` 选择 Lite；SGLang/CUDA 运行环境保持固定。若需要更新 UI，先执行仓库的 `make build-web`。临时构建文件放在
`~/.csghub-lite/tmp/sglang-build`。镜像不安装 llama.cpp，也不修改基础镜像的 GPU
依赖。用户只需拉取最终镜像；完整首次启动不执行 pip 或 apt。

国内基础镜像可用 `BASE_IMAGE` 替换，必须保持所选 CUDA 12 版本和平台一致：

```bash
BASE_IMAGE=<国内仓库>/sglang:<固定的CUDA12标签或digest> \
  IMAGE_TAG=<国内仓库>/csglite-sglang:<发布版本>-cu129 \
  CSGHUB_LITE_VERSION=<发布版本> bash docker/sglang/build.sh
```

构建脚本只构建本地镜像，不执行 push。为复现构建，使用基础镜像 digest；
验证镜像可先上传供实机验收，宣称 GPU 可用前必须在 NVIDIA 机器验证模型推理。
设置 `CSGLITE_SGLANG_IMAGE` 使用发布后的地址。

## Lite 启动升级与指定版本

默认 `CSGHUB_LITE_VERSION=latest`：每次启动调用官方
`https://hub.opencsg.com/csghub-lite/install.sh` 安装最新版 Lite。
发行包下载和国内/海外源选择全部复用该安装脚本，当前已验证的 Linux Lite 安装包约 11MB。
不升级 SGLang、PyTorch、CUDA，不执行 pip/apt，也不会安装 llama.cpp。

| `CSGHUB_LITE_VERSION` | 行为 |
| --- | --- |
| 不设置、空值或 `latest` | 每次启动运行安装脚本更新 Lite；安装失败时复用缓存版本，没有缓存则使用镜像内置版 |
| `v0.8.55` 等发布版本 | 严格使用指定版本；本地已匹配则跳过安装并可离线复用，否则安装该版本，失败则退出 |
| `bundled` | 使用镜像内置回退版，跳过联网安装 |

Lite 程序持久化到 `/root/.csghub-lite/bin/csghub-lite`。安装先在同一存储根目录的
临时目录中进行，校验实际版本后替换持久化程序，失败时保留旧文件。运行中的
Lite 也使用此路径，页面手动升级的程序文件可随数据卷保留。
安装器的临时后台服务会在安装结束后清理，正式服务由容器启动器统一管理。
启动器强制 `CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER=0`，包括用户传入 `1` 的情况。

日常更新 Lite 直接重启容器：

```bash
docker restart csglite-sglang
# 使用 Compose 启动时：
docker compose -f docker/sglang/compose.cuda.yaml restart
```

指定版本时，修改环境变量并使用同一镜像、同一数据目录重建容器：

```bash
export CSGHUB_LITE_VERSION=v0.8.55
docker compose -f docker/sglang/compose.cuda.yaml up -d
# docker run 的对应参数：-e CSGHUB_LITE_VERSION=v0.8.55
```

旧的 `sglang-v0.5.19-cu129-min-8986bf4` 标签使用固定 Lite；首次启用本功能需切换到
上方新标签。之后仅更新 Lite 无需重建运行时镜像。`CSGHUB_LITE_REGION=CN/INTL`
可指定安装器的下载地区，Compose 默认 `CN`。

## AMD / MI210

共用 Dockerfile，使用匹配 GPU 的 ROCm SGLang 基础镜像构建：

```bash
BASE_IMAGE=<已验证的ROCm-SGLang镜像或digest> \
  DOCKERFILE=docker/sglang/Dockerfile \
  IMAGE_TAG=csglite-sglang:dev-rocm PLATFORM=linux/amd64 \
  bash docker/sglang/build.sh
export CSGLITE_SGLANG_IMAGE=csglite-sglang:dev-rocm
export MODEL_DIR=/absolute/path/to/model
export VIDEO_GID=$(getent group video | cut -d: -f3)
export RENDER_GID=$(getent group render | cut -d: -f3)
docker compose -f docker/sglang/compose.rocm.yaml up -d
```

以上是 Linux 主机命令。宿主需兼容 AMD 驱动及 `/dev/kfd`、`/dev/dri`。
默认关闭 AITER，以 Triton 和禁用 CUDA graph 作为保守起点，并非所有模型通用的
性能预设。用 `SGLANG_EXTRA_ARGS` 覆盖需要的模型参数。

**MI210 状态：尚未验证、未提供可宣称开箱即用的基础镜像。** 上游当前 ROCm
构建档主要面向 gfx942/gfx950/gfx1250，不能直接用于 gfx90a（MI210）。需要获取或
制作覆盖 gfx90a 的 PyTorch、SGLang 算子组合，并在 MI210 验证后发布专用标签。
Compose 强制指定镜像，避免默认误用 MI300 包。没有真实 AMD GPU 时的构建成功
不能作为 MI210 通过的依据。

## 加入现有集群

支持 CSGLite 现有的全部集群环境变量。容器桥接网络默认不依赖 mDNS，通过静态
seed 找到节点。使用单独的 cluster overlay，普通部署不额外公开集群端口。

```bash
export CSGHUB_LITE_CLUSTER_ADVERTISE_HOST=<本机可被其他节点访问的局域网IP>
export CSGHUB_LITE_CLUSTER_SEEDS=<已有节点IP>:11438
# 共享密钥由部署环境注入，也可改用首次加入令牌：
# CSGHUB_LITE_CLUSTER_SECRET / CSGHUB_LITE_CLUSTER_JOIN_TOKEN
docker compose -f docker/sglang/compose.cuda.yaml \
  -f docker/sglang/compose.cluster.yaml up -d
```

将 cuda 文件替换为 rocm 文件即可使用 AMD。同机原生 CSGLite 不得
同时占用 11438。各主机之间需双向可达 TCP 11438，集群身份持久化在数据卷中。
本期只复用现有入群能力；SGLang provider 不因此自动成为集群调度的本地部署资源。

| 环境变量 | 用途 |
| --- | --- |
| `CSGHUB_LITE_CLUSTER_SECRET` | 同一共享密钥自动组网（至少 4 位，建议 12 位以上） |
| `CSGHUB_LITE_CLUSTER_NAME` | 集群显示名 |
| `CSGHUB_LITE_CLUSTER_JOIN_TOKEN` | 尚未入群节点首次加入令牌 |
| `CSGHUB_LITE_CLUSTER_SEEDS` | 逗号分隔的成员地址，容器推荐显式配置 |
| `CSGHUB_LITE_CLUSTER_ADDR` | overlay 固定为 `:11438`，与发布端口一致 |
| `CSGHUB_LITE_CLUSTER_ADVERTISE_HOST` | 对外可达的宿主 IP/主机名，overlay 必填 |
| `CSGHUB_LITE_CLUSTER_DISCOVERY` | overlay 默认 `none`，宿主网络部署可用 `mdns` |
| `CSGHUB_LITE_CLUSTER_DISABLED` | `1` 禁用集群 |

## 运行参数与数据

| 环境变量 | 默认值 / 含义 |
| --- | --- |
| `CSGHUB_LITE_VERSION` | `latest`；指定 Lite 发布版本或使用 `bundled` |
| `CSGHUB_LITE_REGION` | 安装器下载地区 `CN` / `INTL`，Compose 默认 `CN` |
| `SGLANG_MODEL_PATH` | `/root/.csghub-lite/models/serving`，完整本地模型路径 |
| `SGLANG_MODEL_NAME` | `local-sglang`，页面及 API 使用的模型名 |
| `SGLANG_EXTRA_ARGS` | JSON 字符串数组，例如 `["--tp","2","--context-length","4096"]` |
| `SGLANG_START_TIMEOUT` | `1800` 秒，模型初始化上限 |
| `SGLANG_API_KEY` | 可选的内部/已有 SGLang 服务接口密钥 |
| `SGLANG_BASE_URL` | 设置后连接已有 SGLang，跳过容器内 SGLang 进程启动 |
| `CSGLITE_DATA_DIR` | Compose 数据目录，默认相对于 Compose 文件的 `./data` |

所有应用运行数据、缓存、临时文件在 `/root/.csghub-lite`。只修改本镜像管理的
provider ID，保留其他 provider 和模型选择；每次启动按环境变量刷新本 provider。
一个数据目录只供一个运行中的 CSGLite 容器使用，不与原生服务共享写入。
SGLang 仅监听容器 loopback 30000，不公开内部服务端口。CSGLite 默认只发布本机
11435。日志输出到 Docker stdout/stderr。任一托管进程退出则停止另一个，容器
返回失败码；停止时向两个进程组发送 SIGTERM，超时强制结束。

`SGLANG_EXTRA_ARGS` 不接受覆盖受管理的 host/port/model/name/API-key 参数。
改变服务模型目录后重建容器，无需重新构建镜像。Lite 更新或版本切换见上方启动升级说明；
SGLang/CUDA 升级通过替换运行时镜像完成，升级前先备份数据卷。

## 验证

本次本地验证记录（2026-11-17）：

| 项目 | 状态 |
| --- | --- |
| 启动器、缓存、集群环境变量和依赖裁剪 | 17 个单元测试通过 |
| Lite 启动安装 | 真实安装脚本安装最新版与固定版 v1.0.5、重建后离线复用、更新失败回退通过；未安装 llama-server |
| CUDA / ROCm Compose，含集群 overlay | 4 种组合解析通过 |
| 真实 CSGLite 对接模拟 OpenAI 服务 | 无外部网络容器中，模型注册、普通/流式聊天、联合健康检查、正常退出通过 |
| NVIDIA CUDA 12.9 精简镜像 | linux/amd64 构建通过；SGLang/PyTorch/Transformers 导入与 SGLang CLI 检查通过 |
| CUDA JIT 工具链 | NVCC 12.9 编译并链接 sm_80 CUDA 程序通过，未执行 GPU kernel |
| AMD MI210 | 缺少经验证的 gfx90a 基础运行环境，待实机适配 |
| 集群真实节点互联 | 环境变量透传与 Compose 检查通过，真实节点互联待实测 |
| 国内仓库发布 | NVIDIA 精简镜像上传成功，registry manifest 与 digest 校验通过，体积见上方发布信息 |

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s docker/sglang -v
MODEL_DIR=/models docker compose -f docker/sglang/compose.cuda.yaml config --quiet
# 使用已构建的 Linux 镜像验证真实 CSGLite 注册、转发、健康检查和退出：
docker run --rm --network none \
  -v "$PWD/docker/sglang/smoke.py:/smoke.py:ro" \
  --entrypoint python3 "${CSGLITE_SGLANG_IMAGE:-csglite-sglang:dev-cu129}" /smoke.py
```

该 smoke 使用 OpenAI 模拟服务，不能代替真实 GPU 验收。NVIDIA 镜像需在对应
CUDA 12 主机验证真实模型加载、流式输出、重启、停止释放资源和集群入群。
MI210 待基础运行环境就绪后单独验收。
