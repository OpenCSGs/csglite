import { locale, t } from "../i18n";
import { clearDownloadTask, pauseDownload, startDownload } from "../downloads";
import type { DownloadTask } from "../downloads";

export function DownloadInlineStatus({ task }: { task: DownloadTask }) {
  const isComplete = isDownloadComplete(task);
  const label = downloadStatusLabel(task, false);
  return (
    <div class="w-full min-w-0">
      <div class="flex items-center justify-between gap-2 mb-1">
        {!isComplete && (
          <span title={label} class={`min-w-0 truncate text-xs font-medium ${task.status === "error" ? "text-red-600" : task.kind === "dataset" ? "text-purple-600" : "text-indigo-600"}`}>
            {label}
          </span>
        )}
        {(isComplete || task.percent > 0) && <span class="ml-auto shrink-0 text-xs text-gray-400">{displayPercent(task)}%</span>}
      </div>
      <ProgressBar task={task} />
    </div>
  );
}

export function DownloadTableCell({ task, onComplete, showActions = true }: { task?: DownloadTask; onComplete?: () => void; showActions?: boolean }) {
  void locale.value;
  if (!task) {
    return <span class="text-xs text-gray-300">{t("downloads.none")}</span>;
  }
  const isComplete = isDownloadComplete(task);
  const canResume = task.status === "paused" || task.status === "error";
  const isDownloading = task.status === "downloading";
  return (
    <div class="w-full min-w-0 max-w-full">
      <div class="flex flex-wrap items-center gap-x-2 gap-y-1 mb-1">
        <span class="ml-auto shrink-0 text-xs text-gray-400">{displayPercent(task)}%</span>
        {showActions && !isComplete && canResume && (
          <button
            onClick={() => startDownload(task.kind, task.name, onComplete)}
            class="shrink-0 text-xs text-indigo-600 hover:text-indigo-700 font-medium"
          >
            {t("downloads.resume")}
          </button>
        )}
        {showActions && !isComplete && isDownloading && (
          <button
            onClick={() => pauseDownload(task.kind, task.name)}
            class="shrink-0 text-xs text-indigo-600 hover:text-indigo-700 font-medium"
          >
            {t("downloads.pause")}
          </button>
        )}
        {showActions && !isComplete && !isDownloading && (
          <button
            onClick={() => clearDownloadTask(task)}
            class="shrink-0 text-xs text-gray-400 hover:text-gray-600"
          >
            {t("downloads.clear")}
          </button>
        )}
      </div>
      <ProgressBar task={task} />
    </div>
  );
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

