# CLI 命令总览

CSGLite 提供以下命令：

## 模型运行

| 命令 | 说明 |
|------|------|
| [`run <model>`](run.md) | 自动下载（如需）并启动交互对话 |
| [`chat <model>`](chat.md) | 与已下载的本地模型交互对话 |
| [`serve`](serve.md) | 启动 REST API 服务 |

## 模型管理

| 命令 | 说明 |
|------|------|
| [`pull <model>`](pull.md) | 从 CSGHub 下载模型 |
| [`list` / `ls`](list.md) | 列出已下载的本地模型 |
| [`show <model>`](show.md) | 查看模型详细信息 |
| [`rm <model>`](rm.md) | 删除本地模型 |
| [`search <query>`](search.md) | 搜索 CSGHub 平台上的模型 |

## 服务管理

| 命令 | 说明 |
|------|------|
| [`ps`](ps.md) | 列出服务器上运行中的模型 |
| [`stop <model>`](stop.md) | 停止/卸载服务器上运行中的模型 |
| [`restart`](restart.md) | 重启后台 API 服务 |

## 配置与认证

| 命令 | 说明 |
|------|------|
| [`login`](login.md) | 设置 CSGHub 访问令牌 |
| [`config`](config.md) | 查看或修改配置 |

## 其他

| 命令 | 说明 |
|------|------|
| `--version` / `-v` | 显示版本号 |
| `help` | 显示帮助信息 |
| `completion` | 生成 Shell 自动补全脚本 |

## 模型名称格式

所有涉及模型的命令使用 `namespace/name` 格式，例如：

```
Qwen/Qwen3-0.6B-GGUF
OpenCSG/csg-wukong-1B
```
