import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { getTags, openAIApp, type ModelInfo } from "../api/client";
import { locale, t } from "../i18n";

type ConnectionState = "connecting" | "connected" | "disconnected" | "exited";
const claudeCodeAppId = "claude-code";
const shellAppsWithModelSwitch = new Set([claudeCodeAppId, "pi"]);
const shellAppsWithWorkDirSwitch = new Set(["claude-code", "open-code", "codex", "pi"]);

interface ShellControlMessage {
  type: string;
  session_id?: string;
  app_id?: string;
  title?: string;
  model_id?: string;
  work_dir?: string;
  exit_code?: number;
  error?: string;
}

function encodeTerminalBinary(data: string): Uint8Array {
  return Uint8Array.from(data, (char) => char.charCodeAt(0) & 0xff);
}

function decrqmStatus(mode: number): number {
  switch (mode) {
    case 1:
    case 1000:
    case 1002:
    case 1003:
    case 1004:
    case 1006:
    case 1016:
    case 2004:
    case 2026:
      return 2;
    case 7:
    case 25:
    case 1048:
      return 1;
    case 8:
      return 3;
    case 47:
    case 1047:
    case 1049:
      return 2;
    default:
      return 0;
  }
}

function registerOpenCodeXtermCompat(terminal: Terminal, sendInput: (payload: Uint8Array) => void) {
  return terminal.parser.registerCsiHandler({ prefix: "?", intermediates: "$", final: "p" }, (params) => {
    const first = params[0];
    const mode = typeof first === "number" ? first : Array.isArray(first) ? first[0] : 0;
    if (!mode) {
      return false;
    }
    const reply = `\u001b[?${mode};${decrqmStatus(mode)}$y`;
    sendInput(new TextEncoder().encode(reply));
    return true;
  });
}

function shellWebSocketURL(sessionId: string): string {
  const protocol = location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${location.host}/api/apps/shell/${encodeURIComponent(sessionId)}/ws`;
}

function shellModelKey(model: Pick<ModelInfo, "model" | "source">): string {
  return `${model.source || "local"}:${model.model}`;
}

function parseShellModelKey(key: string): { source: string; model: string } {
  const providerPrefix = "provider:";
  if (key.startsWith(providerPrefix)) {
    const next = key.indexOf(":", providerPrefix.length);
    if (next > providerPrefix.length) {
      return { source: key.slice(0, next), model: key.slice(next + 1) };
    }
  }
  const first = key.indexOf(":");
  if (first > 0) {
    return { source: key.slice(0, first), model: key.slice(first + 1) };
  }
  return { source: "", model: key };
}

function normalizeShellModels(models: ModelInfo[]): ModelInfo[] {
  const seen = new Set<string>();
  const out: ModelInfo[] = [];
  for (const model of models) {
    const modelId = model.model?.trim();
    const key = shellModelKey(model);
    if (!modelId || seen.has(key)) {
      continue;
    }
    seen.add(key);
    out.push(model);
  }
  return out;
}

function formatShellModelLabel(model: ModelInfo): string {
  const name = model.display_name || model.model;
  const src = model.source || "local";
  if (src === "cloud") {
    return `${name} [${t("aiApps.modelSourceCloud")}]`;
  }
  if (src.startsWith("provider:")) {
    return name;
  }
  return `${name} [${t("aiApps.modelSourceLocal")}]`;
}

async function closeShellSession(sessionId: string): Promise<void> {
  if (!sessionId) {
    return;
  }
  await fetch(`/api/apps/shell/${encodeURIComponent(sessionId)}/close`, {
    method: "POST",
    keepalive: true,
  }).catch(() => {});
}

export function AIAppShell() {
  void locale.value;

  const containerRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const sessionId = useMemo(() => new URLSearchParams(location.search).get("session_id")?.trim() || "", []);
  const queryAppId = useMemo(() => new URLSearchParams(location.search).get("app_id")?.trim() || "", []);
  const [title, setTitle] = useState("");
  const [modelId, setModelId] = useState("");
  const [appId, setAppId] = useState(queryAppId);
  const [error, setError] = useState("");
  const [state, setState] = useState<ConnectionState>("connecting");
  const [exitCode, setExitCode] = useState<number | null>(null);
  const [workDir, setWorkDir] = useState("");
  const [workDirInput, setWorkDirInput] = useState("");
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [selectedModel, setSelectedModel] = useState("");
  const [applyingLaunchConfig, setApplyingLaunchConfig] = useState(false);
  const exitSeenRef = useRef(false);

  useEffect(() => {
    if (!sessionId) {
      setError(t("aiApps.shellSessionMissing"));
      setState("disconnected");
      return;
    }
    const terminalContainer = containerRef.current;
    if (!terminalContainer) {
      return;
    }

    const terminal = new Terminal({
      cursorBlink: true,
      fontFamily: "ui-monospace, SFMono-Regular, SF Mono, Menlo, Consolas, monospace",
      fontSize: 13,
      lineHeight: 1.35,
      theme: {
        background: "#020617",
        foreground: "#E5E7EB",
        cursor: "#818CF8",
        selectionBackground: "rgba(129, 140, 248, 0.28)",
      },
      convertEol: false,
      allowProposedApi: false,
      scrollback: 5000,
      ...(queryAppId === "open-code"
        ? {
            // Full-screen OpenCode UI queries terminal/window dimensions on startup.
            windowOptions: {
              getWinSizePixels: true,
              getCellSizePixels: true,
              getWinSizeChars: true,
            },
          }
        : {}),
    });
    terminalRef.current = terminal;
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.open(terminalContainer);
    terminal.focus();

    const ws = new WebSocket(shellWebSocketURL(sessionId));
    ws.binaryType = "arraybuffer";
    const encoder = new TextEncoder();
    const sendInput = (payload: Uint8Array) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(payload);
      }
    };
    const xtermCompatDisposable = appId === "open-code"
      ? registerOpenCodeXtermCompat(terminal, sendInput)
      : null;

    let resizeFrame: number | null = null;
    const resizeTimers: number[] = [];
    let resizeObserver: ResizeObserver | null = null;

    const scheduleResize = () => {
      if (resizeFrame !== null) {
        return;
      }
      resizeFrame = window.requestAnimationFrame(() => {
        resizeFrame = null;
        sendResize();
      });
    };

    let followOutputFrame: number | null = null;
    const scheduleFollowOutput = () => {
      if (followOutputFrame !== null) {
        return;
      }
      followOutputFrame = window.requestAnimationFrame(() => {
        followOutputFrame = null;
        if (terminal.rows > 0) {
          terminal.scrollToBottom();
        }
      });
    };

    const sendResize = () => {
      fitAddon.fit();
      if (terminal.rows > 0) {
        scheduleFollowOutput();
      }
      if (ws.readyState !== WebSocket.OPEN) {
        return;
      }
      ws.send(JSON.stringify({
        type: "resize",
        cols: terminal.cols,
        rows: terminal.rows,
      }));
    };

    const resizeHandler = () => {
      scheduleResize();
    };

    const inputDisposable = terminal.onData((data) => {
      sendInput(encoder.encode(data));
      scheduleFollowOutput();
    });
    const binaryDisposable = terminal.onBinary((data) => {
      sendInput(encodeTerminalBinary(data));
    });

    const pointerFocusHandler = () => {
      terminal.focus();
    };
    const visibilityHandler = () => {
      if (document.visibilityState === "visible") {
        scheduleResize();
        terminal.focus();
      }
    };

    terminalContainer.addEventListener("mousedown", pointerFocusHandler);
    document.addEventListener("visibilitychange", visibilityHandler);

    if (typeof ResizeObserver !== "undefined") {
      resizeObserver = new ResizeObserver(() => {
        scheduleResize();
      });
      resizeObserver.observe(terminalContainer);
    }

    ws.onopen = () => {
      setState("connected");
      setError("");
      scheduleResize();
      resizeTimers.push(window.setTimeout(scheduleResize, 80));
      resizeTimers.push(window.setTimeout(scheduleResize, 250));
    };

    ws.onmessage = (event) => {
      if (typeof event.data === "string") {
        let message: ShellControlMessage | null = null;
        try {
          message = JSON.parse(event.data) as ShellControlMessage;
        } catch {
          message = null;
        }
        if (!message) {
          return;
        }

        if (message.type === "ready") {
          setTitle(message.title || "");
          setAppId(message.app_id || "");
          setModelId(message.model_id || "");
          setWorkDir(message.work_dir || "");
          setWorkDirInput(message.work_dir || "");
          document.title = message.title ? `${message.title} · CSGHub Lite` : "CSGHub Lite";
          return;
        }

        if (message.type === "exit") {
          exitSeenRef.current = true;
          setState("exited");
          setExitCode(typeof message.exit_code === "number" ? message.exit_code : 0);
          if (message.error) {
            setError(message.error);
          }
          terminal.writeln("");
          terminal.writeln(`\x1b[90m[${t("aiApps.shellExitNotice", String(message.exit_code ?? 0))}]\x1b[0m`);
          return;
        }

        return;
      }

      const chunk = event.data instanceof ArrayBuffer
        ? new Uint8Array(event.data)
        : new Uint8Array();
      if (chunk.byteLength > 0) {
        terminal.write(chunk, () => {
          scheduleFollowOutput();
        });
      }
    };

    ws.onerror = () => {
      if (!exitSeenRef.current) {
        setError(t("aiApps.shellConnectionFailed"));
      }
    };

    ws.onclose = () => {
      if (!exitSeenRef.current) {
        setState("disconnected");
      }
    };

    window.addEventListener("resize", resizeHandler);
    scheduleResize();

    return () => {
      window.removeEventListener("resize", resizeHandler);
      document.removeEventListener("visibilitychange", visibilityHandler);
      terminalContainer.removeEventListener("mousedown", pointerFocusHandler);
      resizeObserver?.disconnect();
      if (resizeFrame !== null) {
        window.cancelAnimationFrame(resizeFrame);
      }
      if (followOutputFrame !== null) {
        window.cancelAnimationFrame(followOutputFrame);
      }
      for (const timer of resizeTimers) {
        window.clearTimeout(timer);
      }
      xtermCompatDisposable?.dispose();
      binaryDisposable.dispose();
      inputDisposable.dispose();
      ws.close();
      terminal.dispose();
      if (terminalRef.current === terminal) {
        terminalRef.current = null;
      }
    };
  }, [sessionId]);

  useEffect(() => {
    if (!shellAppsWithModelSwitch.has(appId)) {
      setModels([]);
      setModelsLoading(false);
      return;
    }

    let disposed = false;
    setModelsLoading(true);

    getTags({ refresh: true })
      .then((items) => {
        if (disposed) return;
        setModels(normalizeShellModels(items));
      })
      .catch(() => {
        if (disposed) return;
        setModels([]);
      })
      .finally(() => {
        if (!disposed) {
          setModelsLoading(false);
        }
      });

    return () => {
      disposed = true;
    };
  }, [appId]);

  useEffect(() => {
    if (!shellAppsWithModelSwitch.has(appId)) {
      setSelectedModel("");
      return;
    }

    setSelectedModel((current) => {
      if (modelId && models.some((item) => item.model === modelId)) {
        const model = models.find((item) => item.model === modelId);
        return model ? shellModelKey(model) : modelId;
      }
      if (current && models.some((item) => shellModelKey(item) === current)) {
        return current;
      }
      return models[0] ? shellModelKey(models[0]) : "";
    });
  }, [appId, modelId, models]);

  const shellTitle = title || t("aiApps.shellTitle");
  const statusLabel = state === "connected"
    ? t("aiApps.shellConnected")
    : state === "exited"
      ? t("aiApps.shellExited")
      : state === "disconnected"
        ? t("aiApps.shellDisconnected")
        : t("aiApps.shellConnecting");
  const statusClass = state === "connected"
    ? "bg-emerald-500/15 text-emerald-300 border-emerald-500/30"
    : state === "exited"
      ? "bg-slate-500/15 text-slate-300 border-slate-500/30"
      : "bg-amber-500/15 text-amber-200 border-amber-500/30";

  const isOpenCodeShell = appId === "open-code";
  const canSwitchShellModel = shellAppsWithModelSwitch.has(appId);
  const canSwitchShellWorkDir = shellAppsWithWorkDirSwitch.has(appId);
  const trimmedWorkDir = workDirInput.trim();
  const selectedModelParts = selectedModel ? parseShellModelKey(selectedModel) : null;
  const currentModelKey = modelId
    ? shellModelKey(models.find((item) => item.model === modelId) || { model: modelId, source: "local" })
    : "";
  const modelChanged = canSwitchShellModel && selectedModel !== currentModelKey;
  const workDirChanged = trimmedWorkDir !== workDir;
  const launchConfigChanged = modelChanged || workDirChanged;
  const canApplyLaunchConfig = canSwitchShellModel || canSwitchShellWorkDir;
  const applyLaunchConfigDisabled = !canApplyLaunchConfig ||
    applyingLaunchConfig ||
    (canSwitchShellWorkDir && !trimmedWorkDir) ||
    (canSwitchShellModel && (modelsLoading || !selectedModel)) ||
    !launchConfigChanged;

  const handleApplyLaunchConfig = async () => {
    if (!canApplyLaunchConfig || applyLaunchConfigDisabled) {
      return;
    }

    setApplyingLaunchConfig(true);
    setError("");
    try {
      const requestedModel = canSwitchShellModel
        ? selectedModelParts?.model
        : modelId || undefined;
      const { url } = await openAIApp(appId || claudeCodeAppId, requestedModel, trimmedWorkDir, selectedModelParts?.source);
      await closeShellSession(sessionId);
      location.replace(url);
      return;
    } catch (err) {
      setError((err as Error).message || t("aiApps.openFailed"));
    } finally {
      setApplyingLaunchConfig(false);
    }
  };

  const handleCloseWindow = () => {
    void closeShellSession(sessionId);
    window.close();
    window.setTimeout(() => {
      if (!window.closed) {
        location.href = "/ai-apps";
      }
    }, 50);
  };

  return (
    <div class="absolute inset-0 bg-slate-950 text-slate-100 flex flex-col">
      <div class="absolute top-0 left-0 right-0 z-10 bg-slate-950/95 backdrop-blur border-b border-slate-800 flex flex-col shadow-sm">
        <div class="px-5 py-4 flex items-center justify-between gap-4">
        <div class="min-w-0">
          <div class="flex items-center gap-3 flex-wrap">
            <h1 class="text-base font-semibold text-white truncate">{shellTitle}</h1>
            <span class={`inline-flex items-center rounded-full border px-2.5 py-1 text-xs font-medium ${statusClass}`}>
              {statusLabel}
            </span>
          </div>
          <div class="mt-1 flex items-center gap-3 flex-wrap text-xs text-slate-400">
            {appId && <span>{appId}</span>}
            {!canSwitchShellModel && modelId && <span>{t("aiApps.shellModel")}: {modelId}</span>}
            {!canSwitchShellWorkDir && workDir && <span>{t("aiApps.shellDirectory")}: {workDir}</span>}
            {state === "exited" && exitCode !== null && <span>{t("aiApps.shellExitCode", String(exitCode))}</span>}
          </div>
        </div>
        <div class="flex items-center gap-2 flex-wrap justify-end">
          {canApplyLaunchConfig && (
            <>
              {canSwitchShellModel && (
                <div class="relative min-w-[260px] max-w-[340px]">
                  <select
                    value={selectedModel}
                    onChange={(e) => setSelectedModel((e.currentTarget as HTMLSelectElement).value)}
                    disabled={modelsLoading || applyingLaunchConfig || models.length === 0}
                    class={`appearance-none w-full rounded-lg border bg-slate-900 pl-3 pr-9 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent ${
                      modelsLoading || applyingLaunchConfig || models.length === 0
                        ? "border-slate-700 text-slate-500"
                        : "border-slate-700 text-slate-100"
                    }`}
                    aria-label={t("aiApps.model")}
                  >
                    {modelsLoading ? (
                      <option value="">{t("aiApps.modelLoading")}</option>
                    ) : models.length === 0 ? (
                      <option value="">{t("aiApps.modelDefault")}</option>
                    ) : (
                      models.map((model) => (
                        <option key={shellModelKey(model)} value={shellModelKey(model)}>
                          {formatShellModelLabel(model)}
                        </option>
                      ))
                    )}
                  </select>
                  <svg class="absolute right-3 top-1/2 -translate-y-1/2 w-4 h-4 text-slate-400 pointer-events-none" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
                  </svg>
                </div>
              )}
              <input
                type="text"
                value={workDirInput}
                onInput={(e) => setWorkDirInput((e.currentTarget as HTMLInputElement).value)}
                placeholder={t("aiApps.shellDirectoryPlaceholder")}
                disabled={applyingLaunchConfig}
                class={`min-w-[300px] max-w-[420px] rounded-lg border bg-slate-900 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent ${
                  applyingLaunchConfig
                    ? "border-slate-700 text-slate-500"
                    : "border-slate-700 text-slate-100 placeholder:text-slate-500"
                }`}
                aria-label={t("aiApps.shellDirectory")}
              />
              <button
                onClick={handleApplyLaunchConfig}
                disabled={applyLaunchConfigDisabled}
                class={`rounded-lg border px-3 py-2 text-sm transition-colors ${
                  applyLaunchConfigDisabled
                    ? "border-slate-800 text-slate-500 cursor-not-allowed"
                    : "border-indigo-500/40 text-indigo-200 hover:bg-indigo-500/10"
                }`}
              >
                {applyingLaunchConfig ? t("aiApps.shellApplyingLaunch") : t("aiApps.shellApplyLaunch")}
              </button>
            </>
          )}
          {!isOpenCodeShell && (
            <>
              <button
                onClick={() => location.reload()}
                class="rounded-lg border border-slate-700 px-3 py-2 text-sm text-slate-200 hover:bg-slate-900 transition-colors"
              >
                {t("aiApps.shellReconnect")}
              </button>
              <button
                onClick={handleCloseWindow}
                class="rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white hover:bg-indigo-500 transition-colors"
              >
                {t("aiApps.close")}
              </button>
            </>
          )}
        </div>
      </div>

      {error && (
        <div class="px-5 py-3 border-b border-red-900/40 bg-red-950/40 text-sm text-red-200">
          {error}
        </div>
      )}
      </div>

      {!sessionId ? (
        <div class="absolute inset-0 flex items-center justify-center px-6 text-sm text-slate-400">
          {t("aiApps.shellSessionMissing")}
        </div>
      ) : (
        <div class="absolute inset-0 pt-[80px] pb-[50px] px-4 flex flex-col z-0">
          <div ref={containerRef} class="flex-1 min-h-0 w-full overflow-hidden" />
        </div>
      )}
    </div>
  );
}
