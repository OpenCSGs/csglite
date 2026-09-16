# 企业版（EE）License 功能门控设计

## 1. 背景与目标

CSGLite 目前是单一的开源版本：一个 Go 二进制内嵌前端，Apache-2.0 许可，
公开托管在 GitHub 并同步到 GitLab。随着面向团队和企业的能力增多（多 provider
池与语义路由、可观测性与审计导出、API Key 远程鉴权、企业 agent 集成等），需要
在**不拆仓库、不拆构建**的前提下区分社区版（CE）和企业版（EE）。

本设计采用业界最主流的 open core 做法，即 GitLab、Mattermost、PostHog、
Metabase 等项目使用的模式：

- **代码全部公开在同一个仓库，构建产物只有一个二进制。**
- **功能是否可用由 License 决定，而不是由编译时开关决定。**
- EE 代码使用单独的商业许可证约束生产使用；技术门控负责产品体验，
  法律条款负责商业保护。

**License 的签发不在 CSGLite 内实现，复用 CSGHub（starhub-server）已有的
License 体系**：CSGHub SaaS 管理后台签发，CSGLite 只做校验和门控。详见第 4 节
和第 13 节。

目标：

1. CE 用户拿到的二进制和 EE 用户完全一样，导入 License 文件即可解锁。
2. 决定“一个功能属于 CE 还是 EE”只需改一处注册表，其余由测试和 CI 兜底。
3. 发布、升级、Docker、Homebrew 等现有链路一律不变。
4. 离线环境可用；在线激活是可选增强。
5. License 文件格式、签名算法、字段语义与 CSGHub EE 完全一致，一套签发后台
   同时服务 CSGHub 和 CSGLite。

非目标：

- 不做代码混淆，不阻止有人删掉校验重新编译。这在 open core 模式下由许可证
  条款约束，所有同类项目均如此。
- 不做多租户或用户级权限；License 作用于整个 CSGLite 实例。
- 不在 CSGLite 内实现签发、私钥管理或 License 台账。

## 2. 许可证与目录约定

### 2.1 目录

| 路径 | 许可证 | 内容 |
|---|---|---|
| 仓库根目录及其它目录 | Apache-2.0（不变） | CE 代码，以及 EE 功能的**门控框架**（`internal/license`） |
| `ee/` | CSGLite Enterprise License（新增 `ee/LICENSE`） | 仅在持有 License 时才会启用的 EE 实现代码 |

第一阶段不强制把现有 EE 候选功能的实现代码搬进 `ee/`，只要求**新增**的 EE
功能实现放在 `ee/` 下。搬迁存量代码作为后续独立任务，避免一次性大改动带来的
合并冲突。

### 2.2 许可证文本要点

`ee/LICENSE` 需要覆盖以下条款，措辞可参考 GitLab EE License 和 PostHog
Enterprise License：

- 本目录代码不受根目录 Apache-2.0 覆盖。
- 允许出于评估、开发、测试目的免费使用和修改。
- 生产环境使用需持有 OpenCSG 签发的有效 License。
- 不得移除、禁用或绕过 License 校验逻辑。
- 允许社区提交修改（贡献者协议按现有流程）。

根目录 `LICENSE` 顶部追加一段说明：`ee/` 目录下的文件适用 `ee/LICENSE`。
`README.md` 的许可证章节同步说明。

`ee/` 下每个源文件文件头统一加注释：

```go
// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise License.
// See ee/LICENSE for details.
```

## 3. 功能注册表：唯一真源

### 3.1 与 CSGHub 共用的命名规则

CSGHub 在 `common/types/feature_registry.go` 中定义了功能注册表
`FeatureDefinition{Key, Type, DefaultValue}`，并规定：

- 布尔功能开关的 Key 必须以 `feature.` 开头，写在 License `Extra.features` 里；
- 整数配额的 Key 必须以 `quota.` 开头，写在 License `Extra.limits` 里；
- 签发端对未注册的 Key 严格拒签，**但 `lite.` 产品段例外**：
  `feature.lite.*`、`quota.lite.*` 只按前缀做类型校验（布尔 / 整数），不要求
  事先登记。CSGLite 自己的注册表是这段命名空间的唯一真源，新增或改名 Key 只
  改 csglite 一个仓库；在 CSGHub 注册表里登记只是让签发后台能展示中英文名。

为避免与 CSGHub 自身功能（如 `feature.audit_log`）在同一平面命名空间冲突，
CSGLite 的 Key 统一加产品段：`feature.lite.<name>`、`quota.lite.<name>`。

### 3.2 CSGLite 侧注册表

新增包 `internal/license`，其中 `features.go` 是 CSGLite 所有功能划分的唯一
真源。后端门控、前端导航、发布说明全部引用这里的常量，禁止在其它地方出现
裸字符串。结构与 CSGHub 的 `FeatureDefinition` 保持一致，便于两边对照。

```go
package license

type FeatureType string

const (
    FeatureTypeBoolean FeatureType = "boolean"
    FeatureTypeInt     FeatureType = "int"
)

type FeatureDefinition struct {
    Key          string      // 与 CSGHub 注册表中的 Key 完全相同
    Type         FeatureType
    Gated        bool        // true 才校验 License；false 的功能在任何版本下始终开启
    DefaultValue any         // Gated 功能持有有效 License 但 Extra 未提及该 Key 时的取值
    NavItem      string      // 对应前端导航项 id，可为空
    Since        string      // 首次进入 EE 的 CSGLite 版本
}

var (
    FeatureProviderPools   = FeatureDefinition{Key: "feature.lite.provider_pools",   Type: FeatureTypeBoolean, DefaultValue: true, NavItem: "ai-gateway",    Since: "0.10.0"}
    FeatureObservability   = FeatureDefinition{Key: "feature.lite.observability",    Type: FeatureTypeBoolean, DefaultValue: true, NavItem: "observability", Since: "0.10.0"}
    FeatureRemoteAPIKeys   = FeatureDefinition{Key: "feature.lite.remote_api_keys",  Type: FeatureTypeBoolean, DefaultValue: true, NavItem: "",              Since: "0.10.0"}
    FeatureAIApps          = FeatureDefinition{Key: "feature.lite.ai_apps",          Type: FeatureTypeBoolean, DefaultValue: true, NavItem: "ai-apps",       Since: "0.10.0"}
    FeatureRealtimeVoice   = FeatureDefinition{Key: "feature.lite.realtime_voice",   Type: FeatureTypeBoolean, DefaultValue: true, NavItem: "",              Since: "0.10.0"}
    FeatureImageGeneration = FeatureDefinition{Key: "feature.lite.image_generation", Type: FeatureTypeBoolean, DefaultValue: true, NavItem: "images",        Since: "0.10.0"}

    QuotaProviderPools     = FeatureDefinition{Key: "quota.lite.max_provider_pools", Type: FeatureTypeInt, DefaultValue: 0 /* 0 表示不限 */}
)

var Catalog = []FeatureDefinition{ /* 以上全部 */ }
```

**只有 `Gated: true` 的功能才受 License 控制；未标记的功能在社区版、开发
构建和任何 License 状态下都始终开启。** 把一个功能收进 EE 的动作就是把它的
`Gated` 改为 `true` 并包裹对应路由；在此之前注册表条目只是与签发端对齐名字，
不影响任何行为。当前仓库里没有任何条目是 `Gated: true`。

对 `Gated` 功能，取值语义与 CSGHub 的 `licenseProvider` 一致：

| 情形 | 布尔功能 | 整数配额 |
|---|---|---|
| 未标记 `Gated` | `true`（始终开启） | `0`（不限） |
| 无有效 License | `false` | `0` |
| 有效 License，Extra 未提及 | `DefaultValue` | `DefaultValue` |
| 有效 License，Extra 明确给值 | Extra 中的值 | Extra 中的值 |

因此 `Edition = "Enterprise"` 的 License 即使 `Extra` 为空也解锁全部 EE 功能
（默认值为 `true`），需要按客户裁剪时在 `Extra.features` 里显式写 `false`。

以上初始划分是**建议**，正式上线前需产品确认。表中未列出的功能一律属于 CE：
模型市场与下载、本地推理、Chat、OpenAI / Anthropic 兼容 API、数据集下载、
基础设置、升级。

### 3.3 决策规则

一个新功能是否进入 EE，按以下三条判断，命中两条即走 EE：

1. 使用者是团队或组织，而不是单个开发者本机使用。
2. 只有在多人共享实例或对外提供服务时才有价值。
3. 会带来持续的支持、运维或第三方服务成本。

补充约束：

- **已在 CE 发布过的功能不回收进 EE。** 只能新增功能进 EE。
- 同一功能不做“CE 阉割版 + EE 完整版”的双实现，避免维护两套代码；
  如确有需要（例如 CE 限制数量），用 `quota.lite.*` 配额而不是分支实现。
- 涉及 OpenCSG 服务端配合的能力（云端模型、账号、在线激活）天然由服务端
  控制，不需要额外走本地 License 门控。

### 3.4 PR 流程

- PR 模板增加必选项：`功能归属：CE / EE / 不涉及`。
- 选择 EE 的 PR 必须同时包含：`Catalog` 条目标记 `Gated: true`、路由门控、前端门控、
  `openapi/local-api.json` 更新、发布说明 `[EE]` 前缀。任一缺失 CI 失败。
  不需要改 starhub-server；如希望签发后台显示该功能的中英文名，可另提 MR
  登记（见第 13 节）。
- `docs/agent-guidelines/` 新增 `ee-features.md`，把本节规则写成 agent 可
  执行的检查清单。

## 4. License 文件格式：与 CSGHub 完全一致

### 4.1 载荷结构

CSGLite 不定义自己的载荷，直接采用 CSGHub `common/types/license.go` 的
`RSAPayload`。在 `internal/license/csghub_format.go` 中**逐字段复制**以下
结构（不引入 `opencsg.com/csghub-server` 模块依赖，字段名与类型必须一致，
因为 gob 按字段名匹配）：

```go
type RSAPayload struct {
    Key string // License 唯一 ID，签发端生成的 UUID
    DataBody
}

type DataBody struct {
    Company    string    // 客户名称
    Email      string    // 客户联系邮箱
    Product    string    // 产品，CSGLite 固定为 "CSGLite"
    Edition    string    // 套餐，首期固定为 "Enterprise"
    MaxUser    int       // 席位数，签发端要求 >= 1；CSGLite 首期只展示不强制
    StartTime  time.Time // 生效时间
    ExpireTime time.Time // 到期时间
    Extra      string    // JSON：{"features": {...}, "limits": {...}}
    Version    string    // 适用的最低 CSGLite 版本，空表示不限制
}

type RSAInfo struct {
    Payload   []byte // gob(RSAPayload)
    Signature []byte // RSA-SHA256 PKCS#1 v1.5 签名
}
```

字段映射与 CSGHub 的差异只有语义层面：

| 字段 | CSGHub 用法 | CSGLite 用法 |
|---|---|---|
| `Product` | `"CSGHub"` | `"CSGLite"`。校验时 `Product` 不匹配即视为无效，防止拿 CSGHub 的 License 解锁 CSGLite |
| `Edition` | `"Enterprise"` 等 | 首期只识别 `"Enterprise"`；其它值按 CE 处理并给出提示 |
| `MaxUser` | 与注册用户数比较 | 仅在状态接口和 UI 中展示 |
| `Extra` | `feature.*` / `quota.*` | 同规则，Key 带 `lite.` 段 |
| `Version` | 自由文本 | 语义化版本，非空时要求当前 CSGLite 版本不低于该值 |

首期设计中的“宽限期”和“机器绑定”不进入载荷（CSGHub 签发端不支持这两个
字段，且 `Extra` 严格只允许 `features` 与 `limits` 两个顶层键）：

- 宽限期作为 CSGLite 侧的固定策略常量（14 天），见第 5 节。
- 机器绑定暂不实现；若将来需要，在 starhub-server 的 `LicenseExtra` 中新增
  顶层字段并同步放宽校验，属于第 13 节的后续项。

### 4.2 编码与签名

与 `builder/rsa/keys.go` 的 `GenerateData` / `VerifyData` 逐步一致：

1. `gob.Encode(RSAPayload)` 得到 `payload`。
2. `SHA-256(payload)`，用 RSA 私钥做 PKCS#1 v1.5 签名得到 `signature`。
3. `gob.Encode(RSAInfo{payload, signature})`。
4. `base64.StdEncoding`，每 64 字符换行。
5. 首尾包裹 `-----BEGIN LICENSE KEY-----` / `-----END LICENSE KEY-----`。

CSGLite 只实现第 5 到第 1 步的逆向（解码与验签），使用 Go 标准库
`crypto/rsa`、`crypto/sha256`、`crypto/x509`、`encoding/gob`、`encoding/pem`，
不新增依赖。

**兼容性保证**：把 starhub-server `builder/rsa/keys_test.go` 中的
`TestPublicKey` 与 `EncodedResult` 作为黄金测试向量复制到
`internal/license/csghub_format_test.go`，要求 CSGLite 的解码结果与 CSGHub
测试中断言的字段完全相同。这条测试是两边格式不漂移的硬约束。

### 4.3 公钥

- CSGLite 把 CSGHub SaaS 签发端使用的那对密钥的**公钥** PEM（`PUBLIC KEY`
  PKIX 格式）作为常量编进 `internal/license/pubkey.go`。支持多把公钥，
  任一验签通过即有效，便于将来轮换。
- 环境变量 `CSGHUB_LITE_LICENSE_PUBLIC_KEY_FILE` 可覆盖为文件路径，用于
  预发环境和本地联调。
- 私钥不出现在本仓库。单测使用仓库内生成的**测试专用**密钥对，
  与生产密钥无关。

### 4.4 签发流程

签发完全复用 CSGHub SaaS（`saas && license_issuer` 构建变体）已有的管理接口，
CSGLite 不实现任何签发逻辑：

| 步骤 | 接口 | 说明 |
|---|---|---|
| 查看可签功能 | `GET /api/v1/licenses/management/features` | 返回注册表，含 i18n 名称与描述；CSGLite 功能登记后自动出现 |
| 签发 | `POST /api/v1/licenses/management` | `product: "CSGLite"`, `edition: "Enterprise"`, `extra` 按需裁剪；响应即 License 文件内容 |
| 重新下载 | `GET /api/v1/licenses/management/{id}?create_license_file=true` | 用台账记录重新生成同一份文件 |
| 修改 | `PUT /api/v1/licenses/management/{id}` | 修改后需重新下载文件分发给客户 |

管理员在 OpenCSG 后台操作，客户拿到文件后在 CSGLite 中导入。

## 5. 运行时校验

### 5.1 存放位置

License 文件位于存储根目录：`~/.csghub-lite/license.key`。遵守
`docs/agent-guidelines/storage.md`，不写入 `config.json`（License 是签名
文件而非用户设置，且按 `config-schema.md` 不应与其它设置混放）。

环境变量 `CSGHUB_LITE_LICENSE_FILE` 可覆盖路径，`CSGHUB_LITE_LICENSE` 可直接
提供内容，便于容器和 CI 场景。CSGLite 只保留一份 License，导入即覆盖，
不做 CSGHub 那样的多条台账与“最新生效”查询。

### 5.2 加载与状态

```go
type State struct {
    Status    Status            // none | valid | grace | expired | invalid | not_started
    Payload   *RSAPayload       // 解码后的载荷，可为空
    Features  map[string]bool   // 按 3.2 节语义解析后的布尔功能
    Limits    map[string]int    // 按 3.2 节语义解析后的配额
    CheckedAt time.Time
    Reason    string            // invalid 时的原因，仅日志和 CLI 使用
}
```

- 启动时加载一次；之后每小时重新读取文件，文件变化即时生效，无需重启。
- `PUT /api/license` 导入后立即重新加载。
- 有效期判定与 CSGHub `GetLatestActive` 一致：`StartTime <= now <= ExpireTime`。
- 状态含义：

| 状态 | 判定 | 行为 |
|---|---|---|
| `none` | 无文件 | 按 CE 运行 |
| `not_started` | 签名正确但 `now < StartTime` | 按 CE 运行，UI 提示生效日期 |
| `valid` | 签名正确、`Product` 匹配、在有效期内 | 按 3.2 节解析功能 |
| `grace` | `ExpireTime < now <= ExpireTime + 14 天` | 功能照常，UI 与日志显示到期提醒 |
| `expired` | 超过宽限期 | 回退到 CE，UI 明确提示 |
| `invalid` | 签名错误、格式错误、`Product` 不匹配、`Version` 不满足、`Extra` 解析失败 | 回退到 CE，UI 提示并给出原因 |

- **`Extra` 解析**采用 CSGHub `ValidateLicenseExtraForImport` 的宽松策略：
  未知 Key 记警告并忽略，已知 Key 类型错误才判 `invalid`。这样新签发端签出
  的 License 可以被旧版 CSGLite 导入。
- **时钟回拨**：在存储根记录最近一次成功校验的时间戳；当前时间早于该时间戳
  超过 24 小时视为异常，按 `grace` 处理并记日志，而不是直接判失效，避免误伤。
- **在线校验**（第二阶段可选）：复用 `internal/cloud` 到 OpenCSG 的通道，
  上报 `Key` 与 CSGLite 版本；服务端可依据台账返回吊销标记。网络不可达时
  保持本地判定结果，不降级。

## 6. 后端门控

### 6.1 中间件

```go
// internal/server/license.go
func (s *Server) requireFeature(def license.FeatureDefinition) func(http.HandlerFunc) http.HandlerFunc
```

未授权时返回 403，与 CSGHub `FeatureGate` 中间件对
`ErrLicenseFeatureDisabled` 的处理一致：

```http
HTTP/1.1 403 Forbidden
Content-Type: application/json

{
  "error": "This feature requires a CSGLite Enterprise license.",
  "errorCode": 403,
  "code": "feature_not_licensed",
  "feature": "feature.lite.observability",
  "edition_required": "Enterprise"
}
```

`error` 与 `errorCode` 沿用本地 API 现有的错误形状，`code` 是前端判断用的固定
错误码，与现有 401（API Key）和 409 区分。

### 6.2 应用位置

- 只在 `internal/server/routes.go` 中包裹路由，不修改各 handler 内部逻辑。
  例如：

  ```go
  mux.HandleFunc("GET /api/observability/requests",
      s.requireFeature(license.FeatureObservability)(s.handleObservabilityRequests))
  ```

- `externalAPIRoutes()` 中的对应路由使用同一中间件，保证桌面模式外部 API
  与本地 API 行为一致。
- **非 HTTP 入口必须单独检查**：后台任务（provider pool 路由评估、可观测性
  落库、数据集导出任务、realtime 会话建立）在任务入口调用
  `s.license.Enabled(def)`。这是最容易漏的地方，测试要覆盖。
- 配额类功能在业务处调用 `s.license.Limit(def)`，`0` 表示不限。
- 读取类接口是否放开需逐项决定：例如可观测性数据在 License 过期后仍允许
  只读查看和导出，避免客户数据被锁死。默认原则是**写入和新任务受限，
  既有数据可读可导出**。

### 6.3 状态注入

`Server` 新增字段 `license *license.Manager`。`Manager` 负责加载、定时刷新、
提供 `Enabled(FeatureDefinition) bool`、`Limit(FeatureDefinition) int`、
`State() State`。所有判断经由 `Manager`，禁止在 handler 里直接读文件。

## 7. 前端门控

### 7.1 接口字段

`GET /api/settings` 响应新增（`pkg/api/types.go` 的 `SettingsResponse`）：

```json
{
  "edition": "Enterprise",
  "license": {
    "status": "valid",
    "key": "e519fa5a-76fd-4506-ae99-876f41f6be49",
    "company": "某某科技有限公司",
    "email": "admin@example.com",
    "product": "CSGLite",
    "edition": "Enterprise",
    "max_user": 50,
    "start_time": "2026-10-01T00:00:00Z",
    "expire_time": "2027-10-01T00:00:00Z",
    "grace_until": null
  },
  "features": ["feature.lite.provider_pools", "feature.lite.observability"],
  "limits": {"quota.lite.max_provider_pools": 0},
  "feature_catalog": [
    {"key": "feature.lite.observability", "type": "boolean", "nav_item": "observability"}
  ]
}
```

`license` 子对象字段名与 CSGHub `LicenseStatusResp` 的 JSON 标签保持一致，
方便将来 OpenCSG 控制台或桌面壳复用同一套展示组件。`features` 为当前实际
可用的功能；`feature_catalog` 供前端知道哪些导航项需要加锁标记。现有
`hidden_nav_items` 保留，语义仍是“运营方隐藏”，与授权无关。

### 7.2 页面行为

- `Layout.tsx`：导航项按 `feature_catalog` 关联的功能判断。未授权的项**不隐藏**，
  而是显示锁图标并可点击，进入页面后显示该功能的说明和“导入 License”入口。
  这样 CE 用户知道 EE 有什么，符合 GitLab、PostHog 的做法。
- 新增 `useLicense()` 与 `hasFeature(key)`，从 `/api/settings` 派生，放在
  `web/src/license.ts`。
- `api/client.ts` 统一拦截 403 且 `error === "feature_not_licensed"` 的响应，
  抛出带 `feature` 字段的错误，页面据此渲染升级提示组件 `FeatureLocked`。
- Settings 页新增“License”分区：显示状态、客户、到期时间、席位；提供粘贴或
  上传 License、删除 License；`grace`、`expired`、`invalid` 状态用醒目颜色
  提示并显示原因。
- 所有文案走 i18n，遵守 `docs/agent-guidelines/frontend-i18n.md`。

## 8. 管理接口与 CLI

### 8.1 HTTP

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/license` | 返回当前 `State`（不含原始 License 文本） |
| `POST` | `/api/license/verify` | 请求体 `{"data": "<文件内容>"}`，只校验不保存，返回解码后的载荷；对应 CSGHub 的 `POST /licenses/verify` |
| `PUT` | `/api/license` | 请求体 `{"data": "<文件内容>"}`，校验通过后写入 `license.key` 并重新加载，失败返回 400 并说明原因；对应 CSGHub 的 `PUT /licenses/import` |
| `DELETE` | `/api/license` | 删除文件并回退到 CE |

请求体字段名 `data` 与 CSGHub `ImportLicenseReq` 一致。以上仅在本地 API
暴露，不进入 `externalAPIRoutes()`。同步更新 `openapi/local-api.json` 并运行
`go test ./internal/server`。

### 8.2 CLI

```
csghub-lite license show               # 显示状态、套餐、功能、到期时间
csghub-lite license verify <file>      # 只校验并打印载荷，不保存
csghub-lite license install <file>     # 导入 License 文件
csghub-lite license install -          # 从标准输入导入
csghub-lite license remove             # 删除 License
csghub-lite license features           # 列出 Catalog 及当前是否可用
```

`docs/cli/license.md` 与 `docs/cli/overview.md` 同步补充。

## 9. 桌面模式与 Docker

- 桌面模式（`DesktopMode`）下 License 状态通过 `/api/settings` 一并返回，
  桌面壳无需额外接口。桌面壳如需在托盘显示到期提醒，读取同一字段。
- Docker 镜像通过 `CSGHUB_LITE_LICENSE` 环境变量或挂载 `license.key` 注入，
  `docs/guides/docker.md` 补充示例。

## 10. 测试与 CI 约束

- `internal/license`：
  - CSGHub 黄金向量解码通过（见 4.2）；
  - 篡改载荷 / 篡改签名 / `Product` 不匹配 / `Version` 不满足 / 未生效 /
    过期 / 宽限 / 时钟回拨 / 多公钥轮换 / `Extra` 未知 Key 警告 /
    `Extra` 类型错误判无效，全部单测覆盖。
- `internal/server`：
  - 每个 `Catalog` 条目至少被一条路由或一个后台入口引用；
  - 每个 `requireFeature` 引用的定义都在 `Catalog` 中；
  - 无 License、有效 License、过期 License 三种状态下对代表性路由的
    200 / 403 断言。
- 前端：`Layout` 对锁标记的渲染、403 拦截逻辑的单测。
- CI 新增检查脚本 `scripts/check-ee.sh`：`ee/` 下所有源文件带许可证头；
  PR 标记为 EE 时 `Catalog`、`openapi/local-api.json`、发布说明同时有改动。
- `make lint` 与 `make test` 保持为唯一入口，不新增独立流水线。

## 11. 分期计划

> 实施进度：阶段一、二（`internal/license`、`/api/license*`、CLI、OpenAPI）和
> 阶段三的 License 管理部分（Settings 页导入/校验/删除、侧边栏 “CSGLite EE”
> 标识）已完成；导航锁标与 `FeatureLocked` 组件随第一条被门控的路由一起做。
> `Catalog` 已声明但没有任何条目 `Gated: true`，也未包裹路由，所有功能在所有版本
> 下开启；等待产品确认初始划分（第 12 节问题 1）后逐条标记。


| 阶段 | 内容 | 估时 |
|---|---|---|
| 一 | `internal/license`：CSGHub 格式解码验签、黄金向量测试、公钥、`Manager` | 1-2 天 |
| 二 | `Catalog`、`requireFeature`、routes 门控、后台入口检查、`/api/license*`、CLI、OpenAPI | 2 天 |
| 三 | 前端 `useLicense`、导航锁标、`FeatureLocked`、Settings License 分区、i18n | 2-3 天 |
| 四 | `ee/` 目录与许可证文本、根 LICENSE 与 README 说明、agent guideline、PR 模板、CI 检查 | 1 天 |
| 五 | starhub-server 侧登记 CSGLite 功能与 i18n（第 13 节） | 0.5 天 |
| 六 | 文档：本设计、`docs/cli/license.md`、Docker 与配置指南更新、发布说明 | 0.5 天 |

合计约 1.5 到 2 周，单人。阶段一至三可并行推进后端与前端；阶段五需要
starhub-server 仓库的 MR 与发版配合。在线校验与席位强制不在本期范围。

## 12. 未决问题

1. 初始 `Catalog` 划分需产品最终确认，尤其是图像生成和实时语音是否进入 EE。
2. `ee/LICENSE` 法律文本需法务审阅；是否允许非商业生产使用需明确。
3. 是否需要 `Edition = "Trial"`（例如 30 天全功能试用）以及试用 License 由谁签发。
4. 席位 `MaxUser` 是否以及如何强制：按 API Key 数、按并发会话数，还是仅展示。
5. License 过期后可观测性等历史数据的只读策略需逐功能确认。
6. 生产公钥的获取与轮换流程：由哪一方把 CSGHub 签发端公钥交付给 CSGLite 仓库，
   轮换时如何保证旧 License 继续有效。

## 13. 与 CSGHub（starhub-server）的对齐清单

CSGHub 已有完整的 License 体系，CSGLite 全部复用，两边只需要以下配合：

### 13.1 已完成的 starhub-server 改动（MR !3054）

| 改动 | 位置 | 说明 |
|---|---|---|
| `lite.` 命名空间放行 | `common/types/license_extra.go` | 签发时 `feature.lite.*` 按布尔、`quota.lite.*` 按整数做类型校验，不要求登记；前缀与所在小节不匹配仍报错 |
| 登记首批 CSGLite 功能 | `common/types/feature_registry.go` | 3.2 节的 7 个 Key，仅用于签发后台展示名称 |
| 功能名称与描述 | `common/i18n/{zh-CN,en-US,zh-HK}/features.json` | 键名 `license.<key>.name` / `license.<key>.description` |

以后 CSGLite 新增 Key 时 starhub-server **不需要改动**；登记与 i18n 是可选的
体验优化。

### 13.2 建议做的 starhub-server 改动

| 改动 | 说明 |
|---|---|
| `FeatureDefinition` 增加 `Product` 字段 | 让 `GET /licenses/management/features?product=CSGLite` 只返回该产品的功能，签发后台按产品分组展示，避免管理员把 CSGHub 功能签进 CSGLite License |
| 签发时按 `Product` 校验 `Extra` | `ValidateLicenseExtraForIssue` 拒绝与 `Product` 不匹配的 Key |
| 台账列表支持 `product` 过滤 | `QueryLicenseReq.Product` 已存在，只需后台 UI 透出 |

### 13.3 CSGLite 与 CSGHub 保持一致的约定

- 载荷结构、gob 编码、RSA-SHA256 PKCS#1 v1.5、base64 换行与 PEM 包裹全部一致，
  由黄金向量测试锁定。
- `Extra` 只有 `features` 与 `limits` 两个顶层键；导入宽松、签发严格，
  `lite.` 段只按前缀校验类型。
- 状态命名沿用 `active` / `inactive` / `expired` 的语义，CSGLite 额外增加
  `grace`、`not_started`、`invalid`、`none` 用于本地单文件场景。
- 导入与校验接口的请求体字段名为 `data`，与 `ImportLicenseReq` 一致。

### 13.4 不需要对齐的部分

- CSGHub 用数据库存多条 License 并查“最新生效”，CSGLite 是单文件，导入即覆盖。
- CSGHub 使用 OpenFeature SDK 与 Prometheus 指标；CSGLite 体量小，直接用
  `Manager` 提供布尔与整数查询，不引入 OpenFeature。
- CSGHub 的 `CheckLicense` 中间件在无 License 时整站拒绝；CSGLite 无 License
  时正常以 CE 运行，只锁 EE 功能。
