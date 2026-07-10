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
csghub-lite serve &
```
