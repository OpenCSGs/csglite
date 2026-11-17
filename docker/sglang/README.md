# CSGLite + SGLang Docker

将 CSGLite 加入已预装 SGLang 的镜像。一个容器运行两个进程，通过容器内部
OpenAI 接口连接。启动时注册 `SGLang` provider 及选中的模型，直接在 CSGLite
聊天模型列表里选择 `local-sglang [SGLang]`。这条路径直读 Safetensors，不转换
GGUF。模型库里单独下载的模型不会自动变为 SGLang 服务模型。

Docker 部署面向 Linux NVIDIA 和 AMD 主机，macOS 不在本方案的运行支持范围内。
构建机仍可使用 macOS，交叉编译并构建 Linux 镜像。

## 构建与 NVIDIA 部署（CUDA 12.9）

宿主需安装兼容 CUDA 12.9 的 NVIDIA 驱动、Docker 和 NVIDIA Container Toolkit。
按 NVIDIA 的 CUDA 12.9 兼容说明检查驱动；不能把能运行早期 CUDA 12 的驱动视为
一定满足镜像全部算子的要求。默认基础镜像固定为
`lmsysorg/sglang:v0.5.19-cu129-runtime`，不使用 CUDA 13。

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

构建脚本交叉编译仓库当前源码，嵌入现有 `internal/server/static`，不从发布站下载
程序。若需要更新 UI，先执行仓库的 `make build-web`。临时构建文件放在
`~/.csghub-lite/tmp/sglang-build`。镜像不安装 llama.cpp，也不修改基础镜像的 GPU
依赖。用户只需拉取最终镜像；完整首次启动不执行 pip 或 apt。

国内基础镜像可用 `BASE_IMAGE` 替换，必须保持所选 CUDA 12 版本和平台一致：

```bash
BASE_IMAGE=<国内仓库>/sglang:<固定的CUDA12标签或digest> \
  IMAGE_TAG=<国内仓库>/csglite-sglang:<发布版本>-cu129 \
  CSGHUB_LITE_VERSION=<发布版本> bash docker/sglang/build.sh
```

构建脚本只构建本地镜像，不执行 push。发布前需固定基础镜像 digest，在对应 NVIDIA
机器完成实机验证，再上传国内仓库。设置 `CSGLITE_SGLANG_IMAGE` 使用发布后的地址。

## AMD / MI210

共用 Dockerfile，使用匹配 GPU 的 ROCm SGLang 基础镜像构建：

```bash
BASE_IMAGE=<已验证的ROCm-SGLang镜像或digest> \
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
改变服务模型目录后重建容器，无需重新构建镜像。升级通过新镜像替换容器，回退使用
旧标签与原数据卷，升级前先备份数据卷。

## 验证

本次本地验证记录（2026-11-17）：

| 项目 | 状态 |
| --- | --- |
| 启动器与集群环境变量透传 | 6 个单元测试通过 |
| CUDA / ROCm Compose，含集群 overlay | 4 种组合解析通过 |
| 真实 CSGLite 对接模拟 OpenAI 服务 | 无外部网络容器中，模型注册、普通/流式聊天、联合健康检查、正常退出通过 |
| NVIDIA CUDA 12.9 完整镜像 | 已验证基础标签存在，完整构建与 GPU 推理待完成 |
| AMD MI210 | 缺少经验证的 gfx90a 基础运行环境，待实机适配 |
| 集群真实节点互联、国内发布 | 待验证 / 待发布 |

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s docker/sglang -v
MODEL_DIR=/models docker compose -f docker/sglang/compose.cuda.yaml config --quiet
# 使用已构建的 Linux 镜像验证真实 CSGLite 注册、转发、健康检查和退出：
docker run --rm --network none \
  -v "$PWD/docker/sglang/smoke.py:/smoke.py:ro" \
  --entrypoint python3 "${CSGLITE_SGLANG_IMAGE:-csglite-sglang:dev-cu129}" /smoke.py
```

该 smoke 使用 OpenAI 模拟服务，不能代替真实 GPU 验收。发布前需在 NVIDIA CUDA
12 和 MI210 环境分别验证真实模型加载、流式输出、重启、停止释放
资源和集群入群，并记录镜像大小与驱动要求。当前仓库文件不代表镜像已经发布。
