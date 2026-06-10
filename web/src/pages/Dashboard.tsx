import { useEffect, useRef } from "preact/hooks";
import { signal } from "@preact/signals";
import { getPs, getTags, getSystemInfo, stopModel, streamLogs } from "../api/client";
import type { RunningModel, ModelInfo, SystemInfo } from "../api/client";
import { ApiInfoDialog } from "../components/ApiInfoDialog";
import { t, locale } from "../i18n";

const runningModels = signal<RunningModel[]>([]);
const allModels = signal<ModelInfo[]>([]);
const sysInfo = signal<SystemInfo | null>(null);
const logs = signal<string[]>([]);
const streaming = signal(true);
const apiInfoModel = signal<string>("");

function isEmbeddingModel(model?: Pick<ModelInfo, "pipeline_tag" | "category"> | null): boolean {
  const pipelineTag = (model?.pipeline_tag || "").toLowerCase();
  const category = (model?.category || "").toLowerCase();
  return category === "embedding" || ["feature-extraction", "sentence-similarity", "text-embedding", "embedding"].includes(pipelineTag);
}

function isASRModel(model?: Pick<ModelInfo, "pipeline_tag" | "input_modalities" | "output_modalities"> | null): boolean {
  const pipelineTag = (model?.pipeline_tag || "").toLowerCase();
  return pipelineTag === "automatic-speech-recognition" ||
    Boolean(model?.input_modalities?.includes("audio")) ||
    Boolean(model?.output_modalities?.includes("transcription"));
}

export function Dashboard() {
  const logRef = useRef<HTMLDivElement>(null);
  void locale.value;

  useEffect(() => {
    const load = () => {
      getPs().then((m) => (runningModels.value = m)).catch(() => {});
      getTags().then((m) => (allModels.value = m)).catch(() => {});
      getSystemInfo().then((s) => (sysInfo.value = s)).catch(() => {});
    };
    load();
    const iv = setInterval(load, 3000);

    const ac = new AbortController();
    streamLogs((line) => {
      logs.value = [...logs.value.slice(-200), line];
      if (logRef.current && streaming.value) {
        logRef.current.scrollTop = logRef.current.scrollHeight;
      }
    }, ac.signal);

    return () => {
      clearInterval(iv);
      ac.abort();
    };
  }, []);

  const handleUnload = async (name: string) => {
    await stopModel(name);
    runningModels.value = runningModels.value.filter((m) => m.name !== name);
  };

  const sys = sysInfo.value;
  const cpuPct = sys ? Math.round(sys.cpu_usage * 100) / 100 : 0;
  const ramPct = sys ? Math.round((sys.ram_used / sys.ram_total) * 100) : 0;
  const gpuUsageAvailable = Boolean(sys?.gpu_usage_available);
  const gpuPct = sys && gpuUsageAvailable && sys.gpu_vram_total > 0 ? Math.round((sys.gpu_vram_used / sys.gpu_vram_total) * 100) : 0;
  const gpuValue = !sys
    ? "—"
    : sys.gpu_vram_total > 0
      ? gpuUsageAvailable
        ? `${fmtGB(sys.gpu_vram_used)} / ${fmtGB(sys.gpu_vram_total)} GB`
        : t("dash.totalOnly", fmtGB(sys.gpu_vram_total))
      : t("dash.na");
  const gpuDetail = [
    sys?.gpu_name || "",
    sys?.gpu_shared_memory ? t("dash.unifiedMemory") : "",
    sys && sys.gpu_vram_total > 0 && !gpuUsageAvailable ? t("dash.usageUnavailable") : "",
  ]
    .filter(Boolean)
    .join(" · ");
  const apiModelInfo = apiInfoModel.value
    ? allModels.value.find((m) => m.name === apiInfoModel.value || m.model === apiInfoModel.value)
    : undefined;

  return (
    <div class="p-8 max-w-6xl mx-auto space-y-6">
      {/* Resource Utilization */}
      <section class="bg-white rounded-xl border border-gray-200 p-6">
        <div class="flex items-center justify-between mb-6">
          <div class="flex items-center gap-2">
            <svg class="w-5 h-5 text-gray-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            </svg>
            <h2 class="text-lg font-bold text-gray-900">{t("dash.resource")}</h2>
          </div>
          <span class="text-sm text-gray-400">{t("dash.updates")}</span>
        </div>
        <div class="grid grid-cols-3 gap-8">
          <ResourceCard
            label={t("dash.cpu")}
            value={sys ? `${sys.cpu_cores} Cores` : "—"}
            detail={sys?.cpu_clock || ""}
            percent={cpuPct}
            usageAvailable
          />
          <ResourceCard
            label={t("dash.ram")}
            value={sys ? `${fmtGB(sys.ram_used)} / ${fmtGB(sys.ram_total)} GB` : "—"}
            detail={sys?.ram_info || ""}
            percent={ramPct}
            usageAvailable
          />
          <ResourceCard
            label={t("dash.gpu")}
            value={gpuValue}
            detail={gpuDetail}
            percent={gpuPct}
            usageAvailable={gpuUsageAvailable}
          />
        </div>
      </section>

      {/* Active Models */}
      <section class="bg-white rounded-xl border border-gray-200 p-6">
        <div class="flex items-center justify-between mb-4">
          <div class="flex items-center gap-2">
            <svg class="w-5 h-5 text-gray-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2z" />
            </svg>
            <h2 class="text-lg font-bold text-gray-900">{t("dash.active")}</h2>
          </div>
        </div>
        {runningModels.value.length === 0 ? (
          <p class="text-gray-400 text-sm py-4">{t("dash.noModels")}</p>
        ) : (
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-gray-100 text-left text-gray-500">
                <th class="pb-3 font-medium">{t("dash.modelName")}</th>
                <th class="pb-3 font-medium">{t("dash.expiresAt")}</th>
                <th class="pb-3 font-medium text-right">{t("dash.actions")}</th>
              </tr>
            </thead>
            <tbody>
              {runningModels.value.map((m) => (
                <tr key={m.name} class="border-b border-gray-50">
                  <td class="py-3">
                    <div class="font-medium text-gray-900">{m.name}</div>
                    <div class="text-xs text-gray-400">{t("dash.format")}: {m.format}</div>
                  </td>
                  <td class="py-3 text-gray-600">{new Date(m.expires_at).toLocaleTimeString()}</td>
                  <td class="py-3 text-right space-x-2">
                    <span
                      class="text-indigo-600 text-xs cursor-pointer hover:underline"
                      onClick={() => (apiInfoModel.value = m.name)}
                    >
                      {t("dash.apiInfo")}
                    </span>
                    <button
                      onClick={() => handleUnload(m.name)}
                      class="px-3 py-1 text-xs rounded border border-red-300 text-red-600 hover:bg-red-50 transition-colors"
                    >
                      {t("dash.unload")}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {/* Live Logs */}
      <section class="bg-white rounded-xl border border-gray-200 p-6">
        <div class="flex items-center justify-between mb-4">
          <div class="flex items-center gap-2">
            <svg class="w-5 h-5 text-gray-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
            </svg>
            <h2 class="text-lg font-bold text-gray-900">{t("dash.logs")}</h2>
          </div>
          <div class="flex items-center gap-3">
            <span class="flex items-center gap-1.5 text-xs">
              <span class={`w-2 h-2 rounded-full ${streaming.value ? "bg-green-400" : "bg-gray-300"}`} />
              {streaming.value ? t("dash.streaming") : t("dash.paused")}
            </span>
            <button
              onClick={() => (logs.value = [])}
              class="text-gray-400 hover:text-gray-600"
              title={t("dash.clear")}
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
              </svg>
            </button>
          </div>
        </div>
        <div
          ref={logRef}
          class="bg-gray-900 rounded-lg p-4 h-64 overflow-auto font-mono text-xs leading-5"
        >
          {logs.value.length === 0 ? (
            <span class="text-gray-500">{t("dash.waitLogs")}</span>
          ) : (
            logs.value.map((line, i) => <LogLine key={i} line={line} />)
          )}
        </div>
      </section>

      {/* API Info Modal */}
      {apiInfoModel.value && (
        <ApiInfoDialog
          model={apiInfoModel.value}
          pipelineTag={apiModelInfo?.pipeline_tag}
          isVision={apiModelInfo?.pipeline_tag === "image-text-to-text"}
          isEmbedding={isEmbeddingModel(apiModelInfo)}
          isASR={isASRModel(apiModelInfo)}
          onClose={() => (apiInfoModel.value = "")}
        />
      )}
    </div>
  );
}

function ResourceCard({
  label,
  value,
  detail,
  percent,
  usageAvailable = true,
}: {
  label: string;
  value: string;
  detail: string;
  percent: number;
  usageAvailable?: boolean;
}) {
  const circumference = 2 * Math.PI * 36;
  const offset = circumference - (percent / 100) * circumference;
  const color = !usageAvailable ? "#D1D5DB" : percent > 80 ? "#EF4444" : percent > 50 ? "#F59E0B" : "#6366F1";
  const percentLabel = usageAvailable ? `${percent}%` : "—";

  return (
    <div class="flex items-center gap-5">
      <div class="relative w-20 h-20 flex-shrink-0">
        <svg class="w-20 h-20 -rotate-90" viewBox="0 0 80 80">
          <circle cx="40" cy="40" r="36" fill="none" stroke="#E5E7EB" stroke-width="6" />
          <circle
            cx="40"
            cy="40"
            r="36"
            fill="none"
            stroke={color}
            stroke-width="6"
            stroke-dasharray={circumference}
            stroke-dashoffset={offset}
            stroke-linecap="round"
          />
        </svg>
        <span class="absolute inset-0 flex items-center justify-center text-sm font-semibold text-gray-700">
          {percentLabel}
        </span>
      </div>
      <div>
        <div class="text-xs text-gray-400 font-medium uppercase tracking-wide">{label}</div>
        <div class="text-base font-bold text-gray-900 mt-0.5">{value}</div>
        {detail && <div class="text-xs text-gray-400 mt-0.5">{detail}</div>}
      </div>
    </div>
  );
}

function LogLine({ line }: { line: string }) {
  let color = "text-gray-300";
  if (line.includes("INFO:")) color = "text-green-400";
  else if (line.includes("WARN:")) color = "text-yellow-400";
  else if (line.includes("ERROR:")) color = "text-red-400";
  else if (line.includes("REQUEST:")) color = "text-blue-400";
  return <div class={color}>{line}</div>;
}

function fmtGB(bytes: number): string {
  return (bytes / (1024 * 1024 * 1024)).toFixed(1);
}
