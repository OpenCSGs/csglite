# CLI 命令总览

CSGLite 提供以下命令：

## 模型运行

| 命令 | 说明 |
|------|------|
| [`run <model>`](run.md) | 自动下载（如需）并启动交互对话 |
| [`chat <model>`](chat.md) | 与已下载的本地模型交互对话 |
| [`generate MODEL [TEXT]`](model-operations.md) | 单次文本推理，支持标准输入和 JSON 输出 |
| [`embed MODEL [TEXT...]`](model-operations.md) | 为一条或多条文本生成向量 |
| `image MODEL --prompt TEXT` | 生成图像，`--input` 可传入待编辑图像 |
| [`serve`](serve.md) | 前台启动 REST API 服务 |

## 模型管理

| 命令 | 说明 |
|------|------|
| [`pull <model>`](pull.md) | 从 CSGHub 下载模型 |
| [`list` / `ls`](list.md) | 列出已下载的本地模型 |
| [`load MODEL`](model-operations.md) | 等待本地模型加载就绪 |
| [`model-config MODEL`](model-operations.md) | 读取或更新模型的持久运行设置 |
| [`show <model>`](show.md) | 查看模型详细信息 |
| [`rm <model>`](rm.md) | 删除本地模型 |
| [`search <query>`](search.md) | 搜索 CSGHub 平台上的模型 |

## 服务管理

| 命令 | 说明 |
|------|------|
| [`logs [MODEL]`](model-operations.md) | 读取服务历史日志，按模型过滤并跟踪新增行 |
| [`ps`](ps.md) | 列出服务器上运行中的模型 |
| [`start`](start.md) | 后台启动 API 服务 |
| [`stop`](stop.md) | 停止后台 API 服务 |
| [`stop model [MODEL]`](stop.md) | 停止/卸载服务器上运行中的模型 |
| [`restart`](restart.md) | 重启后台 API 服务 |

## 运行时管理

| 命令 | 说明 |
|------|------|
| [`runtime status / upgrade`](runtime.md) | 查看或升级本地 Python 运行时（image/Diffusers、ASR、TTS） |

## 配置与认证

| 命令 | 说明 |
|------|------|
| [`login`](login.md) | 设置 CSGHub 访问令牌 |
| [`config`](config.md) | 查看或修改配置 |
| [`license`](license.md) | 查看、校验、安装或删除企业版 License |
| [`cluster`](cluster.md) | LAN 和公网集群的成员、模型分布及部署管理 |
| [`edge`](edge.md) | 向 CSGHub 贡献算力、管理申请、查询收益 |

## 其他

| 命令 | 说明 |
|------|------|
| `--version` / `-v` | 显示版本号 |
| `help` | 显示帮助信息 |
| `completion` | 生成 Shell 自动补全脚本 |

## 模型名称格式

下载时使用仓库名 `namespace/name`。推理时传所选来源的模型 ID，端侧贡献时传 `edge status` 列出的 ID。仓库名示例：

```
Qwen/Qwen3-0.6B-GGUF
OpenCSG/csg-wukong-1B
```
