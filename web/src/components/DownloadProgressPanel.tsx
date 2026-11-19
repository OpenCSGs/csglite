import { locale, t } from "../i18n";
import { formatSpeed } from "../utils/format";
import type { DownloadTask } from "../downloads";

export function DownloadProgressCell({
  task,
  completeWhenMissing = false,
}: {
  task?: DownloadTask;
  completeWhenMissing?: boolean;
}) {
  void locale.value;
  if (!task) {
    if (completeWhenMissing) {
      return <span class="text-xs font-medium text-emerald-600">{t("downloads.done")}</span>;
    }
    return <span class="text-xs text-gray-300">{t("downloads.none")}</span>;
  }
  if (isDownloadComplete(task)) {
    return <span class="text-xs font-medium text-emerald-600">{t("downloads.done")}</span>;
  }
  if (task.status === "paused") {
    return <span class="block max-w-full truncate text-xs font-medium text-amber-600">{t("downloads.interrupted")}</span>;
  }
  if (task.status === "downloading" || task.status === "queued") {
    const speed = formatSpeed(task.speedBytesPerSec);
    return (
      <div
        class="w-20 min-w-0 max-w-20"
        title={[speed, t("downloads.downloadingPercent", displayPercent(task))].filter(Boolean).join(" · ")}
      >
        <div class="mb-1 text-right text-xs text-gray-400 tabular-nums">{displayPercent(task)}%</div>
        <ProgressBar task={task} />
        <div class="mt-1 h-4 truncate text-right text-xs text-gray-400 tabular-nums">{speed}</div>
      </div>
    );
  }
  return <DownloadStatusCell task={task} completeWhenMissing={completeWhenMissing} />;
}

export function DownloadStatusCell({ task, completeWhenMissing = false }: { task?: DownloadTask; completeWhenMissing?: boolean }) {
  void locale.value;
  if (!task) {
    if (completeWhenMissing) {
      return <span class="text-xs font-medium text-emerald-600">{t("downloads.done")}</span>;
    }
    return <span class="text-xs text-gray-300">{t("downloads.none")}</span>;
  }

  const label = downloadStatusLabel(task, false);
  const isError = task.status === "error";
  const reason = task.error || task.statusText;
  const color = isError ? "text-red-600" : task.kind === "dataset" ? "text-purple-600" : "text-indigo-600";

  if (isError && reason) {
    return (
      <button
        type="button"
        title={reason}
        onClick={() => window.alert(`${t("downloads.errorDetails")}\n\n${reason}`)}
        class={`max-w-full truncate text-left text-xs font-medium underline decoration-dotted underline-offset-2 ${color}`}
      >
        {label}
      </button>
    );
  }

  return (
    <span title={label} class={`block max-w-full truncate text-xs font-medium ${color}`}>
      {label}
    </span>
  );
}

function ProgressBar({ task }: { task: DownloadTask }) {
  const color = task.status === "error" ? "bg-red-500" : task.kind === "dataset" ? "bg-purple-500" : "bg-indigo-500";
  return (
    <div class="w-full h-1.5 bg-gray-200 rounded-full overflow-hidden">
      <div class={`h-full rounded-full transition-all duration-300 ${color}`} style={{ width: `${Math.max(displayPercent(task) || 0, task.status === "downloading" ? 3 : 0)}%` }} />
    </div>
  );
}

function isDownloadComplete(task: DownloadTask): boolean {
  return task.status === "success" || (task.status !== "error" && task.percent >= 100 && !task.error);
}

function displayPercent(task: DownloadTask): number {
  if (task.status === "queued") return 0;
  if (task.status === "error") return Math.min(task.percent, 99);
  return isDownloadComplete(task) ? 100 : task.percent;
}

function downloadStatusLabel(task: DownloadTask, includePercent: boolean): string {
  if (isDownloadComplete(task)) return t("downloads.done");
  if (task.status === "error") return t("downloads.failed");
  if (task.status === "paused") return t("downloads.interrupted");
  if (task.status === "queued") return t("downloads.queued");
  if (includePercent && task.percent > 0) return t("downloads.downloadingPercent", task.percent);
  return t("downloads.downloading");
}

