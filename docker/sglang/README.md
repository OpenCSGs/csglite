# CSGLite + SGLang Docker

本镜像在已预装 SGLang 的 Linux GPU 容器中运行 CSGLite。默认是 **native
managed 模式**：CSGLite 自己按模型启动、停止和重启 SGLang 进程，模型通过
Web UI 或 `/api/*`、`/v1/*` 选择并加载，不需要 `SGLANG_MODEL_PATH`，也不要求
容器启动时就挂载固定模型。Safetensors 直接由 SGLang 服务，不转换 GGUF。

Docker 部署面向 Linux NVIDIA 和 AMD 主机，macOS 不在本方案的运行支持范围内。
构建机仍可使用 macOS 交叉编译并构建 Linux 镜像。

## 版本要求

native managed 镜像内置包含 SGLang 后端的 CSGLite 构建。启动默认通过官方
installer 检查 `latest`，在内置、缓存和下载版本中保留较新的 semver 版本，避免
公开版本暂时落后时覆盖新功能。也可设置 `CSGHUB_LITE_VERSION=vX.Y.Z` 指定版本，
或设置 `bundled` 使用镜像内置版本。精确指定旧版本会按指定值安装；旧版 Lite
需要搭配 legacy fixed-model 模式。

本地构建需先编译并同步 UI，且必须提供可比较的 semver 构建版本：

```bash
make build-web
CSGHUB_LITE_VERSION=1.0.6-sglang.1 \
IMAGE_TAG=opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/csglite:sglang-v0.5.19-cu129-native \
./docker/sglang/build.sh
```

这里的版本用于二进制和镜像标签元数据，不会创建 Git release tag。

## NVIDIA 验证镜像

历史运行时底座标签：`opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/csglite:sglang-v0.5.19-cu129-min`。
仅支持 `linux/amd64`；包含 CSGLite、SGLang 0.5.19、PyTorch 2.13.0+cu129 和 CUDA 12.9。
镜像不包含模型权重。下表记录历史镜像的体积和 digest；native 镜像使用独立标签。

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

**发布标签说明**：上述已发布的 `sglang-v0.5.19-cu129-min` 使用旧的
provider/fixed-model launcher，是 SGLang/CUDA 运行时底座和历史产物；仅替换 Lite
Go binary 不会自动让它获得 native 入口行为。native 镜像标签为
`sglang-v0.5.19-cu129-native`，由本分支源码构建，并内置原生后端版本。
首次迁移入口后，日常只需更新 Lite binary，无需重建
SGLang/CUDA runtime。历史 tag 和 digest 不会被修改。

## 启动（native managed，默认）

从仓库根目录启动。`MODEL_DIR` 可选；不设置时容器启动后通过 UI/API 下载或挂载模型。

```bash
export CSGLITE_SGLANG_IMAGE=opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/csglite:sglang-v0.5.19-cu129-native
docker compose -f docker/sglang/compose.cuda.yaml pull
docker compose -f docker/sglang/compose.cuda.yaml up -d
docker compose -f docker/sglang/compose.cuda.yaml logs -f
```

容器启动后打开 `http://localhost:11435`，在模型库中选择 Safetensors 对话模型，
推理后端选择 SGLang（或自动）。也可以加入集群，由 Master 在节点卡片中查看该
节点的引擎能力并下发托管部署。

### 通过环境变量加入公网 Master

公网 Worker 使用独立的 `compose.worker.yaml`，无需 LAN advertise IP 或入站集群
端口；管理 API 只监听容器内 `127.0.0.1:11435`，从 Master UI 管理该节点。

```bash
export CSGHUB_LITE_CLUSTER_GATEWAY_URL=https://master.example.com:8080
export CSGHUB_LITE_CLUSTER_JOIN_TOKEN='<集群加入凭据>'
docker compose -f docker/sglang/compose.cuda.yaml -f docker/sglang/compose.worker.yaml up -d
```

加入前须将已授权的 `license.key` 放入持久化数据目录。使用自签发证书时，将可信
CA 放入同一数据目录，例如 `data/certs/gateway-ca.pem`，并设置容器内路径
`CSGHUB_LITE_CLUSTER_TLS_CA_FILE=/root/.csghub-lite/certs/gateway-ca.pem`。
不关闭 TLS 验证。加入凭据只通过环境传给 Lite，不写入 `config.json`。

容器支持以下环境变量，并复用已有 `config.json.cluster` 字段：

| 环境变量后缀（前缀 `CSGHUB_LITE_CLUSTER_`） | 既有配置字段 |
| --- | --- |
| `MODE` | `mode`，`lan` / `worker` / `gateway` |
| `GATEWAY_URL` | `gateway_url` |
| `TLS_CA_FILE` | `tls_ca_file` |
| `PUBLIC_LISTEN_ADDR` | `public_listen_addr` |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | `tls_cert_file` / `tls_key_file` |

设置 `worker` 或 `gateway` 时不可同时配置 LAN `CLUSTER_SECRET`。已有 LAN 成员需
先通过 Lite 离群，再切换拓扑；launcher 不会删除已有身份或强制切换成员关系。
原 `compose.cluster.yaml` 保留 LAN 部署方式。

## 参数策略

native 模式不再沿用固定服务的 `8192`/`0.8` 调参。默认值来自模型元数据、请求和
目标节点可见资源：

- **后端**：GGUF 默认 llama.cpp；原生 Safetensors 默认 SGLang（已安装/已配置时）。
  显式选择 SGLang 而运行时不可用时直接报错，绝不静默转换 GGUF。
- **GPU**：默认只用 1 张卡（`--tp-size 1`）。只有显式设置
  `SGLANG_TP_SIZE` 或在 `SGLANG_EXTRA_ARGS` 中给出 `--tp-size` 才张量并行。
  `CUDA_VISIBLE_DEVICES` 的顺序会被保留，`--base-gpu-id` 会参与显存估算。
- **上下文**：请求值 > 每模型设置 > 模型声明的最大值（默认上限 16K），并受
  `--context-length` 显式覆盖。SGLang 的 `--max-total-tokens` 按上下文 ×
  `num_parallel` 计算；显式 `--max-total-tokens` 小于上下文会被拒绝。
- **并发**：`num_parallel` 映射为 `--max-running-requests`，按
  请求 > 每模型设置 > 全局设置 > 1 解析。
- **显存**：`--mem-fraction-static` 由权重、KV 池（含 `head_dim`/嵌套
  `text_config`/KV dtype 宽度）和至少 1.5GiB 或 3% 显存的运行时余量估算，
  避免小模型占满整张 80GB 卡。该值是估算，实际占用随模型、请求和算子缓存变化。
- **dtype**：遵循 SGLang 的 auto 规则：BF16 权重用 BF16，FP16 用 FP16，
  FP32 权重按 FP16 运行。显式 `f16/bf16/f32` 或 `--dtype` 会保留；GGUF 量化名
  在 SGLang 上会被明确拒绝。
- **编译并发**：按容器 cgroup 的 CPU 和内存计算 `MAX_JOBS`（4 CPU/8GiB 容器
  默认 2），显式 `MAX_JOBS` 永远优先。
- **工具调用**：根据模型家族、版本和 tokenizer chat template 选择
  `--tool-call-parser`（如 Qwen3.5/Coder、Qwen2.5/3、DeepSeek、Llama 3、
  GLM 等），必要时选择 `--reasoning-parser`。未知/未验证或 base 模型不会宣称
  支持函数调用；请求 tools 时会得到明确拒绝，而不是返回无法执行的文本。

高级参数可通过 `SGLANG_EXTRA_ARGS`（JSON 字符串数组，支持
`["--flag","value"]` 和 `["--flag=value"]`）覆盖，但 `--model-path`、`--host`、
`--port`、`--served-model-name`、`--api-key` 等身份参数由 CSGLite 管理，不能覆盖；
重复的管理参数会被拒绝。计划中的生效值会通过模型设置和节点状态 API 返回。

## 存储与缓存

进程临时文件、日志、HF/SGLang/FlashInfer/Triton/torch extension/CUDA/bytecode
缓存全部位于配置的存储根目录（默认 `/root/.csghub-lite`）。容器不再要求宿主
`/tmp` 或固定缓存路径。

## Legacy fixed-model 模式（显式 opt-in）

只有需要“一个容器固定服务一个模型并通过 provider 暴露”的旧行为时，才设置：

```bash
SGLANG_LEGACY_FIXED_MODEL=1
SGLANG_MODEL_PATH=/absolute/path/to/complete-hf-model
SGLANG_MODEL_NAME=local-sglang
```

此时 launcher 启动一个固定 SGLang、注册 `SGLang` provider，并设置
`CSGHUB_LITE_SGLANG_DISABLE=1`，避免 native 模式再启动第二个进程。未设置
`SGLANG_LEGACY_FIXED_MODEL=1` 时，launcher 只启动 CSGLite，不要求模型路径。

## 节点与集群语义

节点状态通过 `engines` 上报每个引擎的 `known/installed/available/running`、
版本、支持格式/任务、CUDA/ROCm、PyTorch 可见设备和不可用原因。旧节点不上报该
字段时 UI 显示 **unknown/unreported**，不会假设已安装或可用；首次探测尚未完成时
同样显示 unknown，而不是 not installed。Master 的部署 UI 使用**目标节点**的引擎
能力、GPU/VRAM 和模型格式推荐后端与参数，不使用 Master 本地运行时。

## 限制

- 已在 A100-SXM4-80GB 上验证原生 Safetensors 自动选择 SGLang、启动/停止、
  普通和流式聊天。Qwen2.5-1.5B-Instruct 在 4 CPU/8GiB 容器中使用 BF16/16K/TP1，
  单工具调用、SSE 分片、工具结果回传及 `auto/required/指定函数` 通过；停止后显存归零。
- 同一 1.5B 模型的双城市并行调用未通过，直接请求 SGLang 也复现；不保证模型的
  多工具并行生成质量。AMD MI210 尚未进行实卡验收。
- native SGLang 仅支持 Linux + CUDA/ROCm；macOS/Windows 继续使用 llama.cpp。
- 仅保证文本生成/对话。Embedding、ASR、TTS、图像模型不会自动路由到 SGLang。
- SGLang 的 `--tool-call-parser` 支持取决于模型家族与 chat template；未验证模型
  不宣称函数调用可用。
- 集群自动选择节点会优先匹配目标引擎；旧节点的 unknown 语义不会被当作可用。

## 参考

- 镜像构建：`Dockerfile`、`Dockerfile.cuda`、`compose.cuda.yaml`、`compose.rocm.yaml`
- 公网 Worker：`compose.worker.yaml`；LAN：`compose.cluster.yaml`
- launcher：`launcher.py`（native 默认，legacy 显式 opt-in）
- 集群部署：`docs/guides/cluster-model-deployments.md`
- 环境变量：`docs/guides/environment-variables.md`
