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

export function DownloadTableCell({ task, onComplete }: { task?: DownloadTask; onComplete?: () => void }) {
  void locale.value;
  if (!task) {
    return <span class="text-xs text-gray-300">{t("downloads.none")}</span>;
  }
  const isComplete = isDownloadComplete(task);
  const canResume = task.status === "paused" || task.status === "error";
  const isDownloading = task.status === "downloading";
  const statusLabel = downloadStatusLabel(task, true);
  return (
    <div class="w-full min-w-0 max-w-full">
      <div class="flex flex-wrap items-center gap-x-2 gap-y-1 mb-1">
        {isComplete ? (
          <span class="ml-auto shrink-0 text-xs text-gray-400">{displayPercent(task)}%</span>
        ) : (
          <span title={statusLabel} class={`min-w-0 flex-1 truncate text-xs font-medium ${task.status === "error" ? "text-red-600" : task.kind === "dataset" ? "text-purple-600" : "text-indigo-600"}`}>
            {statusLabel}
          </span>
        )}
        {!isComplete && canResume && (
          <button
            onClick={() => startDownload(task.kind, task.name, onComplete)}
            class="shrink-0 text-xs text-indigo-600 hover:text-indigo-700 font-medium"
          >
            {t("downloads.resume")}
          </button>
        )}
        {!isComplete && isDownloading && (
          <button
            onClick={() => pauseDownload(task.kind, task.name)}
            class="shrink-0 text-xs text-indigo-600 hover:text-indigo-700 font-medium"
          >
            {t("downloads.pause")}
          </button>
        )}
        {!isComplete && !isDownloading && (
          <button
            onClick={() => clearDownloadTask(task)}
            class="shrink-0 text-xs text-gray-400 hover:text-gray-600"
          >
            {t("downloads.clear")}
          </button>
        )}
      </div>
      <ProgressBar task={task} />
      {task.error && <div class="mt-1 text-xs text-red-600 truncate" title={task.error}>{task.error}</div>}
    </div>
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
  return task.status === "success" || (task.percent >= 100 && !task.error);
}

function displayPercent(task: DownloadTask): number {
  return isDownloadComplete(task) ? 100 : task.percent;
}

function downloadStatusLabel(task: DownloadTask, includePercent: boolean): string {
  if (isDownloadComplete(task)) return t("downloads.done");
  if (task.status === "error") return t("downloads.failed");
  if (task.status === "paused") return t("downloads.interrupted");
  if (includePercent && task.percent > 0) return t("downloads.downloadingPercent", task.percent);
  return t("downloads.downloading");
}

