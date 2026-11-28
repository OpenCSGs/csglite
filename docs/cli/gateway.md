# 网关模型服务

先运行 `csghub-lite start`。发布服务时选择对调用方公开的模型名，使用 `--member 来源=模型ID` 绑定实际提供推理的模型。默认策略为 `failover`，按成员指定顺序故障转移。其他策略包括单成员的 `fixed` 和按权重分配的 `weighted`。

```sh
csghub-lite gateway publish lab-chat --member local=org/chat --member cloud=org/chat
csghub-lite gateway services
csghub-lite gateway usage --period month --service lab-chat
```

客户端向网关请求模型 `lab-chat`。停用和恢复服务时，使用创建响应或服务清单中的管理字段 `id`。停用会保留成员配置。

```sh
csghub-lite gateway unpublish SERVICE_MANAGEMENT_ID
csghub-lite gateway enable SERVICE_MANAGEMENT_ID
```

用量查询默认最近一周，`--period` 可选 `week`、`month`、`year`。`--provider` 按提供方过滤，`--service` 可以传入服务管理 ID、名称或公开模型名。结果以 JSON 输出。
