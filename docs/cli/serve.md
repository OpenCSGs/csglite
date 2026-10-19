# csghub-lite serve

启动 REST API 服务，提供 Ollama 兼容的 HTTP API。

## 用法

```bash
csghub-lite serve [flags]
```

## 选项

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `--listen <addr>` | 监听地址 | `:11435`（来自配置文件） |
| `--openai-stream-default` | 当请求未传 `stream` 时，默认以 SSE 返回 `/v1/chat/completions` | `false` |

也可以通过环境变量 `CSGHUB_LITE_OPENAI_STREAM_DEFAULT=true` 启用。请求体显式传入的 `stream: true` 或 `stream: false` 优先于启动配置。

## 说明

启动后，服务器提供以下 API 端点：

- 对话生成（`/api/chat`、`/api/generate`）
- 模型管理（`/api/tags`、`/api/show`、`/api/pull`、`/api/delete`）
- 服务管理（`/api/ps`、`/api/stop`、`/api/health`）

详见 [REST API 参考](../api/overview.md)。

服务器在首次请求时自动加载模型，后续请求复用已加载的模型实例。

启动时还会在后台检查由 csghub-lite 安装的 `llama-server` 是否与本版本锁定的
`llama.cpp` tag 一致；不一致时静默对齐到锁定 tag（失败只记日志，下次启动重试）。
对齐在后台运行，不会索取管理员权限：安装目录不可写、或需要 root 权限才能完成的
步骤（装 `patchelf`、NVIDIA CUDA 运行库）会跳过并记入日志，需要时手动重跑安装
脚本即可。Windows 上运行中的 `llama-server.exe` 会锁住文件，对齐因此会先停掉受管
引擎再下载替换，正在进行的推理会中断一次。其它来源的
`llama-server`（Homebrew、自行编译的版本等）不会被改动，设
`CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER=0` 可关闭。桌面版不做这一步：运行时随应用
打包在 `Contents/Resources/runtime/llama` 里、单独代码签名，由应用整包更新。

使用 `Ctrl+C` 优雅关闭服务器（会自动释放所有模型资源）。

## 示例

```bash
# 使用默认端口
csghub-lite serve

# 指定端口
csghub-lite serve --listen :8080

# 让 OpenAI Chat Completions 在省略 stream 时默认使用流式响应
csghub-lite serve --openai-stream-default

# 后台运行
csghub-lite start
```
