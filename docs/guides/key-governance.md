# Key 治理与预算

和 LiteLLM 开源版一样，单个 API Key 的限制**免费**：允许的模型、到期时间、
RPM/TPM 限流，以及 Key 自己的 Token 或金额预算。企业版功能键
`feature.lite.budgets`（布尔）只解锁两项：

- **用户和团队预算**：一个用户在所有 Key 和网页端的累计用量，以及团队所有成员的
  累计用量。没有 License 时，`PUT /api/team/users/{id}/budget` 和
  `PUT /api/team/teams/{id}/budget` 返回 `403 feature_not_licensed`；读取不门控。
- **80% 提醒**：任何预算用到 80% 时，推理响应带
  `X-CSGLite-Budget-Warning` 头（例如 `key=85%, team=92%`），列表接口的
  `spend.warning` 为 `true`，页面上显示黄色标记。

代码位置：Key 限制、用量账本和通用预算检查在 `internal/keygov`（Apache-2.0），
HTTP 接入在 `internal/server/key_governance.go`；用户和团队预算的范围与提醒头在
`ee/budget`。

## 1. 参考

| 方面 | LiteLLM 开源版 | CSGLite |
|---|---|---|
| 模型白名单 | `models` | `limits.models`，`*` 结尾按前缀匹配；`/v1/models` 只列出允许的模型 |
| 到期 | `duration` / `expires` | `limits.expires_at`，页面上按日期选，当天 23:59:59 到期 |
| 限流 | `rpm_limit` / `tpm_limit` | `limits.rpm` / `limits.tpm`，最近一分钟的滑动窗口 |
| 预算 | `max_budget` + `budget_duration`（美元） | `budget.tokens` 和/或 `budget.cost` + `currency`，周期为每天、每月或总计 |
| 用户 / 团队预算 | 免费 | 企业版 |
| 软预算告警 | 企业版（Slack/邮件） | 企业版（响应头和页面标记） |

## 2. 拒绝方式

只对推理路由生效（`/v1/*`、`/anthropic/*`、`/api/chat`、`/api/generate`，
以及 `/providers/{id}/v1/*`）。本机操作者不带 Key 时不受限制。

| 情况 | 状态码 | 代码 |
|---|---|---|
| Key 已到期 | 401 | `api_key_expired` |
| 模型不在白名单 | 403 | `model_not_allowed` |
| Key、用户或团队预算用完 | 429 | `budget_exceeded` |
| 超过 RPM/TPM | 429 | `rate_limited`，带 `Retry-After` |

OpenAI 兼容路由和 Anthropic 路由把代码放在 `error.type`，Ollama 路由放在
`code`。检查顺序是到期、模型、预算、限流，被拒绝的请求不占用限流额度。

## 3. 用量怎么算

- 每个计量过的请求结束时，把 Token 数和估算金额记到存储根目录下的
  `spend.db`（SQLite），按「对象、本地日期、币种」累加。对象是 Key、Key 的
  所有者，以及所有者当时所在的团队。网页端登录用户的对话也记到用户和团队上。
- 账本从本版本开始记录，之前的用量不计入预算。删除再新建 Key 不能绕过用户和
  团队预算，因为用户和团队有自己的记录。
- 金额只统计模型有该币种定价的请求；没有定价的请求只计 Token。
- 「每天」从本地零点开始，「每月」从本地每月 1 日开始，「总计」不重置。
- 预算在请求开始前检查，所以最后一个请求可能让用量略超预算，和 LiteLLM 一样。
- RPM/TPM 计数在内存里，重启后清零。TPM 在请求结束时计入，所以窗口内的
  第一个大请求不会被拦。

## 4. 谁能改

- 任何人创建 Key 时都可以给自己的 Key 加限制。
- 创建后只有管理员能改限制（`PUT /api/api-keys/{id}` 带 `limits`），成员只能
  改名，避免成员解除管理员给的限制。`limits: {}` 删除全部限制。
- 用户和团队预算只有管理员能设（企业版）。

## 5. License 失效后

- Key 限制不受影响。
- 已设置的用户和团队预算继续生效，保证花费仍有上限；但在续期前不能修改。
- 80% 提醒停止。
