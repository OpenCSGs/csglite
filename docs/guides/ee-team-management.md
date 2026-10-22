# 用户、团队与角色（EE）

功能键：`feature.lite.team_management`（布尔，EE）；席位：`quota.lite.max_users`
（整数，EE；License 未写则不限，已邀请未激活的账号也占席位）。实现位于
`ee/team`，HTTP 接入在 `internal/server/team*.go`，CLI 在 `internal/cli/user.go`。

**社区版包含 1 个管理员账号**（`quota.lite.max_users` 的社区值为 1），任何实例都能
开启登录保护。邀请成员、审计员和成员角色、团队、修改或删除用户、登录安全设置需要
企业版 License。没有 License 时：

- `GET /api/team/users` 和 `POST /api/team/users` 不门控，第二个账号被席位上限拒绝
  （`403 feature_not_licensed`，`feature: quota.lite.max_users`）。
- 其余 `/api/team/*` 返回 `403 feature_not_licensed`。
- 环境变量和 CLI 同样可以创建这个管理员；管理员可在头像菜单修改自己的密码。

## 1. 目标与参考

CSGLite 默认监听 `:11435` 的所有网卡。共享给团队使用时，局域网内任何人都能调用
管理接口。本功能为共享实例提供登录、角色、邀请、个人归属和团队。设计参考
LiteLLM Proxy 和 OpenRouter Organizations：

| 方面 | LiteLLM | OpenRouter | CSGLite |
|---|---|---|---|
| 初始化 | `UI_USERNAME`/`UI_PASSWORD` 环境变量登录，仅用于初始化，之后关掉并提示 | 账号注册 | 环境变量**只创建一次真实账号**，之后忽略并在页面提示移除；或使用本机访问、初始化令牌、CLI |
| 免费版 | 开源版带管理员登录，多用户与 SSO 收费 | — | 社区版 1 个管理员账号，多用户、角色、团队收费 |
| 加入方式 | Invite User 生成链接；管理员设的密码首登必须修改 | 邀请加入组织 | 默认邀请链接（7 天、一次性）；直接创建时首登强制改密 |
| 角色 | proxy_admin / proxy_admin_viewer / internal_user | Admin / Member | 管理员 / 审计员 / 成员 |
| 应急 | break-glass 账号 | — | `csghub-lite user reset-password` 在主机上直接重置，不受 License 影响 |
| 菜单 | 侧边栏分区，管理员可配置页面可见性 | 管理员专区，成员看不到入口 | 「工作台 / 模型与数据 / 管理」分组，「管理」只对管理员和审计员显示 |

## 2. 初始化：创建第一个管理员

第一个用户创建后账号体系即生效，在此之前实例行为与单机版完全一致。第一个用户
总是管理员，并且自己选择密码（无需首登修改）。有四种方式：

1. **本机 Web 界面**：在服务器本机打开「团队」直接创建。
2. **远程 Web 界面 + 初始化令牌**：还没有账号时，服务在
   `~/.csghub-lite/team-setup-token`（0600）生成一次性令牌。远程创建第一个管理员
   必须提供它，防止局域网里的其他人抢先接管。令牌**不写入日志**，因为开启账号前
   `/api/logs` 对网络开放；日志里只打印文件路径。创建成功后令牌文件随即删除。
3. **环境变量**：设置 `CSGHUB_LITE_ADMIN_USERNAME`（默认 `admin`）以及
   `CSGHUB_LITE_ADMIN_PASSWORD` 或 `CSGHUB_LITE_ADMIN_PASSWORD_FILE`，启动时若还
   没有账号就创建，账号体系从第一秒生效。已有账号时忽略，不会重置密码；变量仍存在时
   「登录安全」页给出警告。
4. **CLI**：`csghub-lite user create admin --role admin`。

关闭账号体系需要在主机上删除 `team.json`。

## 3. 成员加入

- **邀请（默认）**：管理员填写用户名、角色、团队后生成链接 `/invite/<token>`。
  - 链接 7 天有效，只能使用一次，只显示一次（服务端只存 SHA-256）。
  - 对方打开链接设置密码后直接登录。
  - 重新生成链接后，旧链接立即失效；撤销即删除待激活账号。
- **直接创建**：管理员设置临时密码；对方首次登录后，除 `/api/auth/*` 外的所有请求
  返回 `403 password_change_required`，前端显示强制改密页。
- **管理员重置密码**：同样要求对方下次登录时修改，并让其在所有设备上退出。

## 4. 身份识别

`accountsMiddleware` 位于 `apiAuthMiddleware` 之前，依次识别：

1. **本机操作者** → 管理员。判定条件是 loopback 且不带 `X-Forwarded-For`、
   `X-Real-IP`、`Forwarded`、`X-Forwarded-Host`；经本机反向代理转发的请求按远程处理。
2. **`csglite_session` Cookie**：HttpOnly、SameSite=Lax，时长可配置，不滑动续期。
3. **API Key**：有归属的 Key 使用归属人的角色；归属人被停用或删除时 Key 失效。
   开启账号前创建的 Key 没有归属，按成员处理，且不能再签发新 Key。

其他规则：

- 无法识别身份时返回 `401 login_required`，前端据此跳到登录页。
- 公开路径：Web UI 静态资源、`/api/health`、`/api/auth/*`（包括邀请的查看和接受）。
- 用 Cookie 发起的写请求若带有不同源的 `Origin`，返回 403。
- 同一地址对同一用户名连续输错达到阈值后锁定，阈值和时长在「登录安全」中配置，
  默认 5 次、5 分钟。

## 5. 角色

| 能力 | 管理员 | 审计员 | 成员 |
|---|---|---|---|
| 读取模型、设置、仪表盘等 | ✓ | ✓ | ✓ |
| 观测数据、日志、成员与团队、所有 API Key | ✓ | ✓（只读） | — |
| 调用模型（`/v1/*`、`/anthropic/*`、`/api/chat` 等） | ✓ | 仅 `GET /v1/models` | ✓ |
| 自己的 API Key | ✓ | ✓ | ✓ |
| 自己的对话、图片任务 | ✓ | — | ✓ |
| 集群加入令牌、终端 | ✓ | — | — |
| 一切修改（Provider、池、模型、设置、License、成员…） | ✓ | — | — |

判定逻辑在 `ee/team/policy.go`：除管理员外，写操作一律采用白名单，未列出的路由
默认受保护。对话和图片在任何角色之间都按归属人隔离，管理员也看不到他人的对话。
非管理员读取 `GET /api/providers?source=config` 时，上游 API Key 和请求头的值会
被清空。旧版的 `viewer` 角色（未发布）在加载时迁移为成员。

## 6. 界面

- **侧边栏**：分为「工作台」（仪表盘、对话、应用）、「模型与数据」（市场、模型库、
  数据集）、「管理」（网关、集群、观测、团队）三组。「管理」组只对管理员和
  审计员显示。
- **头像菜单**：位于侧边栏底部，未开启账号时显示为「本机」。菜单显示姓名、邮箱、
  角色徽标，菜单项包括「我的 API Key」、「修改密码」、「设置」（仅管理员和审计员）、
  「帮助」、「语言」和「退出登录」。价格页从网关页 CSGHub Provider 旁的链接进入。成员的「我的 API Key」是 `/account/keys`
  页，管理员和审计员跳转到网关。
- **团队**：顶部显示成员数和席位、待接受邀请数、团队数，下设四个标签：
  - 成员：支持搜索，可按角色、团队、状态筛选；可邀请成员或直接创建账号。
  - 团队。
  - 邀请：列出待接受的邀请及过期时间，可重新生成链接或撤销。
  - 登录安全：会话时长、失败锁定阈值、访问规则说明、应急重置命令、初始化环境变量状态。

  审计员看到的是只读版本。没有 License 时：未开启账号则显示创建管理员的表单，
  已开启则显示「登录保护已开启」和账号列表；两种情况下方都有企业版功能介绍。
  License 失效且账号多于 1 个时显示失效说明。「团队」菜单项不带锁标。
- **独立页面**：登录页、邀请接受页（`/invite/<token>`，不需要登录）、强制改密页。

## 7. 数据

`~/.csghub-lite/team.json`（0600）包含以下内容：

- `users`：PBKDF2-SHA256 密码哈希，60 万次迭代。
- `teams`。
- `invites`：只存令牌的 SHA-256。
- `sessions`：只存令牌的 SHA-256。
- `settings`。

这个文件单独存放，原因是它保存凭据且每次登录都会变化，不放进 `config.json`。读取时
按修改时间和文件大小缓存，CLI 的改动会立即被服务看到。

以下字段为新增的可选字段，旧数据可以照常读取：`api_keys.json`、对话文件和
`image-jobs/jobs.json` 中的 `owner_id`。没有 `owner_id` 的旧记录归本机操作者所有。

## 8. License 失效后

- 登录、接受邀请和权限检查都不受 License 门控，已有账号继续保护实例。
- 「团队」页只读列出已有账号；除列出用户外的 `/api/team/*` 返回
  `403 feature_not_licensed`，也不能再添加账号（席位回到社区值 1）。
- `csghub-lite user reset-password` 仍然可用。
- 达到席位上限时返回 `403 feature_not_licensed`，并带上
  `feature: quota.lite.max_users`、`limit` 和 `current`。

## 9. 后续

- E2（Key 治理）：按用户和团队设置预算和模型白名单。
- E3（花费报表）：按用户和团队统计用量。
- E4（审计日志）：记录账号的增删改和登录事件。
- E5（SSO）：复用这里的会话和邀请。
- 团队管理员角色（类似 LiteLLM 的 `team_admin`）在有预算功能后再加。
