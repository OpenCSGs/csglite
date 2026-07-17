# POST /api/chat

对话补全接口，支持多轮对话和流式输出。

## 请求

```
POST /api/chat
Content-Type: application/json
```

### 请求体

```json
{
  "model": "Qwen/Qwen3-0.6B-GGUF",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello!"}
  ],
  "stream": true,
  "options": {
    "temperature": 0.7,
    "max_tokens": 2048,
    "num_ctx": 131072,
    "num_parallel": 1,
    "n_gpu_layers": 40,
    "cache_type_k": "q8_0",
    "cache_type_v": "q8_0",
    "dtype": "q8_0"
  }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model` | string | 是 | 模型名称 |
| `messages` | array | 是 | 对话消息列表 |
| `stream` | bool | 否 | 是否流式输出（默认 `true`） |
| `options` | object | 否 | 生成参数 |

`options` 支持 `temperature`、`top_p`、`top_k`、`max_tokens`、`seed`、`num_ctx`、`num_parallel`、`n_gpu_layers`、`cache_type_k`、`cache_type_v`、`dtype`。其中 `n_gpu_layers` 与 `llama-server --n-gpu-layers` 保持一致，可用于限制 GPU offload 层数；不设置时不向 llama-server 传递该参数，由其按空闲显存自动适配 offload 层数；`cache_type_k` / `cache_type_v` 与 `llama-server --cache-type-k` / `--cache-type-v` 保持一致，可用于在显存紧张时压缩 KV cache；`dtype` 用于控制 SafeTensors -> GGUF 自动转换的输出类型，视觉模型的 `mmproj` 也会跟随同一 `dtype` 一起转换。

### Message 格式

| 字段 | 类型 | 说明 |
|------|------|------|
| `role` | string | 角色：`system`、`user`、`assistant` |
| `content` | string | 消息内容 |

## 响应

### 流式响应（默认）

每个 SSE 事件包含一个 token：

```
data: {"model":"Qwen/Qwen3-0.6B-GGUF","message":{"role":"assistant","content":"Hello"},"done":false,"created_at":"2026-03-11T00:43:14.832Z"}

data: {"model":"Qwen/Qwen3-0.6B-GGUF","message":{"role":"assistant","content":"!"},"done":false,"created_at":"2026-03-11T00:43:14.839Z"}

data: {"model":"Qwen/Qwen3-0.6B-GGUF","done":true,"created_at":"2026-03-11T00:43:14.930Z"}
```

### 非流式响应

设置 `"stream": false` 时返回完整 JSON：

```json
{
  "model": "Qwen/Qwen3-0.6B-GGUF",
  "message": {
    "role": "assistant",
    "content": "Hello! 1 + 1 equals 2."
  },
  "done": true,
  "created_at": "2026-03-11T00:43:14.930Z"
}
```

## 示例

### curl（流式）

```bash
curl http://localhost:11435/api/chat -d '{
  "model": "Qwen/Qwen3-0.6B-GGUF",
  "messages": [{"role": "user", "content": "What is 1+1?"}]
}'
```

### curl（非流式）

```bash
curl http://localhost:11435/api/chat -d '{
  "model": "Qwen/Qwen3-0.6B-GGUF",
  "messages": [{"role": "user", "content": "What is 1+1?"}],
  "stream": false
}'
```

### Python

```python
import requests

resp = requests.post("http://localhost:11435/api/chat", json={
    "model": "Qwen/Qwen3-0.6B-GGUF",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": False
})
print(resp.json()["message"]["content"])
```

### 多轮对话

```bash
curl http://localhost:11435/api/chat -d '{
  "model": "Qwen/Qwen3-0.6B-GGUF",
  "messages": [
    {"role": "user", "content": "My name is Alice."},
    {"role": "assistant", "content": "Hello Alice! Nice to meet you."},
    {"role": "user", "content": "What is my name?"}
  ],
  "stream": false
}'
```
