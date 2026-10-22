# user

管理共享实例的用户账号。账号功能免费；审计员角色需要企业版功能
`feature.lite.team_management`。

第一个账号创建后，所有通过网络访问此实例的人都必须登录或携带 API Key；在服务器
本机执行的命令和请求始终保留管理员权限。

## 用法

```bash
csghub-lite user list [--json]                          # 列出账号
csghub-lite user create <用户名> [--role admin|auditor|member]
                                 [--display-name 名称] [--email 邮箱]
                                 [--invite] [--password-stdin]
csghub-lite user reset-password <用户名> [--password-stdin] [--require-change]
```

- 密码默认交互输入两次且不回显；`--password-stdin` 从标准输入第一行读取，便于脚本使用。
- `create`：
  - 第一个账号总是管理员。
  - 之后为他人设置的密码，对方首次登录时必须修改。
  - 加 `--invite` 则不设密码，命令打印邀请链接路径 `/invite/<token>`（7 天有效），
    在前面加上实例地址后发给对方。
- `reset-password`：在本机直接修改账号文件，**不需要服务在运行，也不受 License
  状态影响**，是管理员忘记密码时的找回方式。该账号会被重新启用并在所有设备上退出
  登录；加 `--require-change` 则要求对方下次登录时再改一次。

## 示例

```bash
# 服务器上创建第一个管理员
csghub-lite user create admin --role admin

# 邀请成员，让对方自己设置密码
csghub-lite user create alice --role member --invite

# 管理员忘记密码
csghub-lite user reset-password admin
```

## 工作方式

- `create`：
  - 本机有运行中的服务时，通过本地 API 创建，License 与席位按 Web 界面同样的规则检查。
  - 没有运行中的服务时，直接写入 `~/.csghub-lite/team.json`；`--role auditor`
    同样需要 License。
- `list` 和 `reset-password` 始终直接读写 `team.json`，服务会立即看到变化。

相关：[用户、团队与角色](../guides/ee-team-management.md)、
[环境变量](../guides/environment-variables.md#用户账号)。
