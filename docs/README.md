# csghub-lite 文档

csghub-lite 是一个轻量级的本地大语言模型运行工具，基于 [CSGHub](https://hub.opencsg.com) 平台的模型生态。

## 文档目录

### 快速上手

- [安装指南](getting-started/installation.md) — 多种安装方式：一键脚本、Homebrew（主要面向 macOS）、源码编译
- [快速入门](getting-started/quickstart.md) — 5 分钟上手：搜索、下载、对话、启动 API

### CLI 命令参考

- [命令总览](cli/overview.md) — 所有命令一览表
- [run](cli/run.md) — 自动下载并交互对话
- [chat](cli/chat.md) — 与本地模型交互对话
- [serve](cli/serve.md) — 启动 REST API 服务
- [restart](cli/restart.md) — 重启后台 API 服务
- [pull](cli/pull.md) — 下载模型
- [list](cli/list.md) — 列出本地模型
- [show](cli/show.md) — 查看模型详情
- [ps](cli/ps.md) — 查看运行中的模型
- [stop](cli/stop.md) — 停止运行中的模型
- [rm](cli/rm.md) — 删除本地模型
- [search](cli/search.md) — 搜索 CSGHub 模型
- [login](cli/login.md) — 设置访问令牌
- [config](cli/config.md) — 配置管理

### REST API 参考

- [API 总览](api/overview.md) — 端点列表与通用说明
- [Chat 对话接口](api/chat.md) — `POST /api/chat`
- [Generate 生成接口](api/generate.md) — `POST /api/generate`
- [模型管理接口](api/models.md) — tags / show / pull / delete / ps / stop

### 使用指南

- [配置说明](guides/configuration.md) — 配置文件、私有化部署、环境变量
- [模型格式](guides/model-formats.md) — GGUF / SafeTensors 格式说明与转换
- [打包与发布](guides/packaging.md) — GoReleaser、Homebrew（主要面向 macOS）、安装脚本
- [架构设计](guides/architecture.md) — 项目结构与模块设计
