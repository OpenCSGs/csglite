# csghub-lite rm

删除已下载到本地的模型，释放磁盘空间。

## 用法

```bash
csghub-lite rm <model>
```

## 参数

| 参数 | 说明 |
|------|------|
| `<model>` | 模型名称，格式 `namespace/name` |

## 说明

- 删除模型目录及其所有文件（包括权重文件、配置文件、manifest）
- 如果模型正在服务器中运行，需要先用 `stop model` 停止
- 删除后可通过 `pull` 重新下载

## 示例

```bash
$ csghub-lite rm Qwen/Qwen3-0.6B-GGUF
Removed model Qwen/Qwen3-0.6B-GGUF
```
