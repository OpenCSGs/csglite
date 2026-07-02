import { computed, signal } from "@preact/signals";
import {
  cancelPullJob,
  createDatasetPullJob,
  createPullJob,
  getPullJob,
} from "./api/client";
import type { PullJob, PullProgress } from "./api/client";

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
  jobId?: string;
  quants?: string[];
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
  files: Record<string, { completed: number; total: number }>;
}

const STORAGE_KEY = "csghub-lite-download-tasks";
const POLL_MS = 1000;
const activePollers = new Map<string, number>();
const completionCallbacks = new Map<string, () => void>();

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
  const status: DownloadStatus =
    raw.status === "success" || raw.status === "error" || raw.status === "downloading"
      ? raw.status
      : "paused";
  return {
    key: taskKey(raw.kind, raw.name),
    kind: raw.kind,
    name: raw.name,
    status,
    percent: Math.max(0, Math.min(100, Number(raw.percent) || 0)),
    statusText:
      status === "paused" && !raw.jobId
        ? "interrupted"
        : String(raw.statusText || status),
    currentFile: typeof raw.currentFile === "string" ? raw.currentFile : undefined,
    completedBytes: Math.max(0, Number(raw.completedBytes) || 0),
    totalBytes: Math.max(0, Number(raw.totalBytes) || 0),
    error: typeof raw.error === "string" ? raw.error : undefined,
    jobId: typeof raw.jobId === "string" ? raw.jobId : undefined,
    quants: Array.isArray(raw.quants)
      ? raw.quants.map((value: any) => String(value || "").trim()).filter(Boolean)
      : undefined,
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
  const completedBytes = hasRepositoryProgress
    ? Math.max(0, p.completed_bytes || 0)
    : aggregate.total
      ? aggregate.completed
      : task.completedBytes;

  let percent = totalBytes > 0 ? Math.min(100, Math.round((completedBytes / totalBytes) * 100)) : task.percent;
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

function applyJobToTask(task: DownloadTask, job: PullJob): DownloadTask {
  const progress = job.progress || { status: job.status };
  if (job.status === "succeeded" || progress.status === "success") {
    const completed = job.completed_at || nowISO();
    return {
      ...task,
      jobId: job.id,
      status: "success",
      statusText: "success",
      percent: 100,
      error: undefined,
      updatedAt: completed,
      completedAt: completed,
    };
  }
  if (job.status === "failed") {
    return {
      ...task,
      jobId: job.id,
      status: "error",
      statusText: progress.status || "error",
      percent: Math.min(task.percent, 99),
      error: job.error || progress.status.replace(/^error:\s*/, "") || "download failed",
      updatedAt: nowISO(),
    };
  }
  if (job.status === "cancelled") {
    return {
      ...task,
      jobId: job.id,
      status: "paused",
      statusText: "paused",
      error: undefined,
      updatedAt: nowISO(),
    };
  }
  return applyProgress({ ...task, jobId: job.id }, progress);
}

function stopPolling(key: string) {
  const timer = activePollers.get(key);
  if (timer !== undefined) {
    clearInterval(timer);
    activePollers.delete(key);
  }
}

async function pollJob(key: string) {
  const task = downloadTasks.value[key];
  if (!task?.jobId) {
    stopPolling(key);
    return;
  }
  try {
    const job = await getPullJob(task.jobId);
    const updated = applyJobToTask(task, job);
    if (job.status === "succeeded") {
      setTask(updated);
      stopPolling(key);
      completionCallbacks.get(key)?.();
      completionCallbacks.delete(key);
      removeTask(key);
      downloadCompletionVersion.value += 1;
      return;
    }
    if (job.status === "failed") {
      setTask(updated);
      stopPolling(key);
      completionCallbacks.delete(key);
      return;
    }
    if (job.status === "cancelled") {
      setTask(updated);
      stopPolling(key);
      completionCallbacks.delete(key);
      return;
    }
    setTask(updated);
  } catch {
    stopPolling(key);
  }
}

function startPolling(key: string) {
  if (activePollers.has(key)) return;
  const timer = window.setInterval(() => void pollJob(key), POLL_MS);
  activePollers.set(key, timer);
  void pollJob(key);
}

async function syncDownloadsFromServer() {
  for (const task of Object.values(downloadTasks.value)) {
    if (task.jobId) {
      try {
        const job = await getPullJob(task.jobId);
        if (job.status === "running" || job.status === "queued") {
          setTask(applyJobToTask({ ...task, status: "downloading" }, job));
          startPolling(task.key);
          continue;
        }
        if (job.status === "succeeded") {
          removeTask(task.key);
          downloadCompletionVersion.value += 1;
          continue;
        }
        setTask(applyJobToTask(task, job));
      } catch {
        /* job no longer exists */
      }
      continue;
    }

    const autoResume =
      task.status === "downloading" ||
      (task.status === "paused" && task.statusText === "interrupted");
    if (!autoResume) continue;

    try {
      const job =
        task.kind === "model" ? await createPullJob(task.name, { quants: task.quants }) : await createDatasetPullJob(task.name);
      if (job.status === "running" || job.status === "queued") {
        setTask(applyJobToTask({ ...task, status: "downloading" }, job));
        startPolling(task.key);
      }
    } catch {
      /* backend may not support pull jobs yet */
    }
  }
}

export const downloadTasks = signal<Record<string, DownloadTask>>(loadTasks());
export const downloadCompletionVersion = signal(0);
export const downloadTaskList = computed(() =>
  Object.values(downloadTasks.value).sort((a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime())
);
export const activeDownload = computed(() => downloadTaskList.value.find((task) => task.status === "downloading"));
export const hasActiveDownload = computed(() => !!activeDownload.value);

void syncDownloadsFromServer();

export function getDownloadTask(kind: DownloadKind, name: string): DownloadTask | undefined {
  return downloadTasks.value[taskKey(kind, name)];
}

export function getDownloadTasks(kind?: DownloadKind): DownloadTask[] {
  return downloadTaskList.value.filter((task) => !kind || task.kind === kind);
}

export function clearDownloadTask(task: DownloadTask) {
  if (task.status === "downloading") {
    pauseDownload(task.kind, task.name);
  }
  removeTask(task.key);
}

export function pauseDownload(kind: DownloadKind, name: string) {
  const key = taskKey(kind, name);
  stopPolling(key);
  completionCallbacks.delete(key);

  const current = downloadTasks.value[key];
  if (current?.jobId) {
    void cancelPullJob(current.jobId).catch(() => {});
  }
  if (current && current.status === "downloading") {
    setTask({
      ...current,
      status: "paused",
      statusText: "paused",
      updatedAt: nowISO(),
    });
  }
}

export function startDownload(kind: DownloadKind, name: string, onComplete?: () => void, options?: { quants?: string[] }): boolean {
  const key = taskKey(kind, name);
  const existingActive = activeDownload.value;
  if (existingActive && existingActive.key !== key) {
    return false;
  }
  if (activePollers.has(key)) {
    return true;
  }

  const startedAt = nowISO();
  const base = downloadTasks.value[key];
  const resumableBase = base?.status === "success" ? undefined : base;
  const quants = kind === "model"
    ? options?.quants?.map((value) => value.trim()).filter(Boolean) || resumableBase?.quants
    : undefined;
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
    jobId: resumableBase?.jobId,
    quants,
    createdAt: base?.createdAt || startedAt,
    updatedAt: startedAt,
    files: resumableBase?.files || {},
  };
  setTask(task);
  if (onComplete) {
    completionCallbacks.set(key, onComplete);
  }

  void (async () => {
    try {
      if (task.jobId) {
        const existing = await getPullJob(task.jobId);
        if (existing.status === "running" || existing.status === "queued") {
          setTask(applyJobToTask(downloadTasks.value[key] || task, existing));
          startPolling(key);
          return;
        }
        if (existing.status === "succeeded") {
          const updated = applyJobToTask(downloadTasks.value[key] || task, existing);
          setTask(updated);
          completionCallbacks.get(key)?.();
          completionCallbacks.delete(key);
          removeTask(key);
          downloadCompletionVersion.value += 1;
          return;
        }
      }

      const job =
        kind === "model" ? await createPullJob(name, { quants }) : await createDatasetPullJob(name);
      setTask({
        ...(downloadTasks.value[key] || task),
        jobId: job.id,
        status: "downloading",
        updatedAt: nowISO(),
      });
      startPolling(key);
    } catch (err: any) {
      const current = downloadTasks.value[key] || task;
      if (current.status === "success") return;
      setTask({
        ...current,
        status: "error",
        statusText: "error",
        error: err?.message || "download failed",
        updatedAt: nowISO(),
      });
      completionCallbacks.delete(key);
    }
  })();

  return true;
}
