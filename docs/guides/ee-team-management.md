# 用户、团队与角色（EE）

功能键：`feature.lite.team_management`（布尔，EE）；席位：`quota.lite.max_users`
（整数，EE；License 未写则不限）。实现位于 `ee/team`，HTTP 接入在
`internal/server/team.go`。

## 1. 目标

CSGLite 默认监听 `:11435` 的所有网卡。共享给团队使用时，局域网内任何人都能调用
管理接口。本功能为共享实例提供：

- 登录：远程访问必须登录或携带 API Key。
- 角色：管理员、成员、只读。
- 归属：API Key、对话、图片任务归属到用户，彼此不可见。
- 团队：把用户归组，供后续用量分摊（E3）和预算（E2）使用。

## 2. 开启方式

第一个用户创建后账号体系即生效，在此之前实例行为与单机版完全一致。

- 创建用户需要 License。第一个用户总是管理员，无论请求里写的什么角色。
- 账号开启后，**本机请求**（loopback 且不带 `X-Forwarded-For`、`X-Real-IP`、
  `Forwarded`、`X-Forwarded-Host`）仍视为管理员，CLI、桌面端、本机启动的 AI 应用
  不受影响。经本机反向代理转发的请求带有转发头，按远程处理。
- 最后一个启用状态的管理员不能被删除、停用或降级。关闭账号体系需要在主机上删除
  `team.json`。

## 3. 身份识别

`accountsMiddleware` 位于 `apiAuthMiddleware` 之前，依次识别：

1. 本机操作者 → 管理员。
2. `csglite_session` Cookie（HttpOnly、SameSite=Lax，7 天，不滑动续期）。
3. API Key（`Authorization: Bearer` 或 `x-api-key`）：
   - 有归属的 Key 使用归属用户的角色；归属用户被停用或删除时 Key 失效。
   - 开启账号前创建的 Key 没有归属，按成员处理：可以继续调用模型，不能管理实例。

无法识别时返回 `401 {code: "login_required"}`，前端据此跳到登录页。
公开路径：Web UI 静态资源、`/api/health`、`/api/auth/*`。

用 Cookie 发起的写请求若带有不同源的 `Origin`，返回 403，以防 CSRF。
同一用户名在同一地址连续 5 次密码错误后锁定 1 分钟。

## 4. 角色

| 能力 | 管理员 | 成员 | 只读 |
|---|---|---|---|
| 读取模型、设置、仪表盘等 GET 接口 | ✓ | ✓ | ✓ |
| 调用模型（`/v1/*`、`/anthropic/*`、`/api/chat` 等） | ✓ | ✓ | 仅 `GET /v1/models` |
| 自己的对话、图片任务、API Key | ✓ | ✓ | — |
| 观测数据、日志、集群令牌、终端、用户列表 | ✓ | — | — |
| Provider、池、模型下载删除、设置、License、集群、应用安装等 | ✓ | — | — |

判定逻辑在 `ee/team/policy.go`：成员的写操作采用白名单，未列出的一律只有管理员
可以执行，新增路由默认就受保护。

非管理员读取 `GET /api/providers?source=config` 时，上游 API Key 和请求头的值
会被清空。

## 5. 数据

`~/.csghub-lite/team.json`（0600），内容包括：

- `users`：PBKDF2-SHA256 密码哈希，60 万次迭代。
- `teams`。
- `sessions`：只存令牌的 SHA-256。

文件单独存放，原因是它保存凭据且每次登录都会变化，不放进 `config.json`。

以下字段为新增的可选字段，旧数据可以照常读取：

- `api_keys.json` 的 `owner_id`。
- 对话文件和 `image-jobs/jobs.json` 的 `owner_id`。

没有 `owner_id` 的旧记录归本机操作者所有。

## 6. License 失效后

- 登录和权限检查不受 License 门控，已有账号继续保护实例，不会因为 License 到期
  而重新对局域网敞开。
- 用户和团队的增删改查（`/api/team/*`）返回 `403 feature_not_licensed`。
- 达到席位上限时返回 `403 feature_not_licensed`，并带上 `feature:
  quota.lite.max_users`、`limit` 和 `current`。

## 7. 后续

- E2（Key 治理）按用户和团队设置预算与模型白名单。
- E3（花费报表）按用户和团队统计用量；目前用量仍按 API Key 统计。
- E5 SSO 复用这里的会话。
