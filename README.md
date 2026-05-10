# csghub-lite

<p align="center">
  <img src="docs/images/apps.png" alt="AI Apps" width="80%">
</p>

A lightweight tool for running large language models locally, powered by models from the [CSGHub](https://opencsg.com) platform.

Inspired by [Ollama](https://ollama.com), csghub-lite provides model download, local inference, interactive chat, and an OpenAI-compatible REST API — all from a single binary.

## Features

### Core

- **One command to start** — `csghub-lite run` downloads, loads, and chats
- **Model keep-alive** — models stay loaded after exit (default 5 min), instant reconnect
- **Auto-start server** — background API server starts automatically, no manual setup
- **Model download** from CSGHub platform (hub.opencsg.com or private deployments)
- **Local inference** via llama.cpp (GGUF models, SafeTensors auto-converted)
- **Interactive chat** with streaming output
- **REST API** compatible with Ollama's API format
- **Cross-platform** — macOS, Linux, Windows
- **Resume downloads** — interrupted downloads resume where they left off
- **Pause/Resume** — pause ongoing downloads and resume later

### Web UI

- **Model Library** — manage local models with download progress, pause/resume, and one-click run
- **Marketplace** — browse and download models/datasets from CSGHub
- **Chat Interface** — interactive chat with local and cloud models
- **AI Apps** — install and launch AI applications with one-click configuration
- **Settings** — configure storage, third-party providers, and access tokens

### Integrations

- **Third-Party Providers** — integrate OpenAI, DeepSeek, MiMo, Kimi, BigModel, Qianfan, MiniMax, OpenRouter, and any OpenAI-compatible API
- **Coding Agents** — one-click config for Claude Code, Codex, Pi, OpenCode
- **AI Applications** — one-click setup for OpenClaw, CSGClaw, Dify, AnythingLLM

### Dataset Support

- **Dataset download** from CSGHub platform
- **Dataset management** — list, show details, and delete local datasets

## Installation

### Quick install (Linux / macOS)

```bash
curl -fsSL https://hub.opencsg.com/csghub-lite/install.sh | sh
```

### Quick install (Windows PowerShell)

```powershell
irm https://hub.opencsg.com/csghub-lite/install.ps1 | iex
```

### From source

```bash
git clone https://github.com/opencsgs/csghub-lite.git
cd csghub-lite
make build
# Binary is at bin/csghub-lite
```

### From GitHub Releases

Download the latest binary for your platform from the [Releases](https://github.com/opencsgs/csghub-lite/releases) page.

## Quick Start

```bash
# Run a model — downloads, starts server, and chats (all automatic)
csghub-lite run Qwen/Qwen3-0.6B-GGUF

# Keep a model loaded until you stop it manually
csghub-lite run Qwen/Qwen3-0.6B-GGUF --keep-alive -1

# Search for models on CSGHub
csghub-lite search "qwen"

# Check running models (model stays loaded after exit)
csghub-lite ps

# Set CSGHub access token (optional, for private models)
csghub-lite login
```

> **Note:** The install script automatically installs [llama-server](https://github.com/ggml-org/llama.cpp) (required for inference). If you installed from source, install it separately: `brew install llama.cpp` (macOS) or download from [llama.cpp releases](https://github.com/ggml-org/llama.cpp/releases).

## Integrations

### Third-Party Model Providers

csghub-lite supports integrating third-party OpenAI-compatible API providers. Configure providers in Settings, and their models appear alongside local and OpenCSG models in Chat.

Supported providers include:

| Provider | Notes | Base URL |
| --- | --- | --- |
| OpenAI | OpenAI-compatible | `https://api.openai.com/v1` |
| DeepSeek | - | `https://api.deepseek.com/v1` |
| MiMo | Xiaomi | `https://api.xiaomimimo.com/v1` |
| Kimi | Moonshot | `https://api.moonshot.cn/v1` |
| BigModel | Zhipu/智谱AI | `https://open.bigmodel.cn/api/coding/paas/v4` |
| Qianfan | Baidu | `https://qianfan.baidubce.com/v2` |
| MiniMax | - | `https://api.minimax.chat/v1` |
| OpenRouter | - | `https://openrouter.ai/api/v1` |
| Any OpenAI-compatible API | Custom provider | Custom base URL |

### Coding Agents

One-click configuration for popular coding agents. Models from local, OpenCSG, or third-party providers work seamlessly:

| Agent | Config Location | One-Click Setup |
|---|---|---|
| **Claude Code** | `~/.claude/settings.json` | `csghub-lite launch claude-code --model <model>` |
| **Codex** | `~/.codex/config.toml` | `csghub-lite launch codex --model <model>` |
| **Pi** | `~/.pi/agent/settings.json` | `csghub-lite launch pi --model <model>` |
| **OpenCode** | `~/.opencode.json` | `csghub-lite launch open-code --model <model>` |

After the first launch via csghub-lite, subsequent runs use the configured settings automatically — no manual API key or base URL setup needed.

Example:
```bash
# Configure Claude Code with GLM model
csghub-lite launch claude-code --model "glm-4-flash"

# Configure Codex with local Qwen model
csghub-lite launch codex --model "Qwen/Qwen2.5-Coder-7B"

# Configure Pi with Kimi model
csghub-lite launch pi --model "moonshot-v1-8k"
```

### AI Applications

One-click setup for AI assistant applications:

| App | Description | Setup |
|---|---|---|
| **OpenClaw** | Open-source AI assistant with web UI | `csghub-lite launch openclaw` or Web UI → AI Apps |
| **CSGClaw** | Enterprise AI assistant with advanced features | `csghub-lite launch csgclaw` or Web UI → AI Apps |
| **Dify** | LLM app development platform | Web UI → AI Apps → Install |
| **AnythingLLM** | Private document chat | Web UI → AI Apps → Install |

All apps auto-configure to use csghub-lite's OpenAI-compatible API endpoint with your selected models.

## CLI Commands

| Command | Description |
|---|---|
| `csghub-lite run <model>` | Pull, start server, and chat (all automatic) |
| `csghub-lite chat <model>` | Chat with a locally downloaded model |
| `csghub-lite ps` | List currently running models and their keep-alive |
| `csghub-lite stop <model>` | Stop/unload a running model |
| `csghub-lite serve` | Start the API server (auto-started by `run`) |
| `csghub-lite pull <model>` | Download a model from CSGHub |
| `csghub-lite list` / `ls` | List locally downloaded models |
| `csghub-lite show <model>` | Show model details (format, size, files) |
| `csghub-lite rm <model>` | Remove a locally downloaded model |
| `csghub-lite login` | Set CSGHub access token |
| `csghub-lite search <query>` | Search models on CSGHub |
| `csghub-lite config set <key> <value>` | Set configuration |
| `csghub-lite config get <key>` | Get a configuration value |
| `csghub-lite config show` | Show current configuration |
| `csghub-lite uninstall` | Remove csghub-lite and llama-server, preserving local data unless `--all` is set |
| `csghub-lite --version` | Show version information |

Model names use the format `namespace/name`, e.g. `Qwen/Qwen3-0.6B-GGUF`.

### run vs chat

- **`run`** — Downloads the model if not present, auto-starts the background server, and opens a chat session. After you exit, the model stays loaded for 5 minutes by default so the next `run` is instant. Use `--keep-alive -1` to keep it loaded until you stop it manually.
- **`chat`** — Starts a chat session with a model that is already downloaded. Supports `--system` flag for custom system prompts.

```bash
# Auto-download and chat (first time)
csghub-lite run Qwen/Qwen3-0.6B-GGUF

# Exit chat, model stays loaded — reconnect instantly
csghub-lite run Qwen/Qwen3-0.6B-GGUF

# Keep the model loaded until `csghub-lite stop`
csghub-lite run Qwen/Qwen3-0.6B-GGUF --keep-alive -1

# Check which models are still loaded
csghub-lite ps

# Chat with custom system prompt (model must be downloaded)
csghub-lite chat Qwen/Qwen3-0.6B-GGUF --system "You are a coding assistant."
```

### Interactive Chat Commands

Once in a chat session (`run` or `chat`):

| Command | Description |
|---|---|
| `/bye`, `/exit`, `/quit` | Exit the chat |
| `/clear` | Clear conversation context |
| `/help` | Show help |

End a line with `\` for multiline input. Press Ctrl+D to exit.

## Supported Platforms

### Operating Systems & Architectures

| Platform | Support |
|---|---|
| Ubuntu (x86_64, ARM64) | Supported |
| Debian (x86_64, ARM64) | Supported |
| CentOS (x86_64, ARM64) | Supported |
| macOS (x86_64, Apple Silicon) | Supported |
| Windows (x86_64) | Supported |

### Accelerators and Compute Boxes

| Hardware | Support |
|---|---|
| Mac mini | Supported |
| AMD Instinct MI50 | Supported |
| AMD Instinct MI200 / MI250 | Supported |
| NVIDIA GPU series | Supported |

## REST API

The server listens on `localhost:11435` by default.

For full endpoint details and examples, see the [REST API Reference](docs/api/overview.md).

If the local server is running, you can also open the interactive API docs in your browser:
[http://localhost:11435/api-docs.html](http://localhost:11435/api-docs.html)

## Logs

By default, csghub-lite writes logs under `~/.csghub-lite/logs/`:

- `csghub-lite.log` — API server logs
- `llama-server.log` — llama-server subprocess logs

## Configuration

Configuration is stored at `~/.csghub-lite/config.json`.

The CLI and Web UI expose a convenience `storage_dir` setting. When you set it, csghub-lite expands it into the persisted `model_dir` and `dataset_dir`.

| Key | Default | Description |
|---|---|---|
| `storage_dir` | `~/.csghub-lite` | Shared local storage root for models and datasets |
| `server_url` | `https://hub.opencsg.com` | CSGHub platform URL |
| `ai_gateway_url` | `https://ai.space.opencsg.com` | AI Gateway URL for cloud inference models |
| `model_dir` | `~/.csghub-lite/models` | Effective local model storage directory |
| `dataset_dir` | `~/.csghub-lite/datasets` | Effective local dataset storage directory |
| `listen_addr` | `:11435` | API server listen address |
| `token` | (none) | CSGHub access token |

Switch to a private CSGHub deployment:

```bash
csghub-lite config set server_url https://my-private-csghub.example.com
```

## Model Formats

| Format | Download | Inference |
|---|---|---|
| GGUF | Yes | Yes (via llama.cpp) |
| SafeTensors | Yes | Yes (auto-converted to GGUF) |

SafeTensors checkpoints are converted once using the bundled llama.cpp `convert_hf_to_gguf.py`. `csghub-lite` automatically prepares an isolated Python environment under `~/.csghub-lite/tools/python`; if that setup fails, run the same commands manually:

```bash
python3 -m venv ~/.csghub-lite/tools/python
~/.csghub-lite/tools/python/bin/python -m pip install --upgrade --index-url https://mirrors.aliyun.com/pypi/simple pip
~/.csghub-lite/tools/python/bin/python -m pip install --index-url https://mirrors.aliyun.com/pypi/simple --find-links https://mirrors.aliyun.com/pytorch-wheels/cpu torch
~/.csghub-lite/tools/python/bin/python -m pip install --index-url https://mirrors.aliyun.com/pypi/simple safetensors transformers sentencepiece
```

Use Python 3.10+ on `PATH` (Windows: `python` or `python3`). `csghub-lite` retries torch from the official PyTorch CPU index (`https://download.pytorch.org/whl/cpu`) if the Aliyun mirror is unavailable. `gguf-py` is loaded from matching Gitee `llama.cpp` source (`https://gitee.com/xzgan/llama.cpp`), not PyPI. If `transformers` is too old for a new architecture, `csghub-lite` tries to upgrade it inside the managed venv before retrying. Some models may need extra packages (for example `sentencepiece`); see [`internal/convert/data/README.md`](internal/convert/data/README.md) for the full list and troubleshooting.

## Development

```bash
# Run tests
make test

# Run tests with coverage
make test-cover

# Build for all platforms
make build-all

# Test goreleaser locally (no publish)
make release-snapshot

# Lint
make lint
```

## Documentation

Full documentation is available in the [`docs/`](docs/) directory:

- **Getting Started**: [Installation](docs/getting-started/installation.md) | [Quick Start](docs/getting-started/quickstart.md)
- **CLI Reference**: [All Commands](docs/cli/overview.md)
- **REST API**: [API Reference](docs/api/overview.md)
- **Guides**: [Configuration](docs/guides/configuration.md) | [Model Formats](docs/guides/model-formats.md) | [Packaging](docs/guides/packaging.md) | [Architecture](docs/guides/architecture.md)

## License

Apache-2.0
