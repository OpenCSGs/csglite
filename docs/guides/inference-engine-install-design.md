# 推理引擎可选安装与可插拔设计

## 1. 背景与目标

目前 CSGLite 的安装器把 `llama-server` 当成必装组件：

- `scripts/install.sh` 的第 6 步 `install_llama_server` 和 `scripts/install.ps1`
  的 `Install-LlamaServer` 无条件执行，唯一的关闭方式是
  `CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER=0`，而且关闭后没有任何后续引导。
- 运行时（`internal/inference/llama.go`）、`csghub-lite uninstall`、Docker 镜像、
  Homebrew formula、安装文档都直接写死 `llama-server`。
- 用户如果只想把 CSGLite 当作远程模型网关、AI Apps 启动器、数据集工具或语音
  （Python 运行时）服务来用，仍然要下载几百 MB 的 llama.cpp 包，在 Linux 上还会
  触发 patchelf、CUDA 运行库等额外安装。

本设计把“推理引擎”做成一个**可选、可枚举、可插拔**的组件：

1. 安装时可以选择安装 llama.cpp，也可以选择不安装任何推理引擎。
2. 不装引擎时 CSGLite 仍然可以正常安装、启动、使用不依赖本地推理的功能，
   并在需要本地推理时给出明确、可执行的安装提示。
3. 后续加入第二个引擎（例如 Linux/NVIDIA 上的 vLLM、macOS 上的 MLX、
   ik_llama.cpp 等）只需要按同一套约定新增一个实现，而不是再改一遍安装器、
   运行时、卸载、文档。

非目标：

- 不在本设计里实现第二个引擎，只保证抽象能容纳它。
- 不改变 llama.cpp 版本锁步规则（`docs/agent-guidelines/llama-cpp.md`）。
- 不把 Diffusers / ASR / TTS 这些 Python 运行时改成新的抽象；它们已经有按需
  安装流程，第 4 节说明二者的关系。

## 2. 核心概念

### 2.1 两层“引擎”

仓库里已有 `internal/inference.Engine`，它表示**一个已加载模型的请求级后端**
（Chat / Generate / Embeddings 代理）。本设计新增的是**生命周期级**的概念：一个
可以被检测、安装、升级、卸载的软件包。为避免混淆，命名区分如下：

| 层 | Go 类型 | 关注点 | 例子 |
|---|---|---|---|
| 请求级 | `inference.Engine` | 某个模型进程怎样回答请求 | `llamaEngine`、`openaiEngine`、`remoteEngine` |
| 生命周期级（本设计新增） | `engine.Provider` | 引擎软件本身是否装了、装在哪、什么版本、怎么装 | `llama.cpp`，未来 `vllm`、`mlx` |

`api.LocalInferenceSupport.Runtime` 目前的取值 `llama` / `diffusers` /
`python-embedding` / `python-asr` / `python-tts` 已经是“某个模型该交给谁跑”的路由
键。`Provider.Runtimes()` 声明自己能服务哪些路由键，从而把“模型 → 运行时 →
引擎”串起来。

### 2.2 引擎标识

引擎 ID 是小写、稳定的字符串，在安装器环境变量、CLI、API、配置、目录名里全部
一致：

| ID | 含义 | 状态 |
|---|---|---|
| `llama.cpp` | ggml-org/llama.cpp 的 `llama-server` 及其共享库 | 现有，第一阶段接入 |
| `none` | 明确表示不安装任何推理引擎 | 第一阶段 |
| `auto` | 由安装器根据平台选推荐引擎，当前等于 `llama.cpp` | 第一阶段，默认值 |
| `vllm`、`mlx`、… | 未来引擎 | 预留，见第 7 节 |

## 3. 用户可见行为

### 3.1 安装脚本：选择引擎

新增统一变量 `CSGHUB_LITE_INFERENCE_ENGINE`，取值为引擎 ID 或逗号分隔的多个
ID：

```sh
# 默认：auto → llama.cpp
curl -fsSL https://hub.opencsg.com/csghub-lite/install.sh | sh

# 不安装推理引擎
curl -fsSL https://hub.opencsg.com/csghub-lite/install.sh | CSGHUB_LITE_INFERENCE_ENGINE=none sh

# 显式选择（未来可写 llama.cpp,vllm）
curl -fsSL https://hub.opencsg.com/csghub-lite/install.sh | CSGHUB_LITE_INFERENCE_ENGINE=llama.cpp sh
```

Windows 对应 `-Engine` 参数和同名环境变量：

```powershell
irm https://hub.opencsg.com/csghub-lite/install.ps1 | iex
& ([scriptblock]::Create((irm https://hub.opencsg.com/csghub-lite/install.ps1))) -Engine none
```

**交互式选择**。只在同时满足以下条件时弹出：

- `CSGHUB_LITE_INFERENCE_ENGINE` 未设置，且 `CSGHUB_LITE_FORCE` 不为 `1`；
- 能读到终端（沿用 `check_existing` 的做法：`[ -t 0 ]` 或 `/dev/tty` 可用；
  `curl | sh` 场景 stdin 是管道，必须从 `/dev/tty` 读，否则会把脚本正文当输入）；
- 本机**没有**检测到任何已安装的引擎。已装过 llama-server 的机器重跑安装器
  属于升级场景，保持现在的静默升级行为，不打断。

提示文案（英文，与安装器其余输出一致），回车或无终端时取默认项 1：

```text
Select an inference engine to install:
  1) llama.cpp   llama-server b10830 for GGUF / SafeTensors text, embedding and vision models (recommended)
                 detected accelerator: CUDA 12.x
  2) none        skip for now; remote and cloud models still work, install later with
                 `csghub-lite engine install llama.cpp`
Choice [1]:
```

“detected accelerator”一行复用安装器已有的 CUDA / ROCm / Metal / CPU 判断，
让用户知道选 1 会装哪一个变体。

**兼容旧变量**。`CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER=0` 继续有效，等价于
`CSGHUB_LITE_INFERENCE_ENGINE=none`，并打印一条弃用提示；两者同时设置时新变量
优先。`CSGHUB_LITE_LLAMA_CPP_TAG`、`CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR`、
`CSGHUB_LITE_LLAMA_CPP_INSTALL_CMD`、`CSGHUB_LITE_AUTO_INSTALL_CUDA_LIBS`、
`CSGHUB_LITE_AUTO_INSTALL_PATCHELF` 是 llama.cpp 这一引擎的私有参数，含义不变。

**其它分发渠道**：

- Homebrew formula 已是 `depends_on "llama.cpp" => :recommended`，用户可以
  `brew install --without-llama.cpp opencsgs/csglite/csghub-lite`，等价于 `none`，
  文档补一句即可。
- `docker/cpu` 与 `docker/rocm` 的 `CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER=1`
  改为 `CSGHUB_LITE_INFERENCE_ENGINE=llama.cpp`。
- 源码编译 / 手动解压的用户本来就没有引擎，这正是 `csghub-lite engine install`
  要服务的场景。

**安装结束的摘要**要明确报告引擎结果，例如：

```text
✔ csghub-lite v0.9.38 installed successfully!
  Inference engine: none (skipped). Local models need one:
    csghub-lite engine install llama.cpp
```

### 3.2 CLI：`csghub-lite engine`

```text
csghub-lite engine list                 # 所有已知引擎及其状态（installed / not installed / update available）
csghub-lite engine show llama.cpp       # 路径、版本、锁定版本、加速后端、是否由 csghub-lite 管理
csghub-lite engine install llama.cpp    # 安装或升级到锁定版本
csghub-lite engine upgrade [ID]         # 升级已安装引擎；不传 ID 时升级全部
csghub-lite engine remove llama.cpp     # 只删除由 csghub-lite 管理的文件
```

`engine install` 是所有入口（安装脚本、Web UI、`csghub-lite upgrade`）最终收敛的
单一实现，见第 5 节和第 8 节的分期说明。

### 3.3 API 与 Web UI

- `GET /api/engines` 返回 `[]api.EngineStatus`（字段见 5.2）。Dashboard 和
  Settings 页据此渲染。
- 未安装任何引擎时，Dashboard 顶部显示一条可关闭的横幅：“未安装本地推理引擎，
  远程模型可正常使用；运行本地模型前请安装 llama.cpp”，附安装命令；
  第二阶段起横幅上直接提供“安装”按钮，进度通过与
  `POST /api/image-runtime/install` 相同的 SSE 形式推送。
- Settings 页新增“推理引擎”卡片：每个已知引擎一行，显示状态、版本、路径；
  现有的“上下文长度 / 并行槽位”等 llama 参数移到该卡片下作为 llama.cpp 的
  子项，为将来每个引擎各有参数留位置。
- Marketplace / Library 里模型详情已经显示 `local_inference.supported`；当对应
  `Runtime` 的引擎未安装时，把“运行”按钮的提示改为“需要安装 llama.cpp”，
  而不是点下去才失败。
- `pull` / 下载模型不依赖引擎，保持可用。

### 3.4 没有引擎时的运行时行为

| 功能 | `none` 下是否可用 |
|---|---|
| Web UI、Dashboard、Settings、登录、`csghub-lite serve` | 可用 |
| 模型搜索、下载、删除、Library 管理 | 可用 |
| 远程 / 云端模型（`remote.go`、`openai.go`）、AI Gateway | 可用 |
| AI Apps（Claude Code、OpenCode、Codex 等）的安装与启动 | 可用（它们连远程模型或已配置的 provider） |
| 数据集下载、导出 | 可用 |
| 图像生成、ASR、TTS、Python embedding | 可用，走各自的 Python 运行时，与本设计无关 |
| 本地 GGUF / SafeTensors 文本、embedding、视觉模型 | **不可用**，返回结构化错误 |

结构化错误统一为：

```json
{
  "error": "no inference engine is installed for runtime \"llama\"",
  "code": "engine_not_installed",
  "engine": "llama.cpp",
  "hint": "Run `csghub-lite engine install llama.cpp`, or re-run the installer with CSGHUB_LITE_INFERENCE_ENGINE=llama.cpp."
}
```

CLI 的 `run` / `chat` 打印同样的 hint；Web UI 依据 `code` 弹出安装引导而不是
展示原始错误字符串。现在 `llama.go` 第 508 行那段多行提示改为由 Provider 生成
`hint`，保证 CLI、API、Web 三处措辞一致。

## 4. 与 Python 运行时的关系

Diffusers、ASR、TTS、Python embedding 已经由 `imagegen.RuntimeManager` 按需安装，
并且有自己的进度 API。第一阶段**不**把它们改造成 `engine.Provider`，原因是：

- 它们不是“可选装或不装”的产品决策，而是按模型类型自动补齐的依赖；
- 它们共享一个 uv venv，生命周期和二进制型引擎差别很大。

但 `Provider` 接口按“能同时容纳二进制包和 Python venv”来设计（`Install` 只要求
上报进度、`Detect` 只要求返回状态），这样将来若希望在 Settings 页用同一张卡片
展示所有运行时，只需给 `RuntimeManager` 加一层适配，不必再改接口。

## 5. Go 侧设计

### 5.1 `internal/engine` 包

```go
package engine

// Provider 描述一种可安装的推理引擎。
type Provider interface {
    ID() string              // "llama.cpp"
    DisplayName() string     // "llama.cpp (llama-server)"
    Runtimes() []string      // 它能服务的 LocalInferenceSupport.Runtime 值，如 {"llama"}
    Supports(goos, goarch string) bool

    // Detect 只读取本机状态，不联网、不写文件。
    Detect(ctx context.Context) (Status, error)

    // Install 安装或升级到 Provider 的锁定版本；进度用于 CLI 和 SSE。
    Install(ctx context.Context, opts InstallOptions, progress ProgressFunc) error

    // Remove 只删除 manifest 记录为 csghub-lite 管理的文件。
    Remove(ctx context.Context) error

    // InstallHint 返回用户在缺少该引擎时应执行的一句话命令。
    InstallHint() string
}

type InstallOptions struct {
    Dir         string // 为空时用 Provider 默认目录
    Version     string // 为空时用锁定版本
    Accelerator string // 为空时自动检测：cuda / rocm / vulkan / metal / cpu
}

type Registry struct{ ... } // 按 ID 注册；Lookup(id)、ForRuntime(runtime)、All()
```

`Status` 与 API 类型 `api.EngineStatus` 字段一致：

```go
type Status struct {
    ID              string `json:"id"`
    DisplayName     string `json:"display_name"`
    Installed       bool   `json:"installed"`
    Path            string `json:"path,omitempty"`
    Version         string `json:"version,omitempty"`         // 本机版本，如 b10830
    PinnedVersion   string `json:"pinned_version,omitempty"`  // 本二进制锁定的版本
    UpToDate        bool   `json:"up_to_date"`
    Managed         bool   `json:"managed"`                   // 是否由 csghub-lite 安装
    Accelerator     string `json:"accelerator,omitempty"`     // cuda / rocm / metal / cpu
    SupportedHere   bool   `json:"supported_here"`            // 当前 OS/Arch 是否支持
    InstallHint     string `json:"install_hint,omitempty"`
    Runtimes        []string `json:"runtimes"`
}
```

### 5.2 llama.cpp Provider

- `Detect` 复用现有 `findLlamaBinary` 的查找顺序（`CSGHUB_LITE_LLAMA_SERVER` →
  与 `csghub-lite` 同目录 → PATH → 常见目录），版本解析复用安装脚本里
  “`version: <n> (<hash>)`，忽略 ≤100 的浅克隆构建号”这套规则，在 Go 里实现一次，
  加单元测试，并让安装脚本第二阶段起改为调用 `csghub-lite engine show --json`
  来判断是否需要升级。
- `PinnedVersion` 直接取 `llama-cpp-assets` 模块导出的 `LlamaCppRef`，与
  `internal/convert/bundled_converter.go` 同源。这样锁步表面从三处（模块、
  converter、安装脚本默认值）减少到两处；安装脚本里的 `LLAMA_CPP_DEFAULT_TAG`
  在第二阶段可以删掉，`internal/convert/version_lockstep_test.go` 相应调整。
- `Install` 第一阶段的实现见第 8 节；第二阶段把 `install.sh` / `install.ps1` 里
  的资产选择（CPU / CUDA / ROCm / Vulkan、GitHub 与 GitLab 镜像、cudart、
  patchelf `$ORIGIN`、macOS `install_name_tool`）移植为 Go，安装脚本转而调用
  `csghub-lite engine install`。

### 5.3 安装清单（manifest）

由 csghub-lite（或第一阶段的安装脚本）安装的引擎，在存储根目录写一份清单：

```text
~/.csghub-lite/engines/<id>/manifest.json
{
  "id": "llama.cpp",
  "version": "b10830",
  "accelerator": "cuda",
  "install_dir": "/usr/local/bin",
  "files": ["/usr/local/bin/llama-server", "/usr/local/bin/libggml-cuda.so", ...],
  "installed_by": "install.sh",   // install.sh | install.ps1 | cli | web
  "installed_at": "2026-09-15T10:00:00Z"
}
```

用途：

- `Status.Managed` 的依据。`csghub-lite uninstall` 和 `engine remove` **只**
  删除清单里列出的文件。当前 `uninstall` 会删除 PATH 里找到的任何 llama-server
  和同目录共享库，这是一个行为变化：用户自己 `brew install llama.cpp` 的副本不再
  被误删，需要写进发布说明。
- 无清单但检测到二进制 → `Managed=false`，显示为“外部安装”，可以用但不会被升级
  或删除。

### 5.4 接入点

| 位置 | 改动 |
|---|---|
| `inference.loadEngineWithProgressMode` | 通过 `Registry.ForRuntime(support.Runtime)` 取 Provider；`Detect` 未安装时返回 `ErrEngineNotInstalled{Engine, Hint}`，替代现在 `llama.go` 里手写的多行错误 |
| `server` | 新增 `GET /api/engines`；错误中间层把 `ErrEngineNotInstalled` 映射为 3.4 节的 JSON（HTTP 503） |
| `cli` | 新增 `engine` 子命令；`run` / `chat` 捕获该错误打印 hint；`uninstall` 改为遍历 Registry 并只删 Managed 文件；`upgrade` 完成后对每个 Managed 引擎调用 `Install`，保持“升级 csghub-lite 顺带把引擎对齐到新锁定版本”的现有体验 |
| `config` | 只新增 `inference.engine`（首选引擎 ID，空表示自动），用于将来一个 Runtime 有多个引擎可选时做默认选择；**不**把“已安装”状态写进 `config.json`，那是磁盘事实，由 `Detect` 得出 |
| Web `Settings.tsx` / Dashboard | 引擎卡片、横幅、结构化错误引导；文案进 i18n |
| `openapi/local-api.json` | 新增 `/api/engines` 与 `EngineStatus`；跑 `go test ./internal/server` |

## 6. 安装脚本内部结构

无论第几阶段，`install.sh` / `install.ps1` 的第 6 步都改为一个分发器，而不是一个
写死 llama 的函数：

```sh
KNOWN_ENGINES="llama.cpp"            # 新增引擎时追加，与 Go Registry 保持一致

resolve_engines() {                  # 读取新旧变量、交互选择、auto 展开，输出以空格分隔的 ID 列表或 none
    ...
}

install_engines() {
    for _id in $(resolve_engines); do
        case "$_id" in
            none) info "Inference engine: none (skipped)."; write_engine_summary none ;;
            llama.cpp) engine_llama_cpp_install ;;
            *) warn "Unknown inference engine '${_id}'. Known: ${KNOWN_ENGINES}" ;;
        esac
    done
}
```

每个引擎在脚本里遵守同一组函数命名：`engine_<id>_detect`、
`engine_<id>_describe`（交互菜单里那一行说明）、`engine_<id>_install`、
`engine_<id>_write_manifest`。现有 `install_llama_server` 整体改名为
`engine_llama_cpp_install`，内部逻辑不动，只在末尾补写 manifest。PowerShell 侧
函数名对应 `Engine-LlamaCpp-Install` 等。

第二阶段起，`engine_<id>_install` 的主体退化为：

```sh
engine_llama_cpp_install() {
    "$TARGET" engine install llama.cpp --installed-by install.sh
}
```

安装脚本不再维护资产选择逻辑，Windows 的 `install.ps1` 也同步收敛。

## 7. 新增一个引擎需要做什么

这是可插拔性的验收标准，新增引擎的 PR 应当只包含下面这些改动：

1. **Go**：在 `internal/engine/<id>/` 实现 `Provider`，在
   `internal/engine/registry.go` 注册；补 `Detect` / 版本解析 / `Supports` 的单元
   测试。
2. **路由**：若引擎服务新的 `Runtime` 值，在 `internal/localinference/support.go`
   增加判定，并在 `inference` 包实现对应的请求级 `inference.Engine`（多数候选引擎
   如 vLLM、MLX、SGLang 都提供 OpenAI 兼容 HTTP 接口，可复用 `openai.go` 的代理，
   新增工作主要是进程启动与健康检查）。
3. **安装脚本**：`KNOWN_ENGINES` 加 ID、加 `engine_<id>_describe`（第一阶段还要
   加 `engine_<id>_install`；第二阶段只需描述行）。`install.ps1` 同步。
4. **文档**：`docs/getting-started/installation.md` 的引擎表、
   `docs/guides/environment-variables.md` 的引擎私有变量、README 的功能矩阵。
5. **不需要**改 `uninstall`、`upgrade`、`/api/engines`、Settings 卡片、错误提示，
   它们都遍历 Registry。

以两个最可能的候选为例，说明接口需要覆盖的差异：

| | vLLM（Linux + NVIDIA） | MLX（macOS Apple Silicon） |
|---|---|---|
| 安装形态 | uv 管理的 venv，`~/.csghub-lite/engines/vllm/` | uv venv，`~/.csghub-lite/engines/mlx/` |
| 模型格式 | 直接吃 SafeTensors，不需要 GGUF 转换 | MLX 自有格式或 SafeTensors |
| 请求级接口 | OpenAI 兼容服务器 | `mlx_lm.server` OpenAI 兼容 |
| `Supports` | `linux/amd64` 且检测到 CUDA | `darwin/arm64` |
| 与 llama.cpp 共存 | 同一模型两种 Runtime 时由 `inference.engine` 配置决定默认 | 同左 |

安装目录约定：llama.cpp 保持现状（与 `csghub-lite` 同目录或
`CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR`），因为用户直接运行 `llama-server` 是既有
用法，且 `findLlamaBinary` 已优先查同目录；非单二进制的引擎默认落在存储根目录
`~/.csghub-lite/engines/<id>/`，符合“运行时文件都放存储根目录”的仓库规则。

## 8. 分期计划

### 第一阶段：可选安装 + 状态可见

- 安装脚本：`CSGHUB_LITE_INFERENCE_ENGINE`、交互选择、`none`、旧变量兼容、
  分发器结构、manifest 写入、结束摘要。`install.ps1` 同步。
- Go：`internal/engine` 包、Registry、llama.cpp Provider 的 `Detect` /
  `InstallHint` / `Remove`；`ErrEngineNotInstalled`；`GET /api/engines`；
  `csghub-lite engine list|show|remove`；`uninstall` 改为按 manifest 删除。
- `csghub-lite engine install llama.cpp` 在本阶段的实现是**下载并执行官方安装
  脚本**，附带 `CSGHUB_LITE_INFERENCE_ENGINE=llama.cpp
  CSGHUB_LITE_INSTALL_COMPONENTS=engine`。后者是新增变量，取值 `binary,engine`
  （默认）或 `engine`，令脚本跳过 csghub-lite 二进制的下载与替换。Windows 走
  `install.ps1` 的同名参数。这样 CLI 命令从第一阶段起就存在且行为稳定，第二阶段
  只替换实现。
- Web：Settings 引擎卡片（只读状态 + 命令提示）、Dashboard 横幅、结构化错误引导。
- Docker、Homebrew、README、安装文档、环境变量文档更新。

### 第二阶段：Go 原生安装

- llama.cpp 的资产选择、镜像回退、cudart / patchelf / `install_name_tool` 等
  后处理移植到 `Provider.Install`，带进度回调。
- `POST /api/engines/{id}/install` 与 `DELETE /api/engines/{id}`，SSE 进度，
  Settings 卡片和 Dashboard 横幅出现“安装 / 升级”按钮。
- 安装脚本第 6 步改为调用 `csghub-lite engine install`，删除
  `LLAMA_CPP_DEFAULT_TAG` 与其锁步测试项，`PinnedVersion` 单一来源为
  `llama-cpp-assets`。
- `csghub-lite upgrade` 结束后升级 Managed 引擎。

### 第三阶段：第二个引擎

- 按第 7 节流程接入一个真实引擎（建议先做 MLX：单平台、依赖简单、能验证
  “同一 SafeTensors 模型可选 llama.cpp 或 MLX”这条路由）。
- `inference.engine` 配置和 Settings 里的默认引擎选择生效。

## 9. 兼容性与迁移

- 已装机器重跑安装器：检测到 llama-server → 不提问，按现有逻辑升级；结束摘要多
  一行引擎状态。无感知。
- 只有 `CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER=0` 的自动化脚本：行为不变，多一条
  弃用提示；建议在两个次版本后移除旧变量。
- `csghub-lite uninstall` 不再删除非 Managed 的 llama-server，发布说明需要提及。
  第一阶段安装器写 manifest 之前装的副本没有清单，会被视为外部安装；`uninstall`
  在这种情况下保留现在的“询问是否一并删除同目录 llama-server”的交互，避免升级用
  户卸载后残留。
- 版本锁步规则在第一阶段不变；第二阶段的删除项已在 `llama-cpp.md` 对应条目中
  同步更新。

## 10. 测试与验收

- `internal/engine`：Registry 查找、`ForRuntime`、llama 版本字符串解析（含浅克隆
  构建号忽略规则）、manifest 读写、`Remove` 只删清单文件。
- `internal/server`：`GET /api/engines` 快照测试；缺引擎时模型加载返回 503 与
  `engine_not_installed`；`openapi/local-api.json` 同步测试。
- `internal/cli`：`engine list` 输出、`uninstall` 对 Managed / 外部两种情况的
  行为、`run` 打印 hint。
- 安装脚本：新增 `scripts/test-install.sh`，用 `CSGHUB_LITE_INSTALL_DRY_RUN=1`
  让脚本只打印将执行的下载与安装动作，覆盖 `none`、`llama.cpp`、旧变量、
  无 TTY 默认值、`CSGHUB_LITE_INSTALL_COMPONENTS=engine` 五种情形；CI 在 macOS 和
  Ubuntu runner 上运行，`install.ps1` 在 Windows runner 上用 `-WhatIf` 风格参数做
  同样检查。
- 手工验收清单：
  1. 全新 macOS / Ubuntu 机器 `curl | sh`，出现菜单，回车后装 llama.cpp，
     `csghub-lite engine list` 显示 installed、managed、up to date。
  2. 同样机器 `CSGHUB_LITE_INFERENCE_ENGINE=none`，安装完成，Web UI 可打开，
     Dashboard 有横幅，`csghub-lite run Qwen/Qwen3-0.6B-GGUF` 打印 hint 而非堆栈。
  3. 在 2 的机器执行 `csghub-lite engine install llama.cpp`，随后模型可运行。
  4. `csghub-lite uninstall` 只删除清单文件；`brew install llama.cpp` 的副本保留。
  5. Docker CPU 镜像启动后 `engine list` 显示 llama.cpp installed。

## 11. 待定问题

- 交互菜单是否默认选 llama.cpp。本设计选“是”，因为绝大多数用户的目标是跑本地
  模型，且回车即默认能保住 `curl | sh` 一路回车的体验；如果产品上更希望用户显式
  选择，只需把默认项去掉。
- `inference.engine` 配置是否在第一阶段就加进 `config.json`。本设计建议**推迟到
  第三阶段**真正出现多引擎时再加，符合“不预留无消费者的配置键”的仓库规则。
- 第二阶段是否同时把 Homebrew 的 `llama.cpp` 依赖改成 `:optional`。Homebrew 的
  llama.cpp 版本不受我们锁定，与内置 converter 可能不一致；改成 `:optional` 后由
  `csghub-lite engine install` 装锁定版本更可控，但会改变 brew 用户的默认体验，
  需要单独决定。
