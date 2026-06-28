# csghub-lite config

查看和管理 CSGLite 的配置。

## 子命令

### config show

显示当前所有配置：

```bash
csghub-lite config show
```

### config get

获取某个配置项的值：

```bash
csghub-lite config get <key>
```

### config set

设置某个配置项：

```bash
csghub-lite config set <key> <value>
```

## 配置项

| Key | 默认值 | 说明 |
|-----|--------|------|
| `storage_dir` | `~/.csghub-lite` | 模型和数据集共用的本地存储根目录，推荐优先设置 |
| `server_url` | `https://hub.opencsg.com` | CSGHub 平台地址 |
| `ai_gateway_url` | `https://ai.space.opencsg.com` | 云端推理模型使用的 AI Gateway 地址 |
| `model_dir` | `~/.csghub-lite/models` | 实际模型存储目录，通常由 `storage_dir` 自动派生 |
| `dataset_dir` | `~/.csghub-lite/datasets` | 实际数据集存储目录，通常由 `storage_dir` 自动派生 |
| `listen_addr` | `:11435` | API 服务监听地址 |
| `token` | （空） | CSGHub 访问令牌 |

## 配置文件

配置文件位于 `~/.csghub-lite/config.json`。

CLI 和 Web 设置页提供 `storage_dir` 这个便捷键；保存时会把它展开为配置文件里的 `model_dir` 和 `dataset_dir`。

JSON 示例：

```json
{
  "server_url": "https://hub.opencsg.com",
  "listen_addr": ":11435",
  "model_dir": "/Users/user/.csghub-lite/models",
  "dataset_dir": "/Users/user/.csghub-lite/datasets"
}
```

## 示例

```bash
# 查看所有配置
csghub-lite config show

# 切换到私有化部署
csghub-lite config set server_url https://my-csghub.example.com

# 修改云端推理网关地址
csghub-lite config set ai_gateway_url https://my-gateway.example.com

# 修改公共存储根目录（推荐）
csghub-lite config set storage_dir /data/csghub-lite

# 查看公共存储根目录
csghub-lite config get storage_dir

# 如有需要，也可以单独覆盖模型或数据集目录
csghub-lite config set model_dir /data/models
csghub-lite config set dataset_dir /data/datasets

# 修改 API 端口
csghub-lite config set listen_addr :8080

# 查看某个配置值
csghub-lite config get server_url
```
