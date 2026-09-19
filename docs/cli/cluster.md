# cluster

管理本机所属的局域网算力集群：发现同一网络里的 CSGLite 节点、配对、把请求路由到
持有模型的节点，并在 Dashboard 上展示每台机器。

节点之间靠证书互相识别，IP 变化（例如重启后 DHCP 换址）不需要任何操作；
本机没有的模型会自动交给持有它的成员执行。社区版最多可以配对 2 个节点，
导入企业版 License 后按 License 中的 `quota.lite.max_cluster_nodes` 放开
（未写明即不限）。

所有子命令都通过本机正在运行的 `csghub-lite` 服务（`/api/cluster*`）操作，
因此先要 `csghub-lite serve` 或 `csghub-lite start`。

**单机不受影响**：没有共享密钥、没有加入令牌、也没进过集群的机器处于休眠
状态，不监听节点间端口、不发 mDNS 广播、不起任何轮询。只有配置了密钥 / 令牌、
已是成员、或执行了 `create` / `join` / `invite` / `code` / `enable` 之一才会
开启网络，并记住这个选择；`leave` 之后（非密钥节点）自动回到休眠，也可以用
`disable` 关闭。

## 用法

```bash
csghub-lite cluster status                      # 本节点、集群与全部成员
csghub-lite cluster create [--name 名称]         # 在本机创建集群并打印加入令牌
csghub-lite cluster join <token> [--address host:port]
csghub-lite cluster leave                       # 离开集群（保留节点身份）
csghub-lite cluster token [--rotate]            # 查看 / 轮换加入令牌
csghub-lite cluster code                        # 未入集群时显示本机准入码
csghub-lite cluster discovered                  # 网络上发现的未配对节点
csghub-lite cluster invite <uuid> <code> [--address host:port]
csghub-lite cluster nodes                       # 成员列表
csghub-lite cluster remove <uuid|名称>           # 移除成员
csghub-lite cluster models                      # 模型 × 节点分布
csghub-lite cluster sync <model> [--all | --node <uuid|名称> ...]
csghub-lite cluster explain <model>             # 调度器会怎样放置一条请求
csghub-lite cluster drain | activate | maintenance
csghub-lite cluster enable | disable            # 开 / 关集群网络（休眠）
```

`status`、`nodes`、`discovered`、`models` 支持 `--json` 输出原始 JSON。

## 最简单的方式：安装时指定共享密钥

给所有盒子用同一个密钥安装，之后一切自动：

```bash
CSGHUB_LITE_CLUSTER_SECRET='机房一层-2026' CSGHUB_LITE_CLUSTER_NAME='机房一层' \
  curl -fsSL https://.../install.sh | sh
```

安装脚本把密钥写入 `config.json`（等价于 `csghub-lite config set cluster_secret <密钥>`）。
第一台启动的机器自动建群，后面启动的机器在网络上发现同一集群后自动加入；
两台同时启动各自建群也会在几秒内自动合并成一个。集群 UUID 和加入令牌都由
密钥派生，密钥本身不落盘、不上网，没有密钥的机器进不来。

已装好的机器补设密钥：`csghub-lite config set cluster_secret <密钥>` 后
`csghub-lite restart`。在自动组网的节点上执行 `cluster leave` 会暂停自动组网
（否则几秒后又会加回来），再执行 `cluster join` 或 `create` 即恢复。

## 其它两种入网方式

**令牌入网**（适合批量部署的盒子）：在任一节点 `cluster create`，把打印出的
`csgl1-<集群 UUID>-<密钥>` 令牌带到其它机器执行 `cluster join <token>`；
加入方通过多播 DNS 自动找到集群成员。也可以在其它机器首次启动前设置环境变量
`CSGHUB_LITE_CLUSTER_JOIN_TOKEN`，服务启动后自动加入。

**邀请入网**（适合有人操作的少量机器）：未入集群的节点执行 `cluster code`
得到一个 8 位准入码（10 分钟内有效、一次性），在任一成员上执行
`cluster invite <该节点 uuid> <准入码>`；uuid 可从 `cluster discovered` 或
Web 界面「算力集群」页看到。

两种方式的握手都不在网络上传输密钥本身，配对后节点间全程 mTLS，并按节点 UUID
钉住对方证书。

## 请求如何路由

- 聊天、嵌入、Responses、Anthropic、语音识别、语音合成都按同一规则路由；图像生成暂不路由。
- 不带 `source` 的推理请求：本机有该模型就在本机执行（与单机完全一致）；
  本机没有、而某个成员有，则转发给该成员。把集群设置中的 `routing_mode`
  改为 `balanced` 后，本机也只是候选之一。
- `source=cluster` 强制走调度器；`source=node:<uuid>` 钉住某个节点；
  `source=local` 强制本机。
- 响应头 `X-CSGLite-Node` / `X-CSGLite-Node-Name` 给出实际执行的节点。
- 同一 `X-CSGLite-Thread-ID` 的多轮对话会落到同一节点以复用 KV cache。
- `cluster explain <model>` 显示每个候选被排除的原因或预计完成时间，以及
  排队、冷启动、实测吞吐、热降频等因素。

## 模型同步

`cluster sync <model> --all` 让每个还没有该模型的成员获取它：**先从集群里
已持有该模型的成员通过内网复制**（mTLS 通道，按文件校验 SHA-256，千兆内网
几十秒即可），没有任何成员持有时才从模型源下载。普通 `pull` 在集群里也走同
样的顺序。成员模型源不一致时 `sync` 会拒绝，避免同一个模型 ID 指向不同文件；
指定了量化或版本的拉取不走复制。

## 运维状态

| 状态 | 含义 |
|---|---|
| `active` | 正常接单 |
| `drain` | 处理完在途请求后不再接新请求（升级、换卡前使用） |
| `maintenance` | 同时不参与模型同步与副本放置 |

## 相关环境变量

| 变量 | 说明 |
|---|---|
| `CSGHUB_LITE_CLUSTER_SECRET` | 共享密钥，同一密钥的机器自动组成一个集群（推荐） |
| `CSGHUB_LITE_CLUSTER_NAME` | 自动组网时的集群显示名 |
| `CSGHUB_LITE_CLUSTER_JOIN_TOKEN` | 首次启动且未入集群时用令牌自动加入 |
| `CSGHUB_LITE_CLUSTER_ADDR` | 节点间监听地址，默认 `:11438` |
| `CSGHUB_LITE_CLUSTER_SEEDS` | 逗号分隔的成员地址，多播不可用时使用 |
| `CSGHUB_LITE_CLUSTER_DISCOVERY` | `mdns`（默认）或 `none` |
| `CSGHUB_LITE_CLUSTER_ADVERTISE_HOST` | 多网卡机器上指定对外公布的地址 |
| `CSGHUB_LITE_CLUSTER_DISABLED` | `1` 时完全不启动集群组件 |

详见 [局域网算力集群设计](../guides/lan-cluster-design.md)。

## 示例

```bash
$ csghub-lite cluster create --name 机房一层
Created cluster "机房一层" (e0a48ae5-...).

Join token (shown once; rotate with 'cluster token --rotate'):
  csgl1-e0a48ae5-...-k3m2...

$ csghub-lite cluster status
Node:     box-01 (47c8c765-...)
Cluster:  机房一层 (e0a48ae5-...)
Nodes:    3 / unlimited (Enterprise edition)
Routing:  local_first, prefer local true, state active

NAME                 HEALTH   STATE   ADDRESS              GPU               VRAM           LOADED  INFLIGHT
box-01 (this node)   healthy  active                       NVIDIA RTX 4090   9.1/24.0 GB    1/4     1
box-02               healthy  active  192.168.1.22:11438   NVIDIA RTX 4090   0.8/24.0 GB    0/3     0
box-03               down     active  192.168.1.9:11438    ...
```
