# csghub-lite stop

停止后台 csghub-lite 服务，或卸载正在运行的模型。

## 用法

```bash
csghub-lite stop
csghub-lite stop model [MODEL]
```

## 说明

- `csghub-lite stop` 停止由 `start`、`serve` 或客户端命令自动拉起的后台 API 服务
- `csghub-lite stop model` 卸载当前正在运行的全部模型，释放内存和 GPU 资源
- `csghub-lite stop model <model>` 只卸载指定模型
- 停止模型会终止底层的 `llama-server` 进程，但不会关闭 csghub-lite 服务
- 下次请求该模型时会重新加载

可用别名（仅停止服务）：

- `stop-server`
- `down`

`csghub-lite stop-service` 仍然可用，但已废弃，请改用 `csghub-lite stop`。

为兼容旧用法，`csghub-lite stop <model>` 仍会卸载指定模型。

## 前提条件

停止模型需要先启动服务器：`csghub-lite start` 或 `csghub-lite serve`

## 示例

```bash
# 停止后台服务
csghub-lite stop
Stopping csghub-lite service...
Stopped csghub-lite service

# 停止当前运行的模型
csghub-lite stop model
Stopped model Qwen/Qwen3-0.6B-GGUF

# 停止指定模型
csghub-lite stop model Qwen/Qwen3-0.6B-GGUF
Stopped model Qwen/Qwen3-0.6B-GGUF
```
