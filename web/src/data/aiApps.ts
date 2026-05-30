import type { Locale } from "../i18n";

export type AIAppCategory = "coding" | "automation" | "documents-rag";
export type AIAppInstallMode = "script" | "npm" | "docker";
export type AIAppProgressMode = "percent" | "indeterminate";
export type AIAppStatus = "idle" | "installing" | "uninstalling" | "installed" | "failed" | "disabled";

export interface LocalizedText {
  en: string;
  zh: string;
}

export interface AIAppCatalogEntry {
  id: string;
  name: string;
  siteLabel: string;
  website: string;
  detailsUrl: string;
  icon: string;
  category: AIAppCategory;
  description: LocalizedText;
  installMode: AIAppInstallMode;
  progressMode: AIAppProgressMode;
  installHint: LocalizedText;
  cnInstallHint: LocalizedText;
  commandPreview: string;
  liveLogsReady: boolean;
  plannedSteps: LocalizedText[];
  status: AIAppStatus;
  progress?: number;
  statusText: LocalizedText;
}

export interface AIAppRuntimeState {
  status: AIAppStatus;
  phase: string;
  progressMode: AIAppProgressMode;
  progress?: number;
  managed: boolean;
  supported: boolean;
  disabled: boolean;
  liveLogsReady: boolean;
  installPath?: string;
  version?: string;
  latestVersion?: string;
  updateAvailable?: boolean;
  modelID?: string;
  runtimeSupported: boolean;
  runtimeRunning: boolean;
  runtimeStatus?: "running" | "stopped";
  logPath?: string;
  lastError?: string;
  logLines: string[];
}

const claudeCodeInstallScriptURL =
  "https://git-devops.opencsg.com/opensource/csghub-lite/-/raw/main/internal/apps/scripts/claude-code-install.sh";

export const aiAppCategoryOptions: Array<{ id: "all" | AIAppCategory; label: LocalizedText }> = [
  { id: "all", label: { en: "All", zh: "全部" } },
  { id: "coding", label: { en: "Coding", zh: "编程" } },
  { id: "automation", label: { en: "Automation", zh: "自动化" } },
  { id: "documents-rag", label: { en: "Documents & RAG", zh: "文档与 RAG" } },
];

export const aiAppsCatalog: AIAppCatalogEntry[] = [
  {
    id: "claude-code",
    name: "Claude Code",
    siteLabel: "@claude.ai",
    website: "https://claude.ai",
    detailsUrl: "https://docs.anthropic.com/en/docs/claude-code/setup",
    icon: "/apps/claude-code.svg",
    category: "coding",
    description: {
      en: "Anthropic's agentic coding tool that can read, edit, and run code in your working directory.",
      zh: "Anthropic 的智能编程工具，可在你的工作目录中读取、修改并执行代码。",
    },
    installMode: "script",
    progressMode: "indeterminate",
    installHint: {
      en: "Install the mirrored Claude Code runtime for macOS, Linux, and Windows without requiring Node or upstream downloads.",
      zh: "通过镜像的 Claude Code runtime 完成安装，支持 macOS、Linux 和 Windows，且不依赖 Node 或上游下载。",
    },
    cnInstallHint: {
      en: "By default the installer reads a versioned Claude Code mirror and wires the launcher locally; set CSGHUB_LITE_CLAUDE_DIST_BASE_URL only when testing another mirror.",
      zh: "默认从版本化的 Claude Code 镜像读取 runtime 并在本地配置启动命令；仅在测试其他镜像时才需要设置 CSGHUB_LITE_CLAUDE_DIST_BASE_URL。",
    },
    commandPreview: `curl -fsSL ${claudeCodeInstallScriptURL} | bash`,
    liveLogsReady: true,
    plannedSteps: [
      {
        en: "Resolve the requested Claude Code version from the mirrored manifest.",
        zh: "从镜像 manifest 中解析目标 Claude Code 版本。",
      },
      {
        en: "Download the mirrored runtime binary for the current platform and verify its checksum.",
        zh: "下载当前平台对应的镜像 runtime 二进制，并校验其 checksum。",
      },
      {
        en: "Place the versioned runtime locally, wire the claude launcher, and then open the first interactive session to finish sign-in and permissions.",
        zh: "将版本化 runtime 落盘到本地并配置 claude 启动命令，然后在首次交互会话中完成登录与权限确认。",
      },
    ],
    status: "idle",
    statusText: {
      en: "Ready to install latest",
      zh: "可安装最新版本",
    },
  },
  {
    id: "open-code",
    name: "OpenCode",
    siteLabel: "@opencode.ai",
    website: "https://opencode.ai",
    detailsUrl: "https://opencode.ai/docs/cli/",
    icon: "/apps/open-code.svg",
    category: "coding",
    description: {
      en: "An open-source AI coding agent that works in the terminal, desktop app, and editor extensions.",
      zh: "开源 AI 编码代理，支持终端、桌面应用和 IDE 扩展等多种使用方式。",
    },
    installMode: "script",
    progressMode: "percent",
    installHint: {
      en: "Install the mirrored OpenCode release for macOS, Linux, and Windows without requiring a local Node runtime.",
      zh: "通过镜像的 OpenCode 发布包完成安装，支持 macOS、Linux 和 Windows，且不依赖本地 Node 运行时。",
    },
    cnInstallHint: {
      en: "By default the installer reads a versioned OpenCode mirror and wires the launcher locally; set CSGHUB_LITE_OPEN_CODE_DIST_BASE_URL only when testing another mirror.",
      zh: "默认从版本化的 OpenCode 镜像读取发布包并在本地配置启动命令；仅在测试其他镜像时才需要设置 CSGHUB_LITE_OPEN_CODE_DIST_BASE_URL。",
    },
    commandPreview: "curl -fsSL https://git-devops.opencsg.com/opensource/apps/-/raw/main/open-code/install.sh | bash",
    liveLogsReady: true,
    plannedSteps: [
      {
        en: "Resolve the requested OpenCode version from the mirrored manifest.",
        zh: "从镜像 manifest 中解析目标 OpenCode 版本。",
      },
      {
        en: "Download the mirrored archive for the current platform, verify its checksum, and extract the packaged runtime.",
        zh: "下载当前平台对应的镜像归档，校验 checksum，并解压打包好的 runtime。",
      },
      {
        en: "Wire the opencode launcher to the extracted runtime and start the first session.",
        zh: "将 opencode 启动命令指向解压后的 runtime，并启动首次会话。",
      },
    ],
    status: "idle",
    statusText: {
      en: "Ready to install latest",
      zh: "可安装最新版本",
    },
  },
  {
    id: "openclaw",
    name: "OpenClaw",
    siteLabel: "@openclaw.ai",
    website: "https://openclaw.ai",
    detailsUrl: "https://docs.openclaw.ai/install",
    icon: "/apps/openclaw.svg",
    category: "automation",
    description: {
      en: "A personal AI assistant that runs on your own devices and bridges messaging services, workflows, and local control.",
      zh: "运行在你自己设备上的个人 AI 助手，可连接消息渠道、自动化工作流与本地控制能力。",
    },
    installMode: "script",
    progressMode: "indeterminate",
    installHint: {
      en: "Use the official installer script first, then finish onboarding and daemon setup.",
      zh: "优先使用官方安装脚本，再完成引导配置与守护进程安装。",
    },
    cnInstallHint: {
      en: "For domestic environments, prepare a proxy or mirror the installer resources internally before running the setup.",
      zh: "国内环境建议先准备代理，或将安装资源镜像到内网后再执行安装。",
    },
    commandPreview: "curl -fsSL https://git-devops.opencsg.com/opensource/apps/-/raw/main/openclaw/install.sh | bash",
    liveLogsReady: true,
    plannedSteps: [
      {
        en: "Run the installer script and ensure a supported Node runtime is available.",
        zh: "运行安装脚本，并确保当前环境具备受支持的 Node 运行时。",
      },
      {
        en: "Complete onboarding, authentication, and local daemon installation.",
        zh: "完成引导配置、鉴权以及本地守护进程安装。",
      },
      {
        en: "Verify gateway status and open the dashboard to finish setup.",
        zh: "检查 gateway 状态，并打开 dashboard 完成初始化。",
      },
    ],
    status: "idle",
    statusText: {
      en: "Ready to install",
      zh: "可安装",
    },
  },
  {
    id: "codex",
    name: "Codex",
    siteLabel: "@openai.com",
    website: "https://developers.openai.com/codex/cli",
    detailsUrl: "https://developers.openai.com/codex/cli",
    icon: "/apps/codex.svg",
    category: "coding",
    description: {
      en: "OpenAI's local coding agent CLI for reviewing code, editing files, and automating terminal workflows.",
      zh: "OpenAI 的本地编码代理 CLI，可用于代码审查、文件修改和终端自动化工作流。",
    },
    installMode: "script",
    progressMode: "percent",
    installHint: {
      en: "Install the mirrored Codex release for macOS, Linux, and Windows without requiring a local Node runtime.",
      zh: "通过镜像的 Codex 发布包完成安装，支持 macOS、Linux 和 Windows，且不依赖本地 Node 运行时。",
    },
    cnInstallHint: {
      en: "By default the installer reads a versioned Codex mirror and wires the launcher locally; set CSGHUB_LITE_CODEX_DIST_BASE_URL only when testing another mirror.",
      zh: "默认从版本化的 Codex 镜像读取发布包并在本地配置启动命令；仅在测试其他镜像时才需要设置 CSGHUB_LITE_CODEX_DIST_BASE_URL。",
    },
    commandPreview: "curl -fsSL https://git-devops.opencsg.com/opensource/apps/-/raw/main/codex/install.sh | bash",
    liveLogsReady: true,
    plannedSteps: [
      {
        en: "Resolve the requested Codex version from the mirrored manifest.",
        zh: "从镜像 manifest 中解析目标 Codex 版本。",
      },
      {
        en: "Download the mirrored archive for the current platform, verify its checksum, and extract the packaged runtime.",
        zh: "下载当前平台对应的镜像归档，校验 checksum，并解压打包好的 runtime。",
      },
      {
        en: "Wire the codex launcher to the extracted runtime and start Codex once to finish authentication.",
        zh: "将 codex 启动命令指向解压后的 runtime，并首次运行 Codex 完成认证。",
      },
    ],
    status: "idle",
    statusText: {
      en: "Ready to install",
      zh: "可安装",
    },
  },
  {
    id: "antigravity",
    name: "Antigravity",
    siteLabel: "@google",
    website: "https://antigravity.google",
    detailsUrl: "https://antigravity.google",
    icon: "/apps/antigravity.svg",
    category: "coding",
    description: {
      en: "Google's AI coding agent CLI for working with projects from the terminal.",
      zh: "Google 的 AI 编程 Agent CLI，可在终端中协助处理项目代码。",
    },
    installMode: "script",
    progressMode: "percent",
    installHint: {
      en: "Install the mirrored Antigravity CLI release for macOS, Linux, and Windows.",
      zh: "通过镜像的 Antigravity CLI 发布包完成安装，支持 macOS、Linux 和 Windows。",
    },
    cnInstallHint: {
      en: "By default the installer reads a versioned Antigravity mirror and wires the agy launcher locally; set CSGHUB_LITE_ANTIGRAVITY_DIST_BASE_URL only when testing another mirror.",
      zh: "默认从版本化的 Antigravity 镜像读取发布包并在本地配置 agy 启动命令；仅在测试其他镜像时才需要设置 CSGHUB_LITE_ANTIGRAVITY_DIST_BASE_URL。",
    },
    commandPreview: "curl -fsSL https://antigravity.google/cli/install.sh | bash",
    liveLogsReady: true,
    plannedSteps: [
      {
        en: "Resolve the requested Antigravity CLI version from the mirrored manifest.",
        zh: "从镜像 manifest 中解析目标 Antigravity CLI 版本。",
      },
      {
        en: "Download the mirrored release for the current platform and verify its SHA512 checksum.",
        zh: "下载当前平台对应的镜像发布包，并校验 SHA512 checksum。",
      },
      {
        en: "Place the agy launcher in the user's local bin directory and run Antigravity's shell environment setup.",
        zh: "将 agy 启动命令安装到当前用户本地 bin 目录，并执行 Antigravity 的 shell 环境配置。",
      },
    ],
    status: "idle",
    statusText: {
      en: "Ready to install",
      zh: "可安装",
    },
  },
  {
    id: "pi",
    name: "Pi",
    siteLabel: "@pi.dev",
    website: "https://pi.dev",
    detailsUrl: "https://docs.ollama.com/integrations/pi",
    icon: "/apps/pi.svg",
    category: "coding",
    description: {
      en: "A minimal terminal coding agent with read, write, edit, and bash tools, configurable for local OpenAI-compatible models.",
      zh: "轻量级终端编程 Agent，内置读取、写入、编辑和 bash 工具，可连接本地 OpenAI 兼容模型。",
    },
    installMode: "script",
    progressMode: "indeterminate",
    installHint: {
      en: "Install Pi Coding Agent from npm, then launch it with the csghub-lite provider and selected model.",
      zh: "通过 npm 安装 Pi Coding Agent，然后使用 csghub-lite provider 和所选模型启动。",
    },
    cnInstallHint: {
      en: "The installer uses the configured npm registry and defaults to npmmirror for faster domestic installs.",
      zh: "安装脚本会使用配置的 npm registry，默认走 npmmirror 以改善国内安装速度。",
    },
    commandPreview: "npm install -g --prefix ~/.local/share/pi-coding-agent @mariozechner/pi-coding-agent",
    liveLogsReady: true,
    plannedSteps: [
      {
        en: "Ensure npm is available in the local environment.",
        zh: "检查本地环境是否可用 npm。",
      },
      {
        en: "Install the @mariozechner/pi-coding-agent package into a user-owned prefix.",
        zh: "将 @mariozechner/pi-coding-agent 安装到当前用户拥有的 prefix。",
      },
      {
        en: "Write the csghub-lite provider into Pi models.json and launch the terminal agent.",
        zh: "将 csghub-lite provider 写入 Pi 的 models.json，并启动终端 Agent。",
      },
    ],
    status: "idle",
    statusText: {
      en: "Ready to install",
      zh: "可安装",
    },
  },
  {
    id: "csgclaw",
    name: "CSGClaw",
    siteLabel: "@opencsg.com",
    website: "https://github.com/OpenCSGs/csgclaw",
    detailsUrl: "https://github.com/OpenCSGs/csgclaw",
    icon: "/apps/csgclaw.svg",
    category: "automation",
    description: {
      en: "A multi-agent collaboration platform that coordinates a team of specialized AI workers through a single Manager, with a built-in WebUI workspace.",
      zh: "多智能体协作平台，通过统一的 Manager 协调多个专业 AI Worker，内置 WebUI 工作空间。",
    },
    installMode: "script",
    progressMode: "percent",
    installHint: {
      en: "Install the official CSGClaw release for macOS (arm64) and Linux (amd64/arm64). Windows is not currently supported.",
      zh: "通过官方 CSGClaw 发布包完成安装，支持 macOS (arm64) 和 Linux (amd64/arm64)。暂不支持 Windows。",
    },
    cnInstallHint: {
      en: "By default the installer reads official CSGClaw releases; launch config uses the OpenCSG PicoClaw manager image, so there is no separate PicoClaw app sync step.",
      zh: "默认从官方 CSGClaw 发布源读取发布包；启动配置会使用 OpenCSG 的 PicoClaw manager 镜像，不再需要单独同步 PicoClaw 应用。",
    },
    commandPreview: "curl -fsSL https://csgclaw.opencsg.com/install.sh | bash",
    liveLogsReady: true,
    plannedSteps: [
      {
        en: "Resolve the requested CSGClaw version from the official release feed.",
        zh: "从官方发布源解析目标 CSGClaw 版本。",
      },
      {
        en: "Download the official archive for the current platform and install the bundled runtime.",
        zh: "下载当前平台对应的官方归档，并安装随包 runtime。",
      },
      {
        en: "Write the csghub-lite provider into config.toml, then start the WebUI through csgclaw serve daemon mode.",
        zh: "将 csghub-lite provider 写入 config.toml，然后通过 csgclaw serve daemon 模式启动 WebUI。",
      },
    ],
    status: "idle",
    statusText: {
      en: "Ready to install",
      zh: "可安装",
    },
  },
  {
    id: "dify",
    name: "Dify",
    siteLabel: "@dify.ai",
    website: "https://dify.ai",
    detailsUrl: "https://docs.dify.ai/getting-started/install-self-hosted",
    icon: "/apps/dify.svg",
    category: "automation",
    description: {
      en: "An open-source platform for building agentic workflows, chat applications, and LLM orchestration services.",
      zh: "面向工作流、聊天应用和 LLM 编排场景的开源平台。",
    },
    installMode: "docker",
    progressMode: "indeterminate",
    installHint: {
      en: "Use the official Docker Compose deployment for a self-hosted installation.",
      zh: "首版建议采用官方 Docker Compose 方式进行自托管部署。",
    },
    cnInstallHint: {
      en: "Domestic deployments should prepare Git mirrors and Docker registry acceleration ahead of time.",
      zh: "国内部署建议提前准备 Git 镜像与 Docker 镜像加速配置。",
    },
    commandPreview: [
      "git clone https://github.com/langgenius/dify.git",
      "cd dify/docker",
      "cp .env.example .env",
      "docker compose up -d",
    ].join("\n"),
    liveLogsReady: true,
    plannedSteps: [
      {
        en: "Clone the deployment repository and prepare the .env file.",
        zh: "克隆部署仓库并准备 .env 配置文件。",
      },
      {
        en: "Pull required service images and boot the compose stack.",
        zh: "拉取所需服务镜像并拉起 compose 栈。",
      },
      {
        en: "Wait for the API, worker, and web services to become healthy.",
        zh: "等待 API、worker 和 web 服务进入健康状态。",
      },
    ],
    status: "disabled",
    statusText: {
      en: "Disabled in AI Apps",
      zh: "应用页暂不支持",
    },
  },
  {
    id: "anythingllm",
    name: "AnythingLLM",
    siteLabel: "@anythingllm.com",
    website: "https://anythingllm.com",
    detailsUrl: "https://docs.anythingllm.com/installation-docker/overview",
    icon: "/apps/anythingllm.svg",
    category: "documents-rag",
    description: {
      en: "An all-in-one AI application for chat with documents, agents, and private RAG workflows.",
      zh: "一体化 AI 应用，支持文档问答、Agent 和私有化 RAG 工作流。",
    },
    installMode: "docker",
    progressMode: "indeterminate",
    installHint: {
      en: "The official self-hosted path uses Docker images published by Mintplex Labs.",
      zh: "官方自托管路径基于 Mintplex Labs 发布的 Docker 镜像。",
    },
    cnInstallHint: {
      en: "Domestic environments should prepare Docker Hub acceleration or a mirrored image registry before installation.",
      zh: "国内环境建议先配置 Docker Hub 加速或镜像代理，再执行安装。",
    },
    commandPreview: [
      "docker pull mintplexlabs/anythingllm:latest",
      "docker run -d -p 3001:3001 --cap-add SYS_ADMIN \\",
      "  -v ${STORAGE_LOCATION}:/app/server/storage \\",
      "  mintplexlabs/anythingllm:latest",
    ].join("\n"),
    liveLogsReady: true,
    plannedSteps: [
      {
        en: "Pull the official image and prepare a persistent storage mount.",
        zh: "拉取官方镜像并准备持久化存储挂载目录。",
      },
      {
        en: "Start the container with the required port and capability flags.",
        zh: "带上端口与能力参数启动容器。",
      },
      {
        en: "Verify the web UI and storage path before exposing it to users.",
        zh: "在对外开放前，校验 Web UI 与存储路径是否可用。",
      },
    ],
    status: "disabled",
    statusText: {
      en: "Disabled in AI Apps",
      zh: "应用页暂不支持",
    },
  },
];

export const initialAIAppStates = aiAppsCatalog.reduce<Record<string, AIAppRuntimeState>>((acc, app) => {
  acc[app.id] = {
    status: app.status,
    phase: app.status === "disabled" ? "docker_disabled" : "ready",
    progressMode: app.progressMode,
    progress: app.status === "disabled" ? 0 : undefined,
    managed: false,
    supported: app.installMode !== "docker",
    disabled: app.installMode === "docker",
    liveLogsReady: app.liveLogsReady,
    runtimeSupported: ["openclaw", "csgclaw"].includes(app.id),
    runtimeRunning: false,
    runtimeStatus: ["openclaw", "csgclaw"].includes(app.id) ? "stopped" : undefined,
    logLines: [],
  };
  return acc;
}, {});

export function createAIAppStateSnapshot(): Record<string, AIAppRuntimeState> {
  return Object.fromEntries(
    Object.entries(initialAIAppStates).map(([id, state]) => [id, { ...state, logLines: [...state.logLines] }])
  ) as Record<string, AIAppRuntimeState>;
}

export function getLocalizedText(text: LocalizedText, currentLocale: Locale): string {
  return currentLocale === "zh" ? text.zh : text.en;
}
