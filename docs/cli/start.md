# csghub-lite start

在后台启动 csghub-lite API 服务。如果服务已经在运行，会打印当前地址并直接退出。

## 用法

```bash
csghub-lite start [flags]
```

## 选项

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `--listen <addr>` | 监听地址 | `:11435`（来自配置文件） |

## 说明

- `start` 会把服务放到后台运行，当前终端可以继续使用
- 如果服务已经在运行，不会重复启动
- 前台启动请使用 `csghub-lite serve`
- 停止后台服务请使用 `csghub-lite stop`

可用别名：

- `start-service`
- `start-server`
- `up`

## 示例

```bash
# 后台启动默认服务
csghub-lite start
Starting csghub-lite service...
Started csghub-lite service at http://127.0.0.1:11435

# 指定端口
csghub-lite start --listen :8080
```
