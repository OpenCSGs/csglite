# 贡献算力

`edge` 使用本机服务配置的 CSGHub 账号，管理通过端侧连接向网关提供的模型。各子命令输出 JSON。先启动本机服务并查看连接状态。

```sh
csghub-lite start
csghub-lite edge status
csghub-lite edge enable
```

在 `status` 输出中查看 `signed_in`、`state`、`model_limit`，从 `models` 中选择就绪模型的 `id`。下例中的 `MODEL_ID` 替换为这个完整 ID。`--name` 是对外展示的模型名，申请会提交给 CSGHub 审核。价格单位为每百万 token 的人民币分，例如 `100` 表示 1 元。

```sh
csghub-lite edge apply MODEL_ID --name lab-model --prompt-price 100 --completion-price 200 --max-concurrency 4
csghub-lite edge applications --page 1 --per 20
csghub-lite edge earnings --month 2026-01
csghub-lite edge balance
csghub-lite edge statements
```

`earnings` 默认查询当月。用 `edge withdraw APPLICATION_ID` 撤回指定申请，ID 来自申请列表。用 `edge disable` 关闭本机的端侧连接。公网集群 Worker 向自己的 Master 共享模型使用 `cluster share`，详见[集群命令](cluster.md)。
