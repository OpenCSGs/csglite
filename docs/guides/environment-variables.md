# 环境变量参考

本文集中列出 CSGLite 提供的环境变量。除安装器变量外，修改环境变量后需要重启
`csghub-lite` 服务才能生效。

Linux / macOS 示例：

```bash
export CSGHUB_LITE_ROCM_SINGLE_ENGINE=0
csghub-lite serve
```

Windows PowerShell 示例：

```powershell
$env:CSGHUB_LITE_ROCM_SINGLE_ENGINE = "0"
csghub-lite serve
```

布尔变量通常接受 `1`、`true`、`yes`、`on` 和
`0`、`false`、`no`、`off`。安装脚本中明确标注为 `1` 的开关除外。

## 服务和界面

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `CSGHUB_LITE_SERVER_URL` | `https://hub.opencsg.com` | CSGHub 服务地址。仅在配置文件未设置 `server_url` 时作为默认值。 |
| `CSGHUB_LITE_AI_GATEWAY_URL` | `https://ai.space.opencsg.com` | AI Gateway 地址。仅在配置文件未设置 `ai_gateway_url` 时作为默认值。 |
| `CSGHUB_LITE_CLOUD_PROVIDER_NAME` | `csghub` | 云端模型 provider 名称。仅在配置文件未设置时生效。 |
| `CSGHUB_LITE_OPENAI_STREAM_DEFAULT` | `false` | OpenAI Chat Completions 请求未传 `stream` 时是否默认使用流式响应。请求参数优先。 |
| `CSGHUB_LITE_HIDDEN_NAV_ITEMS` | 空 | 用逗号分隔要隐藏的 Web UI 导航项。可用项：`dashboard`、`marketplace`、`library`、`datasets`、`chat`、`images`、`ai-apps`、`ai-gateway`、`settings`、`pricing`、`help`。只隐藏入口，不禁用 URL。 |
| `CSGHUB_LITE_LOG_STDERR` | `1` | 设置为 `0` 时停止向标准错误输出日志。 |
| `CSGHUB_LITE_DISABLE_FILE_LOGGING` | 空 | 设置为任意非空值时禁用 `~/.csghub-lite/logs/` 文件日志。 |
| `CSGHUB_LITE_REGION` | 自动检测 | 区域提示。常用值为 `CN` 或 `intl`，影响安装下载源、升级源、转换依赖和 Python 包镜像。 |

## llama.cpp 本地推理

请求中的显式运行参数优先于对应的环境变量。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `CSGHUB_LITE_LLAMA_SERVER` | 自动查找 | 指定 `llama-server` 可执行文件的绝对路径。 |
| `CSGHUB_LITE_LLAMA_READY_TIMEOUT` | 按模型大小计算 | 等待 `llama-server` 就绪的超时。支持 Go duration（如 `45m`、`90s`）或秒数。自动值为 2 分钟加每 GiB 约 1 分钟，范围 2–45 分钟；无法读取文件时为 20 分钟。 |
| `CSGHUB_LITE_LLAMA_NUM_CTX` | 模型感知，通常 `8192` 或 `16384` | 默认上下文长度，最小有效值为 `1024`。模型声明至少 16384 时默认最多扩展到 16384。 |
| `CSGHUB_LITE_LLAMA_USE_MODEL_MAX_CTX` | `false` | 未显式设置 `num_ctx` 时，使用模型元数据声明的最大上下文。可能显著增加内存占用。 |
| `CSGHUB_LITE_LLAMA_NUM_PARALLEL` | `1` | 单个 `llama-server` 的并行槽位数，不是同时加载的模型数量。实际 `--ctx-size` 为每槽上下文乘以该值。 |
| `CSGHUB_LITE_LLAMA_EMBEDDING_POOLING` | 按模型族选择 | 强制 embedding pooling，例如 `last`、`cls` 或 `mean`。Qwen3 Embedding 默认 `last`。 |
| `CSGHUB_LITE_ROCM_SINGLE_ENGINE` | ROCm 主机为开启 | ROCm 主机默认只保留一个 llama 文本/Embedding 引擎。设置为 `0` 可允许多个模型同时加载，但会增加显存压力和 ROCm 崩溃风险。 |
| `CSGHUB_LITE_ROCM_UNIFIED_MEMORY` | AMD APU 自动开启 | 控制 `GGML_CUDA_ENABLE_UNIFIED_MEMORY`。仅 APU 默认开启；ROCm 独显默认关闭。 |
| `CSGHUB_LITE_CONVERTER_URL` | 内置转换器 | 覆盖 `convert_hf_to_gguf.py` 下载地址。通常只用于镜像测试或版本调试。 |

未显式设置 GPU 层数时，CSGLite 不向当前 `llama-server` 传递 `-ngl`，
由 llama.cpp 根据空闲设备内存自动 fit。GPU 层数目前通过 API/CLI 运行参数设置，
没有对应环境变量。

## Python 模型运行时和包镜像

这些变量影响 Diffusers、ASR、Python Embedding 及模型转换环境中的依赖安装。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `CSGHUB_LITE_PACKAGE_MIRROR` | `aliyun` | Python 包镜像策略。`aliyun`/`cn` 使用阿里云；`official`/`global` 使用官方源。 |
| `CSGHUB_LITE_TORCH_INDEX_URL` | 按硬件和镜像选择 | 完整覆盖 PyTorch pip index URL，同时停用自动配置的 find-links。 |
| `CSGHUB_LITE_PYPI_INDEX_URL` | 按镜像选择 | 完整覆盖普通 Python 包的 pip index URL。 |

ASR worker 还支持以下高级调优变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `CSGHUB_ASR_CHUNK_SECONDS` | `30` | FunASR 长音频分块秒数。 |
| `CSGHUB_ASR_LONG_AUDIO_THRESHOLD_SECONDS` | 与分块秒数相同 | 超过该时长后启用长音频分块。 |
| `CSGHUB_ASR_USE_VAD` | `false` | 是否为 FunASR 启用 VAD。 |
| `CSGHUB_ASR_VAD_MODEL` | `fsmn-vad` | VAD 模型名称。 |
| `CSGHUB_ASR_VAD_MAX_SEGMENT_MS` | `30000` | VAD 单段最大毫秒数。 |
| `FUNASR_TRUST_REMOTE_CODE` | `false` | 是否允许 FunASR 模型执行远程自定义代码。仅对可信模型开启。 |

## 主安装脚本

这些变量用于 `install.sh` 或 `install.ps1`，只影响安装过程。部分底层安装选项
当前仅由 Linux/macOS shell 安装器使用。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `CSGHUB_LITE_VERSION` | 最新版本 | 安装指定 CSGLite release，例如 `v0.9.35`。 |
| `CSGHUB_LITE_FORCE` | 空 | 设置为 `1` 时跳过已有安装确认并强制继续。 |
| `CSGHUB_LITE_LLAMA_CPP_TAG` | 与内置转换器锁定的 tag | 指定安装的 llama.cpp release tag。随版本更新，不建议单独覆盖。 |
| `CSGHUB_LITE_LLAMA_ROCM_VERSION` | 自动检测 | 指定 Linux ROCm 包版本（`主版本.次版本`），用于自动检测失败时。 |
| `CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER` | `1` | 设置为非 `1` 时不自动安装 `llama-server`。 |
| `CSGHUB_LITE_LLAMA_CPP_INSTALL_CMD` | 空 | 自定义 llama.cpp 安装命令。命令执行后仍需能找到 `llama-server`。 |
| `CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR` | 与 CSGLite 可执行文件同目录或系统 bin | 指定 llama.cpp 二进制和库的安装目录；当前用于 shell 安装器。 |
| `CSGHUB_LITE_AUTO_INSTALL_CUDA_LIBS` | `1` | Linux NVIDIA 安装时是否自动补齐缺失的 CUDA 用户态运行库；当前用于 shell 安装器。 |
| `CSGHUB_LITE_AUTO_INSTALL_PATCHELF` | `1` | Linux 上缺少 `patchelf` 时是否尝试自动安装；当前用于 shell 安装器。 |

安装时也会读取 `CSGHUB_LITE_REGION` 来选择 GitHub 或内部镜像优先级。

## AI Apps

以下变量主要用于镜像测试、私有镜像或自定义安装位置。普通用户无需设置。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `CSGHUB_LITE_DOCKER_PATH` | 自动查找 | 指定 Docker CLI 路径，供小智等 Docker 应用使用。 |
| `CSGHUB_LITE_CLAUDE_DIST_BASE_URL` | 内置版本化镜像 | Claude Code 发布包基础 URL。 |
| `CSGHUB_LITE_OPEN_CODE_DIST_BASE_URL` | 内置版本化镜像 | OpenCode 发布包基础 URL。 |
| `CSGHUB_LITE_OPEN_CODE_REVIEW_DIST_BASE_URL` | 内置版本化镜像 | Open Code Review 发布包基础 URL。 |
| `CSGHUB_LITE_CODEX_DIST_BASE_URL` | 内置版本化镜像 | Codex CLI 发布包基础 URL。 |
| `CSGHUB_LITE_CODEX_APP_DIST_BASE_URL` | 内置版本化镜像 | Codex App 发布包基础 URL。 |
| `CSGHUB_LITE_CSGCLAW_LATEST_URL` | CSGClaw 官方 latest 地址 | 覆盖 CSGClaw 最新版本元数据地址。 |
| `CSGHUB_LITE_ZCODE_SITE_URL` | ZCode changelog | ZCode 版本发现页面。 |
| `CSGHUB_LITE_ZCODE_DIST_BASE_URL` | ZCode 国内 CDN | ZCode 安装包基础 URL。 |
| `CSGHUB_LITE_PI_PACKAGE` | `@mariozechner/pi-coding-agent@latest` | Pi 的 npm 包规格。 |
| `CSGHUB_LITE_PI_INSTALL_ROOT` | `~/.local/share/pi-coding-agent` | Pi 用户级安装目录。 |
| `CSGHUB_LITE_TMPDIR` | `~/.csghub-lite/tmp/...` | 直接运行部分内置应用安装脚本时覆盖临时目录。由 CSGLite 启动脚本时会自动设置到存储根下。 |

## 开发和内部保留变量

| 变量 | 用途 |
| --- | --- |
| `CSGHUB_LITE_API_TARGET` | Web/Vite 开发服务器代理目标；默认 `http://localhost:11435`。不影响发布版 Web UI。 |
| `CSGHUB_LITE_RESTART_PARENT` | 升级重启流程内部使用。不要手动设置。 |
| `CSGHUB_LITE_USER_SHELL` | AI App shell 启动器内部使用。不要手动设置。 |

`PATH`、`HOME`、`TMPDIR`、代理变量、系统 locale，以及 `GITHUB_TOKEN` /
`GITLAB_TOKEN` 等通用外部工具变量不属于 CSGLite 专用环境变量，因此不在本表中。
