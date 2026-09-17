# license

管理 CSGLite 企业版 License。

License 由 CSGHub 平台的 License 签发后台签发，交付为一个 `LICENSE KEY` PEM
文本文件。CSGLite 只负责校验，不负责签发；没有 License 时以社区版
（Community）运行，导入有效 License 后按企业版（Enterprise）解锁功能。

## 用法

```bash
csghub-lite license show               # 查看当前 License 状态
csghub-lite license verify <file|->    # 只校验，不安装
csghub-lite license install <file|->   # 安装（覆盖已有 License）
csghub-lite license remove             # 删除 License，回到社区版
csghub-lite license features           # 列出功能注册表；EE 列为 yes 的才受 License 控制
```

`<file|->` 可以是文件路径，也可以是 `-` 表示从标准输入读取。`show`、
`verify`、`features` 支持 `--json` 输出原始 JSON。

也可以在 Web 界面的「设置 → License」中粘贴或选择文件导入、校验和删除。
导入成功后侧边栏左上角显示 “CSGLite EE”。

## 工作方式

- 本机有正在运行的 `csghub-lite` 服务时，所有子命令通过本地 API
  （`/api/license*`）操作，改动立即生效。
- 没有运行中的服务时，直接读写存储根目录下的 `license.key`
  （默认 `~/.csghub-lite/license.key`），服务下次启动时读取。

## 状态说明

| 状态 | 含义 |
|---|---|
| `none` | 未安装 License，以社区版运行 |
| `not_started` | 签名有效，但尚未到生效时间 |
| `valid` | 有效期内，企业版功能可用 |
| `grace` | 已过期但在 14 天宽限期内（或检测到系统时钟回拨），功能仍可用并提示续期 |
| `expired` | 超过宽限期，回到社区版 |
| `invalid` | 签名错误、格式错误、产品不是 CSGLite，或要求更高的 CSGLite 版本 |

`install` 会拒绝 `invalid` 和 `expired` 的 License，已安装的 License 不受影响。

只有在注册表中标记为 EE（`features` 输出的 EE 列为 `yes`）的功能才会校验
License，其余功能在社区版下也始终可用。

## 与 `license.txt` 的区别

`EE=1` 方式安装时，安装脚本会把《企业版许可协议》文本写为安装目录下的
`license.txt`，那是法律条款，不是授权凭证。本命令管理的是 OpenCSG 签发的签名
文件 `license.key`，只有它能解锁企业版功能。

## 相关环境变量

| 变量 | 说明 |
|---|---|
| `CSGHUB_LITE_LICENSE_FILE` | 覆盖 License 文件路径 |
| `CSGHUB_LITE_LICENSE` | 直接提供 License 文本（容器、CI 场景）；设置后 `install` 与 `remove` 会被拒绝 |
| `CSGHUB_LITE_LICENSE_PUBLIC_KEY_FILE` | 用指定的 PEM 公钥替代内置的签发方公钥，仅用于预发环境联调 |

## 示例

```bash
$ csghub-lite license install ./csglite.lic
License installed.
Edition:     Enterprise
Status:      valid
Company:     某某科技有限公司
Product:     CSGLite
License ID:  8f2a5b1e-...
Seats:       50
Valid from:  2026-10-01
Expires:     2027-10-01
Grace until: 2027-10-15
Features:    feature.lite.ai_apps, feature.lite.image_generation, ...
```
