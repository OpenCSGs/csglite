# csghub-lite restart

重启后台运行的 csghub-lite API 服务。如果当前没有可用的后台服务，也会直接启动一个新的后台服务。

## 用法

```bash
csghub-lite restart
```

## 说明

该命令会先尝试优雅停止现有服务，然后重新启动并等待健康检查通过。它使用当前配置中的 `listen_addr`，默认地址为 `:11435`。

可用别名：

- `restart-service`
- `restart-server`
- `reload`

## 示例

```bash
# 重启默认后台服务
csghub-lite restart
```
