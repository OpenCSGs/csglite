# POST /api/generate

文本生成接口，支持单次提示词生成和流式输出。

## 请求

```
POST /api/generate
Content-Type: application/json
```

### 请求体

```json
{
  "model": "Qwen/Qwen3-0.6B-GGUF",
  "prompt": "Write a haiku about programming",
  "stream": true,
  "options": {
    "temperature": 0.7,
    "max_tokens": 256,
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
| `prompt` | string | 是 | 提示词 |
| `stream` | bool | 否 | 是否流式输出（默认 `true`） |
| `options` | object | 否 | 生成参数 |

`options` 支持 `temperature`、`top_p`、`top_k`、`max_tokens`、`seed`、`num_ctx`、`num_parallel`、`n_gpu_layers`、`cache_type_k`、`cache_type_v`、`dtype`。其中 `n_gpu_layers` 与 `llama-server --n-gpu-layers` 保持一致，可用于限制 GPU offload 层数；不设置时不向 llama-server 传递该参数，由其按空闲显存自动适配 offload 层数；`cache_type_k` / `cache_type_v` 与 `llama-server --cache-type-k` / `--cache-type-v` 保持一致，可用于在显存紧张时压缩 KV cache；`dtype` 用于控制 SafeTensors -> GGUF 自动转换的输出类型，视觉模型的 `mmproj` 也会跟随同一 `dtype` 一起转换。

## 响应

### 流式响应（默认）

```
data: {"model":"Qwen/Qwen3-0.6B-GGUF","response":"Code","done":false,"created_at":"2026-03-11T00:43:32.205Z"}

data: {"model":"Qwen/Qwen3-0.6B-GGUF","response":" is","done":false,"created_at":"2026-03-11T00:43:32.212Z"}

data: {"model":"Qwen/Qwen3-0.6B-GGUF","response":"","done":true,"created_at":"2026-03-11T00:43:32.343Z"}
```

### 非流式响应

```json
{
  "model": "Qwen/Qwen3-0.6B-GGUF",
  "response": "Code is written,\nCollaborating on the code.\nAnd solved the challenge.",
  "done": true,
  "created_at": "2026-03-11T00:43:32.343Z"
}
```

## 与 /api/chat 的区别

| | `/api/generate` | `/api/chat` |
|---|---|---|
| 输入 | 单个 `prompt` 字符串 | `messages` 数组（多轮对话） |
| 响应字段 | `response` | `message` |
| 适用场景 | 单次生成、补全 | 对话、多轮交互 |

## 示例

### curl

```bash
curl http://localhost:11435/api/generate -d '{
  "model": "Qwen/Qwen3-0.6B-GGUF",
  "prompt": "Explain quantum computing in one sentence",
  "stream": false
}'
```

### Python

```python
import requests

resp = requests.post("http://localhost:11435/api/generate", json={
    "model": "Qwen/Qwen3-0.6B-GGUF",
    "prompt": "Write a Python hello world",
    "stream": False
})
print(resp.json()["response"])
```
