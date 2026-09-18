# 局域网算力集群（EE）设计

> 对应需求：[OpenCSGs/csglite#172](https://github.com/OpenCSGs/csglite/issues/172)
> "构建局域网分布式计算集群：自动发现与连接局域网内的多台计算机设备，将其转化为
> 统一的算力节点池"。集群功能对社区版开放到 2 个节点，更多节点是 EE 能力，
> 遵循 `docs/guides/ee-license-design.md` 与 `docs/agent-guidelines/ee-features.md`
> 的配额门控约定（第 11 节）。已定决策：会话亲和（7.4）、故障切换（7.5）、
> CE 2 节点（11）、现有 API 兼容（8.3）。

## 1. 背景与目标

### 1.1 用户场景

用户现场有多台运行 CSGLite 的算力盒子（下称"节点"），典型情况：

- 每台盒子各有一块或多块 GPU，独立安装 CSGLite，模型各自下载在本机。
- 盒子通过 DHCP 接入同一个局域网，**每次重启 IP 都可能变化**，也可能随时
  关机、掉线、再上线。
- 使用者希望把这些盒子当成一个整体使用：一个入口地址、一份模型列表，
  请求自动落到有该模型、且最空闲的盒子上；单台盒子的并发瓶颈被多台分担。
- 使用者是团队而不是单人，盒子多为无显示器的设备，日常通过某一台的 Web UI
  或 API 使用。

### 1.2 目标

1. **零配置发现**：同一局域网内的 CSGLite 节点自动互相发现；节点重启、IP
   变化后无需任何人工操作，一分钟内自动恢复到集群中。
2. **一个入口**：任意一台成员节点的 `/v1/*`、`/api/chat` 等接口都能代表整个
   集群响应；调用方不需要知道模型在哪台盒子上。
3. **按模型与负载调度**：请求只发给已经下载了该模型的节点；优先选择模型已
   加载、并发槽位空闲、显存充足的节点；节点失败自动切换。
4. **安全**：只有配对过的节点之间才互相转发请求；节点间通信加密；不把节点
   的管理接口暴露给局域网里的任意设备。
5. **不改变单机体验**：未加入集群的节点行为与今天完全一致；加入集群后现有客户端不改配置也能照常工作（第 8.3 节）。
6. **复用现有能力**：调度、限流、故障切换、语义路由沿用已有的 provider
   pool 框架；跨节点追踪沿用已有的 `X-CSGLite-Trace-ID` 传播。

### 1.3 非目标（第一期）

- 不做单个模型跨机器切分（张量并行 / 流水线并行），不做显存池化。一条请求
  自始至终在一个节点上完成。跨机器切分作为第三期可选项评估（第 10 节）。
- 不做跨公网 / 跨子网的集群，不做云端中继。目标是同一个二层网络。
- 不做训练、微调任务的分发。
- 不做多租户和节点级权限。

## 2. 业界方案调研

### 2.1 NVIDIA PAIR（Personal AI Router）

- 仓库 [NVIDIA/Personal-AI-Router](https://github.com/NVIDIA/Personal-AI-Router)，
  Apache-2.0，Go（服务端 13 个子进程）+ TypeScript（Electron 桌面端），
  公测版 v0.1.1 发布于 2026-08-28，支持 Windows / macOS / Linux 混合组网。
- **定位**：不是推理引擎，是一层"虚拟推理路由"。它代理本机 Ollama 与 LM
  Studio 的端口，对外暴露 Ollama 兼容与 OpenAI 兼容接口，把独立请求分发到
  局域网内其它已配对节点的引擎上。
- **发现**：每个节点通过 mDNS 广播一条 `_nvpair-node._tcp` 记录，TXT 里带
  `uuid=`、`cluster-uuid=`、`ip=` 与各服务端口；浏览目录**按节点 UUID 建
  索引**，IP 只是可变属性。另有手工添加节点（每 10 秒探测一次）作为 mDNS
  不可用时的兜底，以及每 15 秒一轮的 HTTP 扫描保证收敛。
- **防抖**：节点要连续 12 轮扫描（约 60 秒）都不可见才会被剔除，期间对其已
  知端口做 TCP 探测，任何响应立即恢复；正在流式返回推理结果也算"存活证明"。
- **配对与信任**：节点持有稳定 UUID + 自签名证书；配对使用 EAP-NOOB
  （RFC 9140）加六位 PIN 由人工带出带内通道；配对后双方互相钉住证书，
  节点间全部走 mTLS。集群没有主节点，成员关系对称。
- **调度**：单一策略，按"该节点排队中 + 运行中的请求数"加"平滑后的最大 GPU
  利用率（0–3 档压力）"排序；调度器本身不看模型，再按"该节点是否有该模型"
  过滤；产生一个有序的故障切换列表。已在文档中承认的局限：不看显存容量、
  GPU 型号、模型是否已加载。
- **对本项目的意义**：架构模式（对称节点、请求级路由、UUID 身份、mDNS +
  手工兜底、防抖、配对后 mTLS）与 CSGLite 的需求高度吻合，直接借鉴。
  **但不宜直接集成**：它只会代理 Ollama / LM Studio，不认识 CSGLite 管理的
  llama-server、模型 ID、keep-alive 与 License；引入后会出现两层路由。

### 2.2 其它开源方案

| 方案 | 类型 | 发现 / 组网 | 结论 |
|---|---|---|---|
| [exo](https://github.com/exo-explore/exo) | Python + MLX，模型跨设备切分（张量并行，2 台约 1.8x） | libp2p 自动发现，Thunderbolt 5 RDMA | 以 Apple Silicon 为中心，Linux 仅 CPU；与 NVIDIA 盒子不匹配，不采用 |
| [llama.cpp RPC](https://github.com/ggml-org/llama.cpp/blob/master/tools/rpc/README.md) | `ggml-rpc-server` 把远端 GPU 暴露为 ggml 设备，一个模型按显存比例切分到多机 | 无发现，`--rpc host:port` 静态配置 | 官方标注 PoC、"fragile and insecure"，无鉴权；目标是**放下更大的模型**而不是提速。作为第三期可选能力（第 10 节） |
| [GPUStack](https://gpustack.ai/) | Python，中心化 server + worker 集群管理器 | worker 用 `--server-url --token --worker-ip` 注册 | 完整的竞品级平台，体量大；其"注册令牌 + worker 身份"模式可借鉴 |
| [LiteLLM](https://docs.litellm.ai/docs/routing) / [llama-swap](https://github.com/mostlygeek/llama-swap) / [llmlb](https://github.com/akiojin/llmlb) | OpenAI 兼容网关 / 负载均衡 | 静态配置上游 | 没有发现与身份，IP 变化即失效；CSGLite 的 provider pool 已经覆盖这类能力 |

### 2.3 Go mDNS 库

- `github.com/pion/mdns/v2`：**已经是本仓库的间接依赖**（`pion/webrtc` 引入），
  纯 Go 应答器 + 查询器，Windows 下不依赖系统 Bonjour，可直接提升为直接依赖。
- `grandcat/zeroconf`：功能最全的 DNS-SD 实现但已停止维护，有多个 fork。
- `hashicorp/mdns`、`brutella/dnssd`：可用，但 IPv6 与多网卡处理口碑一般。
- PAIR 自己实现了应答器，原因是 Windows 没有系统级应答器；它以 `SO_REUSEADDR`
  与 `avahi-daemon` / Bonjour 共享 UDP 5353。

**结论**：优先使用 `pion/mdns/v2`（零新依赖），在 `ee/cluster/discovery` 里
封装成接口，便于替换和在测试里注入内存实现。

### 2.4 方案选择

自研，放在本仓库 `ee/cluster`（EE 代码；社区版可用到 2 个节点，见第 11 节），架构借鉴 PAIR，路由复用 CSGLite 已有的
provider pool。理由：

1. 节点本来就是 CSGLite 实例，每个节点已有 OpenAI 兼容接口、模型清单
   （`/v1/models`）、加载状态（`/api/ps`）、硬件信息（`/api/system`）、
   模型拉取（`/api/pull`）、API Key 鉴权与请求追踪，缺的只是"身份 +
   发现 + 配对 + 调度"这一薄层。
2. provider pool 已实现优先级、加权轮询、每成员 RPM / TPM / 并发上限、限流
   冷却、会话亲和、失败切换、语义路由（`internal/server/provider_pool_inference.go`）。
   把"节点"作为一种新的成员 `source`，这些能力对集群立即可用。
3. `internal/inference/remote.go` 里已有一个把请求转发给另一台 csghub-lite
   的 `remoteEngine`（当前由 CLI `chat` / `run` 用来连本机服务），它已能携带
   `ModelOptions`（上下文、并行度、GPU 层数），是节点间转发引擎的现成基础。

## 3. 核心概念

| 概念 | 定义 |
|---|---|
| 节点（Node） | 一个运行中的 CSGLite 实例。有**持久化的 UUID** 与自签名证书，在 `~/.csghub-lite/cluster/` 下生成一次、永久保留。IP、主机名、端口都是可变属性。 |
| 集群（Cluster） | 一组互相配对（互相钉住证书）的节点。有 UUID 与显示名。没有主节点，成员关系对称，每个节点都保存完整成员表。一个节点最多属于一个集群。 |
| 加入令牌（Join Token） | 集群级、可轮换的密钥，编码为 `csgl1-<clusterUUID>-<secret>`。持有者可把一个节点加入该集群。用于 CLI / 环境变量首启自动入网。 |
| 节点准入码（Node Code） | 每个**未入集群**节点持有的一次性短码（8 位，显示在该节点的 Settings 页与 `csghub-lite cluster code`）。从集群任一成员的 UI 里"邀请"已发现的节点时输入。 |
| 模型存在（Present） | 该节点本地已下载该模型。模型 ID 与单机一致（`namespace/name` 及量化后缀），同一个 ID 在多个节点上就是多份独立副本，这是它们可互换的前提。 |
| 模型已加载（Warm） | 该节点上该模型的 llama-server 正在运行（`/api/ps` 中 `running`）。 |
| 集群来源 | 推理请求的 `source` 新增两种取值：`cluster`（由调度器选节点）与 `node:<uuid>`（钉住某个节点）。与现有 `local` / `cloud` / `provider:<id>` / `pool:<id>` 并列。 |

## 4. 身份与 IP 变化的处理

这是 issue 中最关键的现场约束，处理原则是**身份与地址彻底分离**：

1. **身份不依赖 IP**。节点 UUID、私钥、证书在首次启动时生成并持久化。
   配对时双方钉住的是"UUID ↔ 证书指纹"，与地址无关。同一台盒子换了 IP，
   在其它节点眼里仍是同一个成员，只是地址变了。
2. **地址持续刷新，多源合并**。每个节点维护一份按 UUID 索引的目录，条目的
   地址可来自任一来源，取最新观测：
   - mDNS 广播 / 浏览（主来源，秒级）；
   - 成员表 gossip：成员之间每 15 秒交换一次成员表（含各自"最近观测到的
     对方地址"），漏掉一次 mDNS 广播的节点能从任一同伴处学到新地址；
   - 上次已知地址：成员表持久化了每个成员最近三次成功连接的地址，节点启动
     时在 mDNS 结果到达前就先探测这些地址；
   - 手工静态地址：可以给某个成员配置主机名或 IP（例如做了 DHCP 保留或
     `.local` 主机名），每 10 秒探测一次，作为多播被禁网络的兜底。
3. **本机地址变化时主动重播**。监听网卡地址变化（`netlink` / 轮询接口列表），
   变化后立即重新广播 mDNS 记录并向所有成员推送一次成员表。
4. **防抖，不误剔**。沿用 PAIR 的参数：连续 12 轮（每轮 5 秒）不可见才标记
   `offline`；从第 3 轮起对其已知地址做 TCP 探测，任何响应即恢复；正在返回
   推理流也算存活。`offline` 只影响调度，不删除成员；只有人工"移除"才删除。
5. **一次扫描全空视为本机故障**。已知成员多于一个而某次扫描一个都没发现，
   连续 6 轮内不惩罚任何成员（多为本机网卡切换或休眠唤醒）。
6. **多播不可用时仍可组网**。企业 Wi-Fi、VLAN 或 Docker 默认网络可能不转发
   多播。此时：Docker 建议 `--network host`；或给任一成员配置静态地址，
   其它成员通过 gossip 学到全部地址；或在同一子网退化为 UDP 广播探测
   （第二期）。

对使用者的承诺：**盒子重启、IP 变化后，60 秒内自动回到集群，期间请求由其它
节点承接；无需任何人工操作。**

## 5. 组件设计

### 5.1 目录与许可证

| 路径 | 许可证 | 内容 |
|---|---|---|
| `ee/cluster/` | CSGLite EE License | 身份、发现、配对、成员表、遥测、调度、节点间监听器、节点转发引擎、HTTP handler 实现。社区版在 2 节点配额内可用，许可证文本需相应补一句，见第 11 节 |
| `internal/server/` | Apache-2.0 | 只保留最小挂载点：路由注册、`getChatEngine` 的一处回调、`Server.Run` 中的启动 / 停止调用、Dashboard 的节点摘要接口 |
| `internal/license/features.go` | Apache-2.0 | 新增功能与配额定义 |
| `internal/cli/cluster.go` | Apache-2.0 | CLI 子命令，仅调用本机 HTTP 接口 |

`ee/cluster` 下每个源文件带 `ee/README.md` 规定的文件头。

### 5.2 包结构

```
ee/cluster/
  identity/     节点 UUID、密钥与证书的生成与加载（cluster/identity.json, node.key, node.crt）
  discovery/    Discoverer 接口；mdns 实现（pion/mdns/v2）；static 实现；memory 实现（测试）
  membership/   集群与成员表（cluster/cluster.json）、加入令牌、准入码、钉住证书、gossip
  telemetry/    对成员的状态采样（2 秒健康节奏，失败 4/8/16/30 秒退避）与本机状态汇总
  scheduler/    候选过滤与排序，产出有序故障切换列表
  transport/    节点间 mTLS 监听器与客户端；证书钉住校验
  engine/       nodeEngine：实现 inference.Engine + ChatCompletionProxier + EmbeddingsProxier + NativeToolStreamer
  api/          /api/cluster/* 管理接口 handler；/cluster/v1/* 节点间接口 handler
  cluster.go    Manager：把以上组件装配起来，暴露给 internal/server 的窄接口
```

`internal/server` 只依赖一个接口：

```go
// internal/server/cluster.go（Apache-2.0）
type clusterRouter interface {
    // Enabled 表示本节点已加入集群且至少有一个可用的其它成员。
    Enabled() bool
    // ChatEngine 返回能服务该模型的引擎；source 为 "cluster" 或 "node:<uuid>"。
    ChatEngine(ctx context.Context, modelID, source string, opts inference.ModelOptions) (inference.Engine, error)
    // Models 返回集群范围内可用的模型（供 /v1/models 与 Chat 页合并展示）。
    Models(ctx context.Context) []ClusterModel
}
```

未加入集群（或 `CSGHUB_LITE_CLUSTER_DISABLED=1`）时 `s.cluster` 为 `nil`，所有调用点短路，行为与今天一致。

### 5.3 网络端口

| 端口 | 监听地址 | 用途 | 鉴权 |
|---|---|---|---|
| 11435（现有） | 配置决定 | Web UI、管理 API、对外推理 API | 现有 API Key；加入 / 邀请入口另做节点数配额校验 |
| 11436（现有，桌面） | `0.0.0.0` | 桌面模式仅推理监听器 | 不变 |
| **11438（新增）** | `0.0.0.0` | 节点间控制与转发：`/cluster/v1/*` | **mTLS + 证书钉住**；加入握手除外（见 6.1） |
| UDP 5353 | 多播 `224.0.0.251` / `ff02::fb` | mDNS `_csglite-node._tcp` | 无（只广播非敏感信息） |

单独开一个节点间端口而不是复用 11435，原因：

- 11435 上大量管理路由今天没有鉴权（依赖回环放行），把它暴露给局域网不安全；
- 节点间需要 mTLS 双向校验，与面向浏览器的 11435 的 TLS 策略不同；
- 转发进来的推理请求要有"只在本机执行、绝不再转发"的硬约束，独立端口天然
  区分入口流量与转发流量。

节点间端口可通过 `CSGHUB_LITE_CLUSTER_ADDR` 覆盖；mDNS TXT 里携带实际端口，
不要求所有节点一致。

### 5.4 mDNS 记录

服务类型 `_csglite-node._tcp`，实例名为节点 UUID 前 8 位加主机名。TXT：

```
v=1                       协议版本
uuid=<node uuid>
cluster=<cluster uuid>    未入集群时省略
port=11438                节点间端口
api=11435                 对外 API 端口（供 UI 跳转）
name=<显示名>
ver=<csglite 版本>
```

模型清单、GPU 信息等体积大的数据**不放 TXT**（PAIR 的经验：TXT 装不下），
通过 `/cluster/v1/status` 拉取。

### 5.5 持久化

全部放在 `~/.csghub-lite/cluster/`，与 `providers.json`、`provider_pools.json`
同级，**不新增 `config.json` 顶层键**（依据 `docs/agent-guidelines/config-schema.md`：
成员表是运行时协商出来的状态，且需要 0600 权限单独保护私钥）：

```
cluster/
  identity.json      {"uuid":"…","created_at":"…","name":"box-01"}
  node.key           ECDSA P-256 私钥，0600
  node.crt           自签名证书，10 年，SAN 为 UUID（URI 形式），不含 IP
  cluster.json       见下
```

```json
{
  "version": 1,
  "cluster": {"uuid": "…", "name": "机房一层", "created_at": "…"},
  "join_token_hash": "sha256:…",
  "settings": {"accept_work": true, "state": "active", "weight": 100, "prefer_local": true,
               "routing_mode": "local_first", "affinity_max_queue": 2,
               "replication": {"auto": false, "min_replicas": 1}, "disk_reserve_gb": 50,
               "static_addresses": {}},
  "members": [
    {
      "uuid": "…",
      "name": "box-02",
      "cert_fingerprint": "sha256:…",
      "joined_at": "…",
      "last_addresses": ["192.168.1.22:11438", "192.168.1.9:11438"],
      "static_address": ""
    }
  ]
}
```

用户可调的少量设置（`accept_work`、`prefer_local`、静态地址）也放在这个文件的
`settings` 段，保持一个概念一个真源。环境变量：

| 变量 | 作用 |
|---|---|
| `CSGHUB_LITE_CLUSTER_JOIN_TOKEN` | 首次启动且未入集群时，自动用该令牌加入（盒子出厂预置 / 批量部署） |
| `CSGHUB_LITE_CLUSTER_ADDR` | 节点间监听地址，默认 `:11438` |
| `CSGHUB_LITE_CLUSTER_SEEDS` | 逗号分隔的静态种子地址，多播不可用时使用 |
| `CSGHUB_LITE_CLUSTER_DISABLED` | `1` 时完全不启动集群组件（即使有 License） |

## 6. 配对与安全

### 6.1 三种入网方式，一套握手

**方式 0：共享密钥自动组网（已定为默认交付方式）**

安装时指定 `CSGHUB_LITE_CLUSTER_SECRET`（安装脚本写入 `config.json` 的
`cluster.secret`，也可 `csghub-lite config set cluster_secret`）。集群 UUID 由
密钥经 UUIDv5 派生，加入令牌的密钥部分由密钥经 HMAC 派生，因此所有持有同一
密钥的节点对"集群是谁、令牌是什么"有一致答案，而密钥本身既不落盘也不上网：

1. 节点启动后先监听发现结果 8–14 秒（含按 UUID 的固定抖动），看到有节点广播
   派生出的集群 UUID 就用派生令牌走方式 A 的握手加入；
2. 宽限期内没看到就自己建群（`CreateDerived`），之后成为别人的加入目标；
3. 两台同时建群会得到同一个集群 UUID 的两个单节点集群；任一方在发现列表里
   看到"同集群 UUID 但不在我成员表里"的节点，就对它发起同一握手并把成员表
   取并集（`mergeVia`），几秒内合并为一个集群；
4. 节点被操作者显式 `leave` 时暂停自动组网（`settings.auto_form_paused`），
   避免几秒后被自动拉回；任何 `create` / `join` 恢复；
5. 节点已在另一个（非派生）集群时，自动组网不干预，操作者的决定优先。

节点数配额照常在接收加入的一侧校验。

**方式 A：令牌入网（推荐用于盒子批量部署）**

1. 在任一节点创建集群：UI "创建集群" 或 `csghub-lite cluster create --name 机房一层`。
   本节点生成集群 UUID 与加入令牌，只保存令牌哈希。
2. 在其它盒子上执行 `csghub-lite cluster join <token>`，或首启前设置
   `CSGHUB_LITE_CLUSTER_JOIN_TOKEN`。
3. 加入方从令牌里解出集群 UUID，通过 mDNS 找到任一 `cluster=<uuid>` 的成员
   （找不到则使用 `--address` 或 `CSGHUB_LITE_CLUSTER_SEEDS`）。
4. 握手：加入方对成员的 11438 发起 TLS 连接（首次不校验证书），发送
   `{uuid, name, cert, hmac(token, uuid || cert_fingerprint || nonce)}`；
   成员用令牌校验 HMAC，通过后钉住加入方证书，返回完整成员表（含各成员证书
   指纹与最近地址）与集群信息，并把新成员 gossip 给其它节点。
5. 双方之后全部走 mTLS，证书校验规则是"对方证书指纹必须等于成员表里该 UUID
   钉住的指纹"，不看 CA、不看主机名、不看 IP。

**方式 B：UI 邀请（推荐用于少量盒子、有人操作）**

1. 在集群任一成员的 Web UI "算力集群" 页，"发现的节点" 列表里出现了未入集群
   的盒子（mDNS 看到、`cluster=` 为空）。
2. 点击"邀请加入"，输入该盒子 Settings 页 / `csghub-lite cluster code` 显示的
   8 位准入码。
3. 邀请方连接该盒子的 11438，发起与方式 A 相同的握手，只是由准入码代替令牌做
   HMAC，且角色互换：邀请方把成员表推给被邀方。准入码一次有效，10 分钟过期。

PAIR 采用 EAP-NOOB + 六位 PIN；本设计用"令牌 / 准入码做 HMAC 的 TOFU 握手"
达到同等的防冒充效果（攻击者不知道令牌就无法完成握手；令牌不在网络上明文
传输），实现量小得多。若后续需要更强的形式化保证，可以直接引入 PAIR 的
`eap-noob` Go 包（Apache-2.0）替换握手层，接口不变。

### 6.2 运行期安全规则

- 节点间接口一律 mTLS + 指纹钉住；未配对节点连接 11438 除 `/cluster/v1/join`
  外全部 `403`。
- 转发过来的推理请求带 `X-CSGLite-Routed: <hop uuid>`，接收方**只在本机执行**，
  即使本机没有该模型也直接返回 `404`，绝不再转发，杜绝环路。
- 通过 11438 收到的请求不携带、也不需要对外 API Key；对外 API Key 校验只发生
  在入口节点的 11435 / 11436。入口节点的 `X-CSGLite-Trace-ID`、`X-Request-ID`
  沿 `correlation.ApplyRequestHeaders` 透传到执行节点，实现跨节点追踪。
- 加入令牌可轮换（`POST /api/cluster/token/rotate`），轮换只影响后续入网，
  不影响已配对成员。移除成员即删除其钉住指纹，并 gossip 给所有节点。
- License：功能本身不门控，只门控节点数（第 11 节）。接收加入 / 邀请的节点
  校验 `license.Limit(QuotaMaxClusterNodes)`；成员数超出本节点当前配额
  （例如 License 过期后回落到 2 而集群有 5 台）时，该节点拒绝接收转发、
  在 gossip 与 `status` 中上报 `licensed=false`，调度器不选它，但不退出集群。

## 7. 遥测与调度

### 7.1 节点状态（`GET /cluster/v1/status`）

```json
{
  "uuid": "…", "name": "box-02", "version": "0.12.0", "licensed": true,
  "accept_work": true,
  "state": "active",
  "gpus": [{"index": 0, "name": "NVIDIA RTX 4090", "vram_total": 25769803776, "vram_used": 9126805504,
            "util": 37, "temperature": 71, "power_draw": 310, "power_limit": 450, "throttled": false}],
  "cpu": {"cores": 16, "load1": 3.2, "util": 22},
  "ram": {"total": …, "used": …, "unified": false},
  "disk": {"path": "/data/models", "total": 2000000000000, "free": 640000000000,
           "read_mbps_class": "nvme", "io_busy": false},
  "net": {"link_mbps": 10000, "tx_mbps": 12, "rx_mbps": 3},
  "jobs": {"pulling": ["Qwen/Qwen3-14B-GGUF:Q4_K_M"], "converting": [], "syncing": []},
  "model_source": {"server_url": "https://csghub.corp.example", "hf_endpoint": "", "modelscope_endpoint": ""},
  "models": [
    {"id": "Qwen/Qwen3-32B-GGUF:Q4_K_M", "size": 19800000000, "loaded": true,
     "slots": 4, "active": 1, "expires_at": "…", "n_gpu_layers": -1,
     "native_tool_streaming": true,
     "perf": {"decode_tps": 38.5, "prompt_tps": 1450, "load_seconds": 21}},
    {"id": "Qwen/Qwen3-8B-GGUF:Q4_K_M", "size": 5030000000, "loaded": false,
     "perf": {"load_seconds": 6}}
  ],
  "inflight": 1, "loading": []
}
```

数据源：`/api/ps`（加载状态、槽位、`activeRequests`）、`/api/system`（GPU /
显存 / 内存）、本地模型清单、pull job 与转换任务状态。以下是**新增采集**：

| 字段 | 来源 | 用途 |
|---|---|---|
| `gpus[].util / temperature / power_* / throttled` | `nvidia-smi --query-gpu=utilization.gpu,temperature.gpu,power.draw,power.limit,clocks_throttle_reasons.active`；ROCm 用 `rocm-smi`；Apple 无则为 `null` | 负载与热降频惩罚 |
| `cpu.load1 / util` | `/proc/loadavg`、`host_statistics`、`PDH` | 部分卸载（`n_gpu_layers` 未全上 GPU）的模型与 prompt 处理受 CPU 影响 |
| `ram` | 现有 `/api/system` | GGUF `mmap` 需要页缓存；统一内存平台显存即内存 |
| `disk` | `statfs` / `GetDiskFreeSpaceEx` 对 `ModelDir`；`read_mbps_class` 启动时读取 256 MB 模型文件测一次（nvme / ssd / hdd / network） | 冷启动时间估算、副本放置、同步是否允许 |
| `net` | 接口链路速率与 5 秒平均吞吐 | 判断内网源拉取是否受网卡限制；未来级联复制用 |
| `jobs` | pull / convert / sync 任务 | 磁盘 IO 繁忙时不安排冷启动；同步不打扰推理 |
| `models[].perf` | 入口与执行节点分别按请求计量：`completion_tokens / 生成时长`、`prompt_tokens / 首字时长`、加载耗时，取 EMA 持久化到 `cluster/perf.json` | 异构 GPU 之间按实测吞吐排序，而不是猜 GPU 型号 |
| `state` | `active` / `drain`（只收尾不接新）/ `maintenance`（同时不参与同步） | 运维可控的下线 |
| `model_source` | 该节点的 `server_url` 等模型源配置 | 7.8 节的模型源一致性检查 |

任何字段缺失都取"中性"值，只影响排序不影响可用性。

采样节奏沿用 PAIR：健康节点每 2 秒（带稳定抖动），连续失败退避 4 / 8 / 16 /
30 秒，成功后复位。本机状态走进程内直接读取。

### 7.2 调度算法（第一期）

输入：模型 ID、请求（用于亲和键）、成员目录快照。输出：有序候选列表。

1. **硬过滤**：`online && licensed && accept_work && 模型 present`。
   `source=node:<uuid>` 时只保留该节点；不满足则 `404 model not available on node`。
2. **排序**：会话亲和（详见 7.4）命中且节点仍可接单则置顶；其余候选按
   7.7 节的预计完成时间打分，低者优先；`prefer_local` 开启时同分优先本机，
   否则按 UUID 稳定抖动打散。
3. **故障切换**：详见 7.5。
4. **入口自增预留**：调度器把自己刚派发但遥测尚未反映的请求计入该节点的
   `active`，避免同一瞬间的并发请求全部压到同一节点（PAIR 同样处理）。
5. **冷启动去重**：某模型在集群里没有 warm 副本时，只选一个节点冷启动，并在
   它加载期间（`/api/ps` 状态为 `loading`）把该模型的后续请求继续排给它，
   避免同一个模型在所有盒子上同时加载、互相争抢显存。

响应头新增 `X-CSGLite-Node: <uuid>` 与 `X-CSGLite-Node-Name`，与现有
`X-CSGLite-Pool-*` 一致，供调用方与可观测性页面展示。

### 7.3 与 provider pool 的关系

- `ProviderPoolMember.Source` 允许 `cluster` 与 `node:<uuid>`。于是可以配置
  "集群优先、云端兜底"、"集群 + 第三方 API 语义路由"这类池，而不需要在集群
  组件里重复实现优先级和限流。
- `source=cluster` 直接请求时，内部等价于一个由调度器动态生成的、成员为候选
  节点的临时池，走同一套 `admit` / 失败切换代码路径，只是成员顺序由调度器
  给出而不是静态权重。
- `getChatEngine` 的分派顺序调整为：`pool:` → `provider:` → `cloud` →
  `node:` / `cluster` → 本机 → 本机失败后的自动兜底链里，在"第三方 provider"
  之前插入"集群中存在该模型的其它节点"（仅当 `s.cluster != nil`）。这样默认
  情况下（不传 `source`）本机没有的模型会自动落到集群其它节点，本机有的模型
  仍优先本机，不改变单机行为。

### 7.4 会话亲和

**为什么需要**：llama-server 的 KV / prompt cache 只在进程内有效。多轮对话每次
都带完整历史，落到同一节点可以复用前缀缓存，首字延迟通常从秒级降到百毫秒级；
落到别的节点则整段重算。Agent 类调用（tools 循环、Responses API 连续调用）
对此尤其敏感。

**亲和键**，按优先级取第一个可用的：

| 优先级 | 来源 | 说明 |
|---|---|---|
| 1 | 请求头 `X-CSGLite-Thread-ID` | 已有约定，Chat 页与 AI Apps 已在传；第三方客户端可显式传 |
| 2 | `/v1/responses` 的 `previous_response_id` | Responses API 目前无状态（不存历史），但同一链路的 id 前缀可作键 |
| 3 | `/api/chat` 的会话 id（Web Chat 内部请求） | 由前端在调用时带 `X-CSGLite-Thread-ID`，不新增字段 |
| 4 | API Key + 消息前缀哈希 | 复用 `providerPoolRequestAffinityKey`：对 system 与首条 user 消息取 SHA-256。同一对话后续轮次前缀不变，天然命中 |

**两层实现，多入口也一致**：

1. **确定性基线**：对（亲和键，候选节点集合）做 rendezvous 哈希（HRW）。任何
   一台入口节点在相同候选集下算出同一个目标节点，不需要入口之间同步状态；
   节点加入 / 离开时只有落在该节点上的键会迁移，其它键不动。
2. **入口本地覆盖表**：入口记录"该键上一次实际执行的节点"，TTL 30 分钟滑动
   （沿用 `providerPoolAffinityTTL`），优先级高于基线。它覆盖的是"基线选了 A
   但 A 当时满载、实际去了 B"这类情形，让后续轮次跟着 B 走。

**何时打破亲和**（按顺序判断，任一命中则改走调度器正常排序并重新钉住）：

- 亲和节点 `offline` / `suspect` 达到熔断阈值（见 7.5）。
- 亲和节点上该模型已被驱逐且节点显存不足以重新加载（缓存本来也已丢失）。
- 亲和节点排队深度超过 `affinity_max_queue`（默认 2）：llama-server 自身会
  排队，短暂排队比换节点重算更快，但队列过深就溢出到其它节点。该阈值
  在集群设置里可调，设为 0 表示永不排队、立即溢出。
- 请求显式指定了 `source=node:<uuid>`（钉住优先于亲和）。

**给调用方的控制手段**：响应头 `X-CSGLite-Node` 告知实际执行节点；客户端若要
强一致，下一次请求带 `source=node:<uuid>`；若要打散，换一个
`X-CSGLite-Thread-ID` 即可。UI 的可观测性页面按线程展示节点序列，便于确认
亲和是否生效。

**不做的事**：不做跨节点的 KV cache 迁移，不做会话状态同步。亲和是尽力而为
的优化，打破亲和只损失延迟，不影响正确性。

### 7.5 故障切换

**节点健康状态机**（每节点一份，另按（节点，模型）维护一份用于模型级故障，
例如某一台上的模型文件损坏）：

```
healthy ──1 次请求失败或 1 次遥测失败──▶ suspect ──连续 3 次失败或 60 秒遥测不可达──▶ down
   ▲                                          │                                      │
   └──────── 1 次成功 ────────────────────────┘                    每 10 秒半开探测 ──▶ probing
                                                                   探测成功 ──▶ healthy
```

- `suspect` 节点仍可作候选，但排到同分节点之后；`down` 节点不作候选。
- `probing` 用一次真实的 `status` 拉取加一条最小推理请求（`max_tokens=1`）
  验证，成功才恢复，避免"遥测正常但推理挂了"。
- （节点，模型）级 `down` 持续 5 分钟后自动重试一次；节点级由上面的状态机
  管理。

**按请求阶段的切换规则**：

| 阶段 | 触发 | 动作 |
|---|---|---|
| 建连 | 连接拒绝 / 超时（3 秒） | 立即切到下一候选，节点记 1 次失败 |
| 首字节前 | `5xx`、`503 model loading` 超过加载超时、`429` | 切到下一候选；`429` 按 `Retry-After` 进入现有 `coolOnRateLimit` 冷却，只记（节点，模型）级失败 |
| 首字节前 | 首字节超时：warm 60 秒；cold 用现有模型加载超时；可按模型在 `ModelRuntimeSettings` 覆盖 | 切到下一候选 |
| 流式已开始 | 连接中断、token 间隔超时（120 秒） | **不再切换**，向客户端发送标准 SSE 错误块并结束流；节点记 1 次失败 |
| 非流式已开始 | 同上 | 若已用时间 < 首字节超时且候选未耗尽，允许**重试一次**（请求本身幂等），否则返回 `502` |
| 候选耗尽 | 全部候选失败或过滤后为空 | 返回与今天相同的错误信封，状态 `503`，`code: "cluster_no_available_node"`，附 `tried: [uuid…]` |

前提：转发时请求体已在 `handleOpenAIChatCompletionsProxy` 中序列化为字节，
天然可重放；超过 16 MB（多模态大图）的请求体不做跨节点重试，失败直接返回。

**执行节点侧**：

- 收到转发请求即在本机执行，本机没有该模型直接 `404`，由入口切换，绝不再转发。
- 优雅下线：`cluster leave`、`shutdown`、`accept_work=false` 时先 gossip
  "不再接单"，再等待在途请求最多 30 秒，然后才关闭监听器。入口收到该状态后
  不再派新请求，在途流不受影响。
- 节点异常掉电：在途流由入口按上表处理；该节点的会话亲和键在其
  `down` 期间重新哈希到其它节点。

**入口节点自身**：客户端只指向一个地址时入口是单点。第一期的处理是"任一
成员都是入口"，客户端可配多个 base URL 自行切换；并在 UI 建议给作为入口的
盒子做 DHCP 保留或使用 `.local` 主机名。浮动 IP（VRRP / keepalived）或集群
级 DNS-SD 别名作为第三期评估项。

**验收指标**：拔掉正在服务的盒子网线，其它节点 60 秒内接管；未开始返回的
请求 100% 在其它节点成功，已开始流式返回的请求收到明确错误而非挂起。

### 7.6 模型分布与同步

- `GET /api/cluster/models` 返回集群范围的"模型 × 节点"矩阵（present / warm /
  none），是 UI 的核心视图，也是 `/v1/models` 合并展示的数据源
  （`/v1/models` 返回项新增 `nodes` 字段，仅在集群启用时出现）。
- 第二期：`POST /api/cluster/nodes/{uuid}/models/pull {model}` 让某节点拉取模型，
  底层即转发到该节点的 `/api/pull/jobs`，进度通过现有 pull job 接口跟踪。
  同一模型在多个节点上有副本，调度才有空间，这一步是集群发挥价值的前提，
  UI 上做成"同步到其它节点"一键操作。
- 模型分发以私有化 CSGHub 为内网源（7.8），"同步到节点"就是让目标节点从内网源 `pull`。

### 7.7 资源感知调度：负载、磁盘、热、异构 GPU

第一期调度器不是简单的"warm 优先 + 槽位"，而是对每个候选估算**这条请求的
预计完成时间**，再排序。所有输入来自 7.1 的遥测，缺失项取中性值。

**硬过滤（不满足直接排除）**

| 条件 | 说明 |
|---|---|
| `online && licensed && state == active && 模型 present` | 基本条件 |
| 冷启动时 `vram_free ≥ size × 1.2`（统一内存平台看 `ram.free`） | 装不下就不选，避免触发驱逐把别的模型挤掉；全部候选都装不下时才允许驱逐，且只选一台 |
| `(节点, 模型)` 未处于模型级熔断（7.5） | 例如该节点上文件损坏 |

**预计完成时间**

```
T = T_ready + T_queue + T_prompt + T_decode

T_ready  = 0                                （warm）
         = perf.load_seconds 或 size / disk_read_mbps  （cold；磁盘 io_busy 时 ×2）
T_queue  = max(0, active - slots + 1) × avg_request_seconds（warm，超出槽位才排队）
T_prompt = prompt_tokens / perf.prompt_tps
T_decode = est_completion_tokens / perf.decode_tps      （est 取请求 max_tokens 与该模型历史均值的较小者）
```

`perf` 没有样本时用同 GPU 型号在集群内的样本，再没有则用模型大小估一个粗
值；估错只影响首批请求，EMA 很快收敛。

**乘法惩罚（把 T 放大）**

| 因素 | 惩罚 | 原因 |
|---|---|---|
| GPU 热降频 `throttled=true` 或温度 ≥ 85 °C | ×1.5 | 实际吞吐会掉，且继续加压会更热 |
| GPU 功耗 ≥ 95% 上限 | ×1.2 | 已到功耗墙 |
| 部分卸载模型（`n_gpu_layers` 未全上 GPU）且 `cpu.load1 / cores > 0.8` | ×1.5 | CPU 层的解码受 CPU 争抢 |
| `ram.free < size × 0.5`（GGUF `mmap` 页缓存不足） | ×1.3 | 权重反复换页 |
| 节点正在 `pulling / converting / syncing` 且本次需要冷启动 | ×1.5 | 磁盘带宽被占，加载慢 |
| 节点非 LLM 的 GPU 占用高（`util` 高但本节点无在途推理） | ×1.3 | 有别的进程在用 GPU（PAIR 的"图形应用"情形） |
| 管理员权重 `weight`（默认 100，范围 10–200） | ×(100 / weight) | 让某台机器少接或多接 |

**为什么用"预计时间"而不是分层**：分层规则（warm 优先、再看槽位）在异构
集群下会把请求全部压到一台 warm 的低端卡上，而旁边 4090 是 cold 的。用时间
估算后，"cold 的 4090 加载 6 秒 + 快速解码"与"warm 的 3060 排队 + 慢解码"能
直接比较；实测 `perf` 让排序自动适应真实硬件差异，不需要维护 GPU 型号表。

**可解释**：`GET /api/cluster/explain?model=…&prompt_tokens=…&max_tokens=…`
返回每个候选的过滤结果、各项估算与惩罚、最终排序。UI 在模型矩阵里提供
"为什么会选这台"，测试也用它做断言。

**副本放置建议（模型该放在哪些节点）**：与请求调度分开的一个周期任务
（每 5 分钟），输入每个模型的请求速率、排队时长、溢出次数、现有副本数，
候选节点的显存是否装得下、磁盘余量、磁盘速度、GPU 实测吞吐，输出
"建议把模型 X 同步到节点 Y" 列表，UI 一键执行；`replication.auto=true` 时按
`min_replicas`（默认 1）自动执行。放置时**磁盘因素**：

- `disk.free - size < reserve`（默认 50 GB）的节点不作为同步目标，也在 UI
  标红；
- 优先 `nvme > ssd > hdd` 的节点，冷启动快；
- 由集群同步过来的副本标记 `origin=cluster`，磁盘余量低于阈值时按 LRU 自动
  删除这类副本；用户自己下载的模型永不自动删。

**运维状态**：`state=drain` 让节点收尾在途请求后不再接新请求（升级、换卡前
用）；`maintenance` 再加上不参与同步与放置。CLI：`csghub-lite cluster drain
<node>` / `activate <node>`。

### 7.8 模型在节点间的分发：以私有化 CSGHub 为内网源（已定）

多台盒子对外网带宽通常只有几十到几百 Mbps，而局域网是 1–10 Gbps。**已定
方案：客户内网部署私有化 CSGHub，所有盒子以它为模型源**，公网到内网只由
CSGHub 同步一次，盒子之间不做 P2P。

**为什么选它**

- 零新代码：CSGLite 的模型源本来就可配置（`server_url`、`huggingface_endpoint`、
  `modelscope_endpoint`），盒子配同一个内网地址即可；模型 ID 天然一致。
- 与产品线一致："CSGLite 集群 + 私有化 CSGHub" 就是 OpenCSG 的完整企业方案，
  模型的审核、版本、权限、公网同步都由 CSGHub 承担，CSGLite 不再重复实现
  分发与治理。
- 运维最简单：没有额外进程、端口与协议；CSGHub 是已有的运维对象。

**集群侧需要做的事（很少）**

| 项 | 说明 |
|---|---|
| "同步到节点" = 远程 `pull` | `POST /api/cluster/models/{model}/sync {nodes}` 在目标节点各创建一个普通 pull job（经 11438 转发到其 `/api/pull/jobs`），进度沿用现有接口。目标节点从内网 CSGHub 拉取，速度只受内网与磁盘限制。 |
| 模型源一致性检查 | 每个节点在 `status` 中上报自己的模型源（`server_url` 等）。源不一致的成员在 UI 标黄并说明"同一模型 ID 可能指向不同内容"，`sync` 对源不一致的目标节点拒绝执行。 |
| 内网源检测 | `sync` 前检查目标节点的模型源是否可达；不可达时提示"该节点无法访问模型源"，不盲目创建任务。 |
| 公网源的提示 | 若集群模型源是公网地址（没有私有化 CSGHub），`sync` 到 N 台就是 N 次外网下载。UI 明示这一点并显示预计流量；不阻止。 |
| 副本放置建议 | 7.7 节的建议照旧，一键执行即上述远程 `pull`。 |

**未来可选：流式级联复制（无内网源客户的兜底，不在本期计划）**

若某客户没有私有化 CSGHub 又对外网流量敏感，可加一层轻量的节点间复制：
一个节点从外网顺序下载时，其它节点对它不断增长的 `.part` 做 Range 读，读到
末尾等待新数据，第三个节点再从第二个读；每节点上传连接上限默认 3，多台同步
时自动成树。无分片、无位图，复用现有 Range 续传与 sha256 校验，约 2 天。
接口预留 `GET /cluster/v1/files/{sha256}?offset=`，代码位置 `ee/cluster/sync`。
分片 swarm、嵌入 BitTorrent 库、Dragonfly 均已评估并否决：前两者在千兆 / 万兆
内网、3–20 台盒子的规模下没有收益，Dragonfly 对盒子场景过重。

## 8. 接口

### 8.1 管理接口（11435，`/api/cluster/*`；`POST /api/cluster/join` 与 `POST /api/cluster/invite` 受节点数配额约束）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/cluster` | 本节点身份、集群信息、成员列表及实时状态、设置 |
| GET | `/api/cluster/summary` | Dashboard 用的轻量摘要：节点卡片字段、总量、节点数与上限（CE / EE 同样可用） |
| POST | `/api/cluster` | 创建集群 `{name}`；已在集群则 `409` |
| DELETE | `/api/cluster` | 离开集群（通知全体成员，清空成员表，保留节点身份） |
| POST | `/api/cluster/join` | `{token, address?}`；返回集群信息与成员表 |
| GET | `/api/cluster/token` | 当前加入令牌（仅回环 / 桌面会话可读，与 `/api/license` 同样加 `licenseOriginGuard` 同源保护） |
| POST | `/api/cluster/token/rotate` | 轮换加入令牌 |
| GET | `/api/cluster/code` | 本节点准入码（未入集群时有效） |
| GET | `/api/cluster/discovered` | mDNS 看到但未配对的节点 |
| POST | `/api/cluster/invite` | `{uuid, code}` 邀请已发现节点 |
| PUT | `/api/cluster/nodes/{uuid}` | `{name?, accept_work?, weight?, static_address?}` |
| DELETE | `/api/cluster/nodes/{uuid}` | 移除成员 |
| GET | `/api/cluster/models` | 模型 × 节点矩阵 |
| POST | `/api/cluster/models/{model}/sync` | 第二期：`{nodes: [...]|"all"}`，在目标节点创建从内网 CSGHub 拉取的 pull job（7.8） |
| GET | `/api/cluster/explain` | 调度解释：`?model=&prompt_tokens=&max_tokens=`（7.7） |
| GET | `/api/cluster/recommendations` | 副本放置建议（7.7） |
| POST | `/api/cluster/nodes/{uuid}/state` | `{state: active|drain|maintenance}` |
| PUT | `/api/cluster/settings` | `{prefer_local?, accept_work?, routing_mode?, affinity_max_queue?, replication?, disk_reserve_gb?}` |

推理接口不新增路径：`/v1/chat/completions`、`/v1/embeddings`、`/v1/messages`、
`/v1/responses`、`/api/chat` 的 `source` 接受 `cluster` 与 `node:<uuid>`；
provider 路由族新增 `/providers/cluster/v1/*` 与 `/providers/node:<uuid>/v1/*`
（复用 `registerProviderInferenceRoutes`）。图像、语音第一期不参与集群路由，
对 `cluster` 来源返回与 pool 相同的 `providerPoolRouteUnsupported`。

以上全部同步到 `openapi/local-api.json`，由 `openapi_sync_test.go` 兜底。

### 8.2 节点间接口（11438，`/cluster/v1/*`，mTLS）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/cluster/v1/join` | 加入握手（唯一无需钉住证书的路径） |
| GET | `/cluster/v1/status` | 7.1 节的状态 |
| POST | `/cluster/v1/members` | gossip：交换成员表与观测地址 |
| POST | `/cluster/v1/leave` | 成员宣告离开 |
| POST | `/cluster/v1/inference/v1/chat/completions` 等 | 转发的推理请求，只在本机执行 |
| POST | `/cluster/v1/models/pull` | 第二期：创建 `source=cluster` 的 pull job |
| GET | `/cluster/v1/files/{sha256}?offset=` | 预留，未来级联复制用（7.8），本期不实现 |

### 8.3 与现有 API 的兼容性

原则：**不新增推理路径，不改变任何现有请求 / 响应字段的含义，所有新增都是
可选入参或追加字段**。指向任一节点的现有客户端（Chat 页、AI Apps、
`csghub-lite chat`、第三方 OpenAI / Anthropic SDK）不改配置即可继续工作。

**路由默认值**：集群设置 `routing_mode` 有两档。

| 模式 | 行为 | 适用 |
|---|---|---|
| `local_first`（默认） | 请求不带 `source` 时：本机有该模型就本机执行，与今天完全一致；本机没有才落到集群其它节点（插在现有兜底链"第三方 provider → 云端 → pool"之前）。 | 兼容优先，升级后零感知 |
| `balanced` | 不带 `source` 也交给调度器，在所有持有该模型的节点间按 7.2 排序（本机只是候选之一，`prefer_local` 决定同分时是否优先本机）。 | 想充分利用多台盒子的并发 |

任何模式下，`source=local` 强制本机、`source=cluster` 强制调度、
`source=node:<uuid>` 钉住节点。这与 `docs/agent-guidelines/model-source-routing.md`
"`source` 是推理契约的一部分"一致。

**逐接口约定**：

| 接口 | 兼容性处理 |
|---|---|
| `/v1/chat/completions`、`/v1/embeddings`、`/v1/messages`、`/v1/responses`、`/api/chat`、`/api/generate`、`/anthropic/*` | 路径、请求体、流式格式不变。`source` 新增两个取值。响应只追加 `X-CSGLite-Node`、`X-CSGLite-Node-Name` 头。错误信封与状态码沿用现有 `inference.HTTPStatusError` 写法，仅新增 `code: "cluster_no_available_node"`。 |
| `options`（`num_ctx`、`num_parallel`、`n_gpu_layers`、`keep_alive`、`cache_type_*`） | 原样转发到执行节点；执行节点按它自己的"请求 > 该节点的模型配置 > 该节点默认值"解析。各节点的 `ModelRuntimeSettings` 第一期不同步，UI 在模型矩阵里标出配置不一致的节点。 |
| 工具调用流式（`SupportsNativeToolStreaming`） | 执行节点在 `status.models[]` 中上报每个已加载模型的 `native_tool_streaming`，入口据此决定原样透传还是聚合后规范化，与本机路径逻辑相同。 |
| `/v1/models` | 返回集群内所有节点模型的并集（去重）。每项**追加** `nodes: [{uuid, name, loaded}]`，其余字段不变；旧客户端忽略未知字段。`?scope=local` 返回旧结果。 |
| `/api/ps`、`/api/load`、`/api/stop`、`/api/models*`、`/api/pull*` | 保持只作用于本机。集群范围的加载状态通过 `/api/cluster/models` 获取；对某个节点操作用 `/api/cluster/nodes/{uuid}/...`（第二期）。 |
| `/providers/{id}/v1/*` 路由族 | `id` 新增 `cluster` 与 `node:<uuid>`，其余 provider 路由不变，`openapi_sync_test` 中的路由族测试同步扩展。 |
| provider pool 配置 `provider_pools.json` | 结构不变；`members[].source` 新增合法值 `cluster`、`node:<uuid>`。旧版本二进制读到这些成员时按现有 `normalizeProviderPools` 逻辑当作未知 source 跳过，不会崩溃。 |
| 桌面 11436 推理监听器 | 同一 `getChatEngine`，自动获得集群路由；不新增任何管理路由，符合 `cross-platform.md` 的约束。 |
| CLI `csghub-lite chat` / `run`（`remoteEngine`） | 不变，仍连本机 11435，由本机决定是否转发。 |

**鉴权与账务**：

- API Key 在**入口节点**校验，沿用今天的 `api_keys.json` 与回环放行规则；
  转发到执行节点的请求不带客户 Key，走 mTLS 身份。因此各节点的 API Key
  第一期**不同步**，客户端要用它所指向节点上配置的 Key。跨节点同步 Key 与
  provider 白名单列为第三期可选项。
- 用量统计（`api_usage.db`）与可观测性 trace 记在入口节点，与今天一致；
  执行节点用同一 `X-CSGLite-Trace-ID` 记录本地 span，可观测性页面按 trace
  聚合两端。
- 对话历史（`chathistory`）只在入口节点，Responses API 目前无状态，都不受
  转发影响。

**版本兼容**：mDNS TXT 与握手带协议版本 `v=1`；协议主版本不同的节点互相
可见但拒绝加入 / 转发，`status` 带 csglite 版本，UI 对版本不一致的成员给出
提示。升级集群时逐台滚动升级即可，同一主版本内新旧节点可混跑。

**升级与回退**：升级到带集群功能的版本后不做任何操作即为单机模式；
`~/.csghub-lite/cluster/` 只在创建 / 加入集群时生成。回退到旧版本时该目录
被忽略，无需清理。

### 8.4 CLI

```
csghub-lite cluster status                      # 本节点与成员状态
csghub-lite cluster create --name <名称>
csghub-lite cluster join <token> [--address host:port]
csghub-lite cluster leave
csghub-lite cluster token [--rotate]
csghub-lite cluster code                        # 未入集群时显示准入码
csghub-lite cluster nodes                       # 成员表
csghub-lite cluster remove <uuid|name>
csghub-lite cluster models                      # 模型 × 节点矩阵
csghub-lite cluster sync <model> [--nodes a,b|--all]  # 让节点从内网源拉取模型
csghub-lite cluster drain <node> | activate <node>    # 运维状态
csghub-lite cluster explain <model>             # 调度解释
```

全部通过本机 11435 的 `/api/cluster/*` 实现，与 `license` 子命令同一模式。

## 9. Web UI

- **Dashboard（不分 CE / EE）**：现有 Dashboard 只显示本机的 CPU / 内存 /
  GPU。加入集群后，在其下新增"集群节点"区块：每台机器一张卡，显示主机名、
  在线 / drain / 离线状态、GPU 型号、显存用量条、CPU / 内存、已加载模型数、
  在途请求数、当前地址；顶部一行显示总节点数、总显存 / 已用、集群在途请求。
  CE 显示"节点 2 / 2，导入 EE License 可添加更多"，EE 显示"节点 n / 上限"。
  数据来自新增的 `GET /api/cluster/summary`（轻量，不含成员表细节），未入
  集群时该区块不出现，Dashboard 与今天一致。
- 新增导航项 `cluster`（"算力集群"）作为管理页，`FeatureDefinition.NavItem = "cluster"`。
  CE 与 EE 都可进入，不加锁标；页面顶部显示"节点 n / 上限"，CE 加入第 3 台
  时把 `403 feature_not_licensed` 渲染为"导入 EE License 解锁"引导，跳转
  Settings 的 License 分区。
- 页面分区：
  1. **概览**：集群名、成员数、总显存 / 已用、进行中请求数；每个节点一张卡
     （名称、在线 / drain / maintenance 状态、GPU 与温度、显存条、CPU / 内存、
     磁盘余量、已加载模型、并发、实测吞吐、版本、License 状态、当前地址）。
     离线节点灰显并显示"最后在线时间"。
  2. **发现的节点**：未配对节点列表，"邀请加入" 输入准入码。
  3. **模型分布**：模型 × 节点矩阵，格子为 present / warm / none / syncing
     （带进度）；"同步到节点"操作；右侧显示放置建议与"为什么会选这台"。
     磁盘余量不足的节点标红。
  4. **设置**：加入令牌查看 / 轮换、本节点接收任务开关、路由模式
     （`local_first` / `balanced`）、优先本机开关、亲和排队阈值、成员静态地址、
     离开集群。
- 未入集群时页面展示"创建集群"与"用令牌加入"两个入口，并显示本节点准入码。
- Chat 页模型选择器：集群启用时模型来源新增"集群"，模型项显示副本数徽标。
- 可观测性页面：请求列表新增"执行节点"列（来自 `X-CSGLite-Node-Name`）。
- 全部文案按 `docs/agent-guidelines/frontend-i18n.md` 同时补 `en` / `zh`，
  键前缀 `cluster.`。

## 10. 第三期可选：单模型跨节点（llama.cpp RPC）

当客户要跑的模型放不进任何一台盒子时，llama.cpp 的 RPC 后端可以把权重按显存
比例切到多机（`llama-server --rpc a:50052,b:50052`）。评估结论：

- 上游明确标注 PoC、无鉴权、"never run on an open network"；以太网下吞吐显著
  低于单机，价值只在"放得下"而非"更快"。
- 若做：`ggml-rpc-server` 随 `llama-cpp-assets` 分发；仅监听在 mTLS 隧道内
  （由集群传输层做端口转发），不直接暴露；在模型分布页提供"跨节点加载"入口，
  加载时选择参与节点与切分比例；调度器把该模型视为只存在于发起节点。
- 需要独立的性能验收（同网段千兆 / 万兆各一组数据）后再决定是否进入产品。

## 11. EE 门控：CE 可组 2 台，EE 不限

**已定**：社区版允许最多 2 个节点组成集群（自己加一台），第 3 台起需要 EE
License。这既让 CE 用户体验到集群价值，又把规模作为 EE 的卖点。

按 `ee-features.md` 的规则"同一功能不做 CE / EE 双实现，用 `quota.lite.*`
配额而不是代码分支"，门控只落在**节点数配额**上，功能开关不门控：

```go
// internal/license/features.go
FeatureLANCluster = FeatureDefinition{
    Key: featurePrefix + "lan_cluster", Type: FeatureTypeBoolean, Gated: false,
    DefaultValue: true, NavItem: "cluster", Since: "<发布版本>",
}
QuotaMaxClusterNodes = FeatureDefinition{
    Key: quotaPrefix + "max_cluster_nodes", Type: FeatureTypeInt, Gated: true,
    DefaultValue: 0 /* 有效 EE License 且 Extra 未提及时不限 */, CommunityValue: 2,
}
```

**行为**：

- 集群页、`/api/cluster/*`、`source=cluster` 在 CE 下全部可用，不返回 403。
- 加入 / 邀请时校验 `s.license.Limit(QuotaMaxClusterNodes)`：接收方成员表
  已满则拒绝，返回 `403 feature_not_licensed`，在现有结构上追加 `limit: 2`
  与 `current: 2`；UI 据此显示"社区版最多 2 个节点，导入 EE License 解锁"。
- 配额在**接收加入的那一侧**校验，且每个成员在 gossip 中带 `licensed` 与
  `node_limit`。若某成员的 License 过期导致其配额回落到 2 而集群已有 5 台：
  该成员不再接收转发、也不作为调度候选（`status.licensed=false`），但保留
  成员身份，不拆散集群；重新导入 License 后自动恢复。这样 License 过期只
  降级，不造成生产事故。
- 签发端可按 `max_cluster_nodes` 分档定价（例如 5 / 20 / 不限）。
- License 作用于实例，**每台盒子都要导入同一份 EE License**（License 不绑定
  机器）。交付时预装，或用 `csghub-lite license install` 批量导入。
- 这将是仓库里**第一个** `Gated: true` 的条目，需同步更新
  `TestUngatedFeaturesIgnoreTheLicense`；`CommunityValue` 非零满足
  `ValidateCatalog` 的要求。

**代码位置（已定）**：集群实现放在 `ee/cluster`，这是 EE 功能；社区版只是
被允许在 2 个节点的配额内使用它。这与 `ee/LICENSE` 现文本"生产使用必须持有
有效 EE License"有一处不一致，需要法务在 `ee/LICENSE` 里补一句，例如：

> 对于 Software 中受配额限制的功能，在不超过社区版配额（以 Software 内置的
> `CommunityValue` 为准）的范围内使用，不需要 Enterprise Edition license。

这样许可证条款与技术门控一致：2 台以内 CE 合法使用，第 3 台起既被代码拒绝、
也被条款约束。该改动与阶段一并行推进，不阻塞开发。

**Dashboard 不分版本**：主机与节点信息（主机名、GPU、显存、在线状态）是基础
可观测信息，在 Dashboard 对 CE 与 EE 同样展示，见第 9 节；区别只在节点数上限。

PR 清单：配额条目 `Gated: true`、加入 / 邀请入口的配额校验、
`openapi/local-api.json`、前端配额提示与解锁引导、发布说明 `[EE]` 前缀
（针对"超过 2 节点"这一部分）。

## 12. 跨平台与部署注意

- **Windows**：无系统 mDNS 应答器，`pion/mdns` 自带应答器；首次监听 UDP 5353
  与 TCP 11438 会触发防火墙提示，安装器需预置放行规则。
- **macOS**：桌面客户端访问局域网触发"本地网络"权限提示，需在 `csglite-client`
  的 Info.plist 中声明用途；与 `csglite-client` 协调（见 `cross-platform.md`）。
- **Linux 盒子**：与 `avahi-daemon` 共存需 `SO_REUSEADDR` / `SO_REUSEPORT`
  绑定 5353；systemd 服务需允许多播。
- **Docker**：mDNS 需要 `--network host`；否则用 `CSGHUB_LITE_CLUSTER_SEEDS`
  指定任一成员地址。`docs/guides/docker.md` 需补充。
- **多网卡**：优先在默认路由所在接口广播；`status` 中上报所有非回环地址，
  同伴按"实际连通的地址"记录（PAIR 的 observed-addresses 做法）。
- **时钟**：证书有效期 10 年、不校验 NotBefore 以外的时间；令牌握手用 nonce
  防重放，不依赖时钟同步。

## 13. 测试与验收

- `ee/cluster/identity`：首次生成、重复加载、损坏文件恢复；证书 SAN 只含 UUID。
- `ee/cluster/discovery`：内存实现下的目录合并；**同一 UUID 换 IP 只更新地址
  不产生新条目**；防抖 12 轮；全空扫描不惩罚；本机地址变化触发重播。
- `ee/cluster/membership`：令牌 / 准入码握手成功、错误令牌、重放 nonce、
  已入集群节点拒绝二次加入、移除后指纹失效、gossip 收敛（3 节点、断开 1 个）。
- `ee/cluster/telemetry`：`nvidia-smi` / `rocm-smi` 输出解析、磁盘与
  网络采集的平台桩、字段缺失取中性值、`perf` EMA 持久化。
- 模型同步：远程 pull job 创建与进度透传、模型源不一致拒绝、源不可达提示、
  公网源流量提示。
- `ee/cluster/scheduler`：表驱动排序用例（cold 的快卡胜过 warm 的慢卡、
  热降频惩罚、磁盘繁忙惩罚、drain 排除、`explain` 输出稳定、
  亲和命中、`node:` 钉住、`prefer_local`）。
- `internal/server`：3 个进程内 `Server` 实例 + 内存发现，模拟引擎；覆盖
  `source=cluster` 路由到有模型的节点、本机无模型自动兜底、执行节点不再转发、
  执行节点掉线后切换、`X-CSGLite-Node` 头、无 License 时 `403` 形状；
  `openapi_sync_test` 通过；`/v1/models` 合并展示。
- 前端：集群页锁状态、加入 / 创建流程、矩阵渲染的单测。
- **现场验收清单**（交付时随文档给出）：
  1. 三台盒子导入同一 License，一台 `cluster create`，两台用令牌加入，
     `cluster status` 三台 `online`。
  2. 对任一台的 `/v1/chat/completions` 连续发 20 条并发请求（模型在三台都有），
     `X-CSGLite-Node` 至少覆盖两台。
  3. 重启其中一台并确认其 IP 变化；期间请求继续成功；60 秒内 `cluster status`
     再次显示三台 `online`，且 UUID 不变。
  4. 请求一个只在 B 上的模型，从 A 的入口发起，`X-CSGLite-Node` 为 B。
  5. 删除 License 后，2 台成员照常互相转发；第 3 台仍在线但被标记
     `licensed=false`、不再接收转发；`cluster join` 第 4 台返回
     `403 feature_not_licensed` 且带 `limit: 2`。重新导入 License 后全部恢复。
  6. 用同一个 `X-CSGLite-Thread-ID` 连续对话 10 轮，`X-CSGLite-Node` 全程
     一致，第 2 轮起首字延迟明显低于第 1 轮；停掉该节点后下一轮自动落到
     其它节点并保持一致。
  7. 现有客户端（Chat 页、AI Apps、第三方 OpenAI SDK）不改任何配置，
     指向任一节点，行为与单机一致；`/v1/models` 返回项多出 `nodes` 字段
     但旧客户端忽略后正常工作。
  8. 三台盒子都指向内网 CSGHub，只有 A 有模型；执行"同步到全部节点"，B、C
     各出现一个 pull job 并从内网源拉取，公网出口无新增流量；把 C 的模型源
     改成别的地址后，`sync` 对 C 拒绝并在 UI 标黄。
  9. 把 B 的磁盘余量压到阈值以下，放置建议不再推荐 B；对 B 执行 `drain` 后
     新请求不再落到 B，在途请求正常完成。
  10. 不导入 License 的 2 台盒子组成集群后，两台的 Dashboard 都显示对方的主机
      卡片与"节点 2 / 2"；导入 License 后上限随 License 变化。
## 14. 分期计划

> 实施进度（2026-09-18，分支 `feat/ee-lan-cluster`）：阶段一全部完成
> （`ee/cluster`：身份与证书、mDNS + 内存发现、令牌 / 准入码握手、成员表与
> gossip、11438 mTLS 监听器、状态遥测、预计完成时间调度器与 `explain`、
> 节点转发引擎与失败切换、会话亲和、`/api/cluster/*`、`/cluster/v1/*`、
> `csghub-lite cluster` CLI、节点数配额、OpenAPI）；阶段二的 Dashboard 集群区块、
> 集群管理页与 i18n 已实现；阶段三中的"同步到节点"（远程 pull）、模型源一致性
> 检查、副本放置建议、`drain` / `maintenance`、共享密钥自动组网（6.1 方式 0，含
> 并行建群合并、安装脚本写入配置）、文档已实现；provider pool 成员的
> `source` 可以填 `cluster` 或 `node:<uuid>`（成员引擎走同一个 `getChatEngine`）。
> 未做：UDP 广播兜底、LRU 副本回收、Chat 页来源徽标、可观测性节点列、安装器
> 防火墙规则。

| 阶段 | 内容 | 估时 |
|---|---|---|
| 一 | 身份与证书；mDNS + 静态发现与目录（含 IP 变化、防抖）；令牌 / 准入码握手；成员表与 gossip；11438 mTLS 监听器；`status` 遥测（含 GPU 温度 / 功耗、CPU、内存、磁盘、网络、实测吞吐）；预计完成时间调度器与 `explain`；`nodeEngine`；`getChatEngine` 挂载；`/api/cluster/*` 与 `/cluster/v1/*`；CLI；节点数配额校验与 OpenAPI；`ee/` 文件头与 `scripts/check-ee.sh` | 8–10 天 |
| 二 | Dashboard 集群节点区块与 `/api/cluster/summary`；Web UI 集群页（概览、发现、矩阵、设置）、配额提示与解锁引导、i18n；Chat 页来源与徽标；可观测性节点列；`/v1/models` 合并 | 4–5 天 |
| 三 | "同步到节点"（远程 pull）、模型源一致性检查、副本放置建议与 LRU 副本回收、`drain` / `maintenance`；pool 成员支持 `cluster` / `node:`；UDP 广播兜底；Docker 与安装器防火墙规则；文档（本设计、`docs/cli/cluster.md`、docker、环境变量） | 4–5 天 |
| 四（可选） | 无内网源客户的级联复制；llama.cpp RPC 跨节点加载的性能验证与产品化决策；图像 / 语音路由；EAP-NOOB 替换握手层 | 另行评估 |

一至三合计约四周半，单人；阶段一后端与阶段二前端可并行。阶段一结束即可在真实
盒子上做第 13 节的现场验收 1–5 项。

## 15. 未决问题

已定：代码放 `ee/cluster`；CE 最多 2 个节点、EE 由 `max_cluster_nodes` 配额
分档；Dashboard 的主机 / 节点展示不分版本；默认路由模式 `local_first`
（第 8.3 节）；模型分发以私有化 CSGHub 为内网源（第 7.8 节）。

1. **License 粒度**：每台盒子导入同一份 License（本设计）还是只要求入口节点
   持有 License？本设计选前者，因为配额是在接收加入的一侧校验，且执行节点
   需要自行判断能否接收转发。需产品确认定价口径。
2. **`ee/LICENSE` 补充条款**：需要法务确认第 11 节建议的"社区版配额内使用
   无需 License"措辞，与阶段一并行，不阻塞开发。
3. **导航位置**：独立导航项"算力集群"（本设计）还是并入 AI Gateway 页签。
4. **客户网络是否放行多播**：影响是否必须在第一期就做静态种子 / UDP 广播。
5. **盒子操作系统范围**：若全部为 Linux，可推迟 Windows 防火墙与 macOS 本地
   网络权限的工作。
6. **模型 ID 一致性**：不同盒子上同一模型若量化不同（`Q4_K_M` 与 `Q8_0`），
   按不同模型处理；是否需要"等价模型组"由产品决定。
7. **无内网源的客户**：是否会出现没有私有化 CSGHub 又对外网流量敏感的
   客户，决定第四阶段的级联复制是否要做。
8. **与 CSGHub 侧对接**：是否需要把集群状态上报到 CSGHub 管理后台（多集群
   视图）；不影响本期。
