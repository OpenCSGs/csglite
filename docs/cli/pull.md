# csghub-lite pull

从 CSGHub 平台下载模型到本地。

## 用法

```bash
csghub-lite pull <model>
```

## 参数

| 参数 | 说明 |
|------|------|
| `<model>` | 模型名称，格式 `namespace/name` |

## 说明

- 下载模型仓库中的所有文件（包括模型权重、配置文件、README 等）
- 下载过程中实时显示每个文件的进度和传输速度（如 `23.4 MB/s`）
- 支持断点续传：中断后重新运行会从上次中断处继续
- LFS 大文件通过对象存储（OSS）下载，普通文件通过 API 下载
- 下载完成后自动生成本地 manifest 文件记录元信息
- 模型存储路径：`~/.csghub-lite/models/<namespace>/<name>/`

## 示例

```bash
# 下载 GGUF 模型
csghub-lite pull Qwen/Qwen3-0.6B-GGUF

# 下载过程显示进度和实时速度
Pulling model Qwen/Qwen3-0.6B-GGUF from https://hub.opencsg.com...
  [1/5] .gitattributes  100.0% (1.8 KB / 1.8 KB)
  [2/5] LICENSE  100.0% (11.3 KB / 11.3 KB)
  [3/5] Qwen3-0.6B-Q8_0.gguf  45.2% (275.4 MB / 609.8 MB)  23.4 MB/s

Successfully pulled model Qwen/Qwen3-0.6B-GGUF (gguf, 609.8 MB)
```
