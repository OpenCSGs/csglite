# 安装指南

## 系统要求

- **操作系统**: macOS (Apple Silicon / Intel)、Linux (x86_64 / ARM64)、Windows (x86_64)
- **推理依赖**: [llama-server](https://github.com/ggml-org/llama.cpp)（模型推理必需）
- **编译依赖**: Go 1.22+（仅源码编译需要）

## 安装 CSGLite

### 方式一：一键安装脚本（推荐）

适用于 Linux 和 macOS，自动检测系统架构，从 GitHub Releases 下载安装。

```bash
curl -fsSL https://hub.opencsg.com/csghub-lite/install.sh | sh
```

macOS 上，安装脚本会优先选择当前已经在 `PATH` 且可写的目录（例如 `/opt/homebrew/bin`），找不到时再回退到 `~/bin`，尽量避免 `sudo`。如果回退到了 `~/bin`，脚本会自动写入 shell 配置，并提示当前终端立刻生效的命令。

指定版本安装：

```bash
curl -fsSL https://hub.opencsg.com/csghub-lite/install.sh | CSGHUB_LITE_VERSION=v0.1.0 sh
```

企业版安装（会额外把 `license.txt` 写入安装目录）：

```bash
curl -fsSL https://hub.opencsg.com/csghub-lite/install.sh | EE=1 sh
```

Windows PowerShell 同样支持：

```powershell
$env:EE="1"; irm https://hub.opencsg.com/csghub-lite/install.ps1 | iex
```

安装脚本环境变量（可选，`install.sh` 与 `install.ps1` 共同支持通用变量）：

| 变量 | 说明 |
|------|------|
| `EE` | 设为 `1` 时，将企业版 `license.txt` 写入 `csghub-lite` 安装目录。 |
| `INSTALL_DIR` | 指定 `csghub-lite` 安装目录。未设置时，macOS 会优先选择当前 `PATH` 中可写的目录，否则回退到 `~/bin`；Linux 仍默认使用已有安装目录或 `/usr/local/bin`。 |
| `CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR` | 指定 `llama-server` 安装目录。未设置时，macOS 默认跟随 `csghub-lite` 的安装目录。 |
| `CSGHUB_LITE_LLAMA_CPP_TAG` | 指定要安装的 `llama.cpp` release tag。默认固定到与内置 `convert_hf_to_gguf.py` / `gguf-py` 对齐的 tag，确保三个版本一致。 |
| `CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER` | 设为 `0` 可跳过自动安装/升级 `llama-server`。 |
| `CSGHUB_LITE_AUTO_INSTALL_CUDA_LIBS` | Linux NVIDIA 环境安装 CUDA 版 `llama-server` 后，若缺少 `libcudart` / `libcublas`，默认通过 NVIDIA 官方 APT 源安装 `cuda-libraries-*`；设为 `0` 可关闭。 |
| `CSGHUB_LITE_AUTO_INSTALL_PATCHELF` | Linux 上设为 `0` 可禁止自动 `apt/dnf/yum install patchelf`（用于为 `llama-server` 设置 `$ORIGIN`，使同目录 `.so` 可被直接加载）。 |
| `CSGHUB_LITE_LLAMA_ROCM_VERSION` | Linux 上可显式指定优先尝试的 ROCm 资产版本（例如 `7.2`）。未设置时，安装脚本会尝试从本机 ROCm 环境自动识别版本，再回退到发布页中可用的其他 ROCm/Vulkan/CPU 包。 |

说明：若远程 llama.cpp 与本地 **build 号一致**，脚本会跳过重新下载；此前若因缺少 `libmtmd.so.0` 等导致 `llama-server --version` 失败，会被误判为需要升级——新版本已用 `LD_LIBRARY_PATH` 检测版本，并从压缩包 **递归** 安装所有 `.so`。

### 方式二：Homebrew（主要面向 macOS）

可选额外入口，主安装入口仍然推荐使用上面的 `curl ... | sh`。Linux 仍建议优先使用安装脚本、release 压缩包或系统包管理器。

```bash
brew tap opencsgs/csglite https://github.com/OpenCSGs/csglite
brew install opencsgs/csglite/csghub-lite
```

### 方式三：GitHub Releases 手动下载

前往 [Releases](https://github.com/opencsgs/csglite/releases) 页面，下载对应平台的压缩包：

| 平台 | 文件名 |
|------|--------|
| macOS Apple Silicon | `csghub-lite_*_darwin_arm64.tar.gz` |
| macOS Intel | `csghub-lite_*_darwin_amd64.tar.gz` |
| Linux x86_64 | `csghub-lite_*_linux_amd64.tar.gz` |
| Linux ARM64 | `csghub-lite_*_linux_arm64.tar.gz` |
| Windows x86_64 | `csghub-lite_*_windows_amd64.zip` |

下载后解压并移动到一个已经在 `PATH` 中的目录，例如 `~/bin`、`/opt/homebrew/bin` 或 `/usr/local/bin`：

```bash
tar xzf csghub-lite_*.tar.gz
mkdir -p "$HOME/bin"
mv csghub-lite "$HOME/bin/"
```

### 方式四：Linux 包管理器

Debian / Ubuntu：

```bash
sudo dpkg -i csghub-lite_*.deb
```

RHEL / CentOS / Fedora：

```bash
sudo rpm -i csghub-lite_*.rpm
```

### 方式五：从源码编译

```bash
git clone https://github.com/opencsgs/csglite.git
cd csglite
make build
# 二进制文件位于 bin/csghub-lite
```

全平台编译：

```bash
make build-all
```

## 安装 llama-server（推理依赖）

CSGLite 使用 llama.cpp 的 `llama-server` 进行模型推理。你需要单独安装它。

### macOS

```bash
brew install llama.cpp
```

### Linux / Windows

从 [llama.cpp Releases](https://github.com/ggml-org/llama.cpp/releases) 下载对应平台的预编译包，解压后将 `llama-server` 放入 PATH 即可。

```bash
# 示例：Linux x86_64
wget https://github.com/ggml-org/llama.cpp/releases/download/b9158/llama-b9158-bin-ubuntu-x64.tar.gz
tar xzf llama-b9158-bin-ubuntu-x64.tar.gz
sudo cp build/bin/llama-server /usr/local/bin/
```

## 验证安装

```bash
csghub-lite --version
llama-server --version
```
