import { computed, signal } from "@preact/signals";
import { pullDataset, pullModel } from "./api/client";
import type { PullProgress } from "./api/client";

export type DownloadKind = "model" | "dataset";
export type DownloadStatus = "downloading" | "paused" | "success" | "error";

export interface DownloadTask {
  key: string;
  kind: DownloadKind;
  name: string;
  status: DownloadStatus;
  percent: number;
  statusText: string;
  currentFile?: string;
  completedBytes: number;
  totalBytes: number;
  error?: string;
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
  files: Record<string, { completed: number; total: number }>;
}

const STORAGE_KEY = "csghub-lite-download-tasks";
const activeControllers = new Map<string, AbortController>();

function taskKey(kind: DownloadKind, name: string): string {
  return `${kind}:${name}`;
}

function nowISO(): string {
  return new Date().toISOString();
}

function normalizeTask(raw: any): DownloadTask | null {
  if (!raw || (raw.kind !== "model" && raw.kind !== "dataset") || typeof raw.name !== "string" || !raw.name.trim()) {
    return null;
  }
  const status: DownloadStatus = raw.status === "success" || raw.status === "error" ? raw.status : "paused";
  return {
    key: taskKey(raw.kind, raw.name),
    kind: raw.kind,
    name: raw.name,
    status,
    percent: Math.max(0, Math.min(100, Number(raw.percent) || 0)),
    statusText: status === "paused" ? "interrupted" : String(raw.statusText || status),
    currentFile: typeof raw.currentFile === "string" ? raw.currentFile : undefined,
    completedBytes: Math.max(0, Number(raw.completedBytes) || 0),
    totalBytes: Math.max(0, Number(raw.totalBytes) || 0),
    error: typeof raw.error === "string" ? raw.error : undefined,
    createdAt: typeof raw.createdAt === "string" ? raw.createdAt : nowISO(),
    updatedAt: typeof raw.updatedAt === "string" ? raw.updatedAt : nowISO(),
    completedAt: typeof raw.completedAt === "string" ? raw.completedAt : undefined,
    files: raw.files && typeof raw.files === "object" ? raw.files : {},
  };
}

function loadTasks(): Record<string, DownloadTask> {
  try {
    const parsed = JSON.parse(localStorage.getItem(STORAGE_KEY) || "[]");
    const list = Array.isArray(parsed) ? parsed : Object.values(parsed || {});
    const tasks: Record<string, DownloadTask> = {};
    for (const item of list) {
      const task = normalizeTask(item);
      if (task?.status === "success") continue;
      if (task) tasks[task.key] = task;
    }
    return tasks;
  } catch {
    return {};
  }
}

function persistTasks(value: Record<string, DownloadTask>) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(Object.values(value)));
  } catch {
    /* ignore storage failures */
  }
}

function setTask(task: DownloadTask) {
  downloadTasks.value = { ...downloadTasks.value, [task.key]: task };
  persistTasks(downloadTasks.value);
}

function removeTask(key: string) {
  const next = { ...downloadTasks.value };
  delete next[key];
  downloadTasks.value = next;
  persistTasks(next);
}

function aggregateFiles(files: Record<string, { completed: number; total: number }>): { completed: number; total: number } {
  let completed = 0;
  let total = 0;
  for (const file of Object.values(files)) {
    completed += Math.max(0, file.completed || 0);
    total += Math.max(0, file.total || 0);
  }
  return { completed, total };
}

function applyProgress(task: DownloadTask, p: PullProgress): DownloadTask {
  const files = { ...task.files };
  const fileKey = p.digest || task.currentFile || "download";
  if (p.total && p.total > 0) {
    files[fileKey] = { completed: Math.max(0, p.completed || 0), total: p.total };
  }
  const aggregate = aggregateFiles(files);
  const hasRepositoryProgress = typeof p.total_bytes === "number" && p.total_bytes > 0;
  const totalBytes = hasRepositoryProgress ? Math.max(0, p.total_bytes || 0) : aggregate.total || task.totalBytes;
  const completedBytes = hasRepositoryProgress ? Math.max(0, p.completed_bytes || 0) : aggregate.total ? aggregate.completed : task.completedBytes;

  // Prefer repository-level bytes from the API so multi-file downloads do not
  // jump based on the current file's progress.
  let percent = totalBytes > 0 ? Math.min(100, Math.round((completedBytes / totalBytes) * 100)) : task.percent;
  // Keep the task open until the backend sends the final success event.
  if (percent >= 100 && completedBytes < totalBytes) {
    percent = 99;
  }

  return {
    ...task,
    status: "downloading",
    statusText: p.status || task.statusText,
    currentFile: p.digest || task.currentFile,
    percent,
    completedBytes,
    totalBytes,
    files,
    updatedAt: nowISO(),
  };
}

function isInterruptedDownloadError(err: any): boolean {
  const name = String(err?.name || "");
  const message = String(err?.message || err || "").toLowerCase();
  return name === "AbortError" ||
    message.includes("input stream") ||
    message.includes("network") ||
    message.includes("failed to fetch") ||
    message.includes("load failed") ||
    message.includes("context canceled");
}

export const downloadTasks = signal<Record<string, DownloadTask>>(loadTasks());
export const downloadCompletionVersion = signal(0);
export const downloadTaskList = computed(() =>
  Object.values(downloadTasks.value).sort((a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime())
);
export const activeDownload = computed(() => downloadTaskList.value.find((task) => task.status === "downloading"));
export const hasActiveDownload = computed(() => !!activeDownload.value);

export function getDownloadTask(kind: DownloadKind, name: string): DownloadTask | undefined {
  return downloadTasks.value[taskKey(kind, name)];
}

export function getDownloadTasks(kind?: DownloadKind): DownloadTask[] {
  return downloadTaskList.value.filter((task) => !kind || task.kind === kind);
}

export function clearDownloadTask(task: DownloadTask) {
  if (task.status === "downloading") {
    // 先暂停再清除
    pauseDownload(task.kind, task.name);
  }
  removeTask(task.key);
}

export function pauseDownload(kind: DownloadKind, name: string) {
  const key = taskKey(kind, name);
  const controller = activeControllers.get(key);
  if (controller) {
    controller.abort();
    activeControllers.delete(key);
  }

  const current = downloadTasks.value[key];
  if (current && current.status === "downloading") {
    setTask({
      ...current,
      status: "paused",
      statusText: "paused",
      updatedAt: nowISO(),
    });
  }
}

export function startDownload(kind: DownloadKind, name: string, onComplete?: () => void): boolean {
  const key = taskKey(kind, name);
  const existingActive = activeDownload.value;
  if (existingActive && existingActive.key !== key) {
    return false;
  }
  if (activeControllers.has(key)) {
    return true;
  }

  const startedAt = nowISO();
  const base = downloadTasks.value[key];
  const resumableBase = base?.status === "success" ? undefined : base;
  const task: DownloadTask = {
    key,
    kind,
    name,
    status: "downloading",
    percent: resumableBase?.percent || 0,
    statusText: base?.status === "paused" || base?.status === "error" ? "resuming" : "downloading",
    currentFile: resumableBase?.currentFile,
    completedBytes: resumableBase?.completedBytes || 0,
    totalBytes: resumableBase?.totalBytes || 0,
    createdAt: base?.createdAt || startedAt,
    updatedAt: startedAt,
    files: resumableBase?.files || {},
  };
  setTask(task);

  const controller = new AbortController();
  activeControllers.set(key, controller);
  const pull = kind === "model" ? pullModel : pullDataset;

  pull(
    name,
    (progress) => {
      if (progress.status === "success") {
        const completed = nowISO();
        setTask({
          ...downloadTasks.value[key],
          status: "success",
          statusText: "success",
          percent: 100,
          error: undefined,
          updatedAt: completed,
          completedAt: completed,
        });
        onComplete?.();
        removeTask(key);
        downloadCompletionVersion.value += 1;
        return;
      }
      if (progress.status.startsWith("error")) {
        setTask({
          ...downloadTasks.value[key],
          status: "error",
          statusText: progress.status,
          error: progress.status.replace(/^error:\s*/, ""),
          updatedAt: nowISO(),
        });
        return;
      }
      setTask(applyProgress(downloadTasks.value[key] || task, progress));
    },
    controller.signal
  ).catch((err: any) => {
    const current = downloadTasks.value[key] || task;
    if (current.status === "success") return;
    if (isInterruptedDownloadError(err)) {
      setTask({
        ...current,
        status: "paused",
        statusText: "interrupted",
        error: undefined,
        updatedAt: nowISO(),
      });
      return;
    }
    setTask({
      ...current,
      status: "error",
      statusText: "error",
      error: err?.message || "download failed",
      updatedAt: nowISO(),
    });
  }).finally(() => {
    activeControllers.delete(key);
  });

  return true;
}
