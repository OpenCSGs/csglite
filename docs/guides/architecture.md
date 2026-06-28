# 架构设计

## 系统架构

```
┌──────────────────────────────────────────────────┐
│                   CLI (Cobra)                    │
│  run │ chat │ pull │ list │ show │ serve │ ...   │
└────────┬────────────────────────┬────────────────┘
         │                        │
         │ 直接调用               │ HTTP 请求
         ▼                        ▼
┌─────────────────┐     ┌─────────────────────────┐
│  Model Manager  │◄────│      REST Server        │
│  (model/)       │     │      (server/)           │
└────────┬────────┘     │                          │
         │              │  /api/chat  /api/generate│
         │              │  /api/tags  /api/show    │
         │              │  /api/pull  /api/delete   │
         │              │  /api/ps    /api/stop     │
         │              └────────────┬─────────────┘
         │                           │
         ▼                           ▼
┌─────────────────┐     ┌──────────────────────────┐
│   CSGHub SDK    │     │    Inference Engine       │
│   (csghub/)     │     │    (inference/)           │
│                 │     │                           │
│  API 交互       │     │  llama-server 子进程管理  │
│  模型下载       │     │  流式 token 生成          │
│  LFS 处理       │     │  对话管理                 │
└────────┬────────┘     └────────────┬──────────────┘
         │                           │
         ▼                           ▼
┌─────────────────┐     ┌──────────────────────────┐
│  CSGHub Platform│     │     llama-server          │
│  (hub.opencsg.  │     │     (llama.cpp)           │
│   com)          │     │                           │
└─────────────────┘     └──────────────────────────┘
```

## 项目结构

```
csglite/
├── cmd/csghub-lite/         # 程序入口
│   └── main.go              # main 函数，版本注入
│
├── internal/                # 内部包（不对外暴露）
│   ├── cli/                 # CLI 命令层
│   │   ├── root.go          # 根命令，注册子命令
│   │   ├── run.go           # run 命令（自动下载+对话）
│   │   ├── chat.go          # chat 命令（本地对话）
│   │   ├── serve.go         # serve 命令（启动 API 服务）
│   │   ├── pull.go          # pull 命令
│   │   ├── list.go          # list/ls 命令
│   │   ├── show.go          # show 命令
│   │   ├── ps.go            # ps 命令
│   │   ├── stop.go          # stop 命令
│   │   ├── rm.go            # rm 命令
│   │   ├── search.go        # search 命令
│   │   ├── login.go         # login 命令
│   │   └── config.go        # config 子命令
│   │
│   ├── server/              # REST API 服务层
│   │   ├── server.go        # Server 结构体，生命周期管理
│   │   ├── routes.go        # 路由注册
│   │   └── handlers.go      # HTTP 处理函数
│   │
│   ├── csghub/              # CSGHub 平台 Go SDK
│   │   ├── client.go        # HTTP 客户端封装
│   │   ├── types.go         # API 请求/响应类型
│   │   ├── models.go        # 模型列表、搜索、详情
│   │   ├── download.go      # 文件下载（LFS + Raw）
│   │   └── snapshot.go      # 仓库快照下载
│   │
│   ├── model/               # 本地模型管理
│   │   ├── types.go         # LocalModel 类型定义
│   │   ├── storage.go       # 模型文件存储
│   │   ├── manifest.go      # manifest.json 管理
│   │   └── manager.go       # Manager（增删查改）
│   │
│   ├── inference/           # 推理引擎
│   │   ├── engine.go        # Engine 接口定义
│   │   ├── llama.go         # llama-server 子进程管理
│   │   ├── chat.go          # 对话格式处理
│   │   └── options.go       # 生成参数
│   │
│   └── config/              # 配置管理
│       └── config.go        # 配置加载/保存
│
├── pkg/api/                 # 公共 API 类型
│   └── types.go             # 请求/响应结构体
│
├── scripts/                 # 脚本
│   └── install.sh           # 一键安装脚本
│
├── Formula/                 # Homebrew
│   └── csghub-lite.rb       # Homebrew formula
│
├── docs/                    # 文档
├── .goreleaser.yml          # GoReleaser 配置
├── .github/workflows/       # CI/CD
├── Makefile                 # 构建脚本
├── go.mod / go.sum          # Go 模块依赖
└── README.md                # 项目概览
```

## 核心模块

### CLI 层 (`internal/cli/`)

基于 [Cobra](https://github.com/spf13/cobra) 框架。每个命令一个文件，`root.go` 统一注册。

关键设计：
- `run` 和 `chat` 共享相同的交互对话逻辑
- `run` 额外增加自动下载功能
- `chat` 支持 `--system` 自定义系统提示词
- `ps` 和 `stop` 通过 HTTP 请求与运行中的 `serve` 实例通信

### REST Server (`internal/server/`)

标准库 `net/http` 实现，不引入第三方框架。

关键设计：
- 使用 Go 1.22 的路由模式匹配（`GET /api/tags`、`POST /api/chat`）
- `engines` map 管理已加载的推理引擎实例（`sync.RWMutex` 保护并发访问）
- 首次请求时自动加载模型（lazy loading）
- 支持 SSE 流式响应和非流式 JSON 响应

### CSGHub SDK (`internal/csghub/`)

CSGHub 平台 API 的 Go 封装。

关键设计：
- 区分 LFS 文件和普通文件的下载逻辑
  - LFS 文件: `/resolve/` 端点，301 重定向到对象存储
  - 普通文件: `/raw/` 端点，JSON 中内嵌文件内容
- 支持断点续传（HTTP Range 请求）
- 进度回调函数（`ProgressFunc`）

### 推理引擎 (`internal/inference/`)

通过子进程调用 `llama-server`（而非 CGO 绑定）。

关键设计：
- `Engine` 接口抽象推理逻辑，便于扩展其他后端
- 自动搜索 `llama-server` 二进制文件（PATH、常见安装路径）
- 流式 token 生成通过 HTTP SSE 从 `llama-server` 获取
- 每个加载的模型对应一个独立的 `llama-server` 进程

### 模型管理 (`internal/model/`)

本地模型的元数据管理和文件存储。

关键设计：
- `manifest.json` 记录模型名称、格式、大小、下载时间等元信息
- 自动检测 GGUF / SafeTensors 格式
- 通过 `Manager` 提供统一的增删查改接口
