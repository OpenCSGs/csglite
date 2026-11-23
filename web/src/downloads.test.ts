import { beforeAll, describe, expect, it, vi } from "vitest";
import type { PullJob } from "./api/client";
import type { DownloadTask } from "./downloads";

const STORAGE_KEY = "csghub-lite-download-tasks";

let downloadTaskKey: typeof import("./downloads").downloadTaskKey;
let compareDownloadTasks: typeof import("./downloads").compareDownloadTasks;
let downloadRowTime: typeof import("./downloads").downloadRowTime;
let applyJobToTask: typeof import("./downloads").applyJobToTask;
let downloadTasks: typeof import("./downloads").downloadTasks;

beforeAll(async () => {
  const values = new Map<string, string>();
  values.set(STORAGE_KEY, JSON.stringify([
    {
      kind: "model",
      name: "Qwen/persisted",
      status: "downloading",
      percent: 40,
      completedBytes: 400,
      totalBytes: 1000,
      speedBytesPerSec: 12 * 1024 * 1024,
    },
  ]));
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  });
  ({ downloadTaskKey, compareDownloadTasks, downloadRowTime, applyJobToTask, downloadTasks } = await import("./downloads"));
});

describe("download row order", () => {
  it("keeps concurrent downloads in start order when progress timestamps move", () => {
    const older = {
      key: "model:older:opencsg@",
      name: "older",
      createdAt: "2026-01-01T00:00:00.000Z",
      updatedAt: "2026-01-03T00:00:02.000Z",
    };
    const newer = {
      key: "model:newer:opencsg@",
      name: "newer",
      createdAt: "2026-01-02T00:00:00.000Z",
      updatedAt: "2026-01-03T00:00:01.000Z",
    };
    const progressed = { ...older, updatedAt: "2026-01-03T00:00:03.000Z" };

    expect([older, newer].sort(compareDownloadTasks).map((task) => task.name)).toEqual(["newer", "older"]);
    expect([progressed, newer].sort(compareDownloadTasks).map((task) => task.name)).toEqual(["newer", "older"]);
    expect(downloadRowTime(progressed)).toBe(progressed.createdAt);
  });
});

describe("downloadTaskKey", () => {
  it("separates identical model repositories by artifact source", () => {
    expect(downloadTaskKey("model", "Qwen/demo", "opencsg")).toBe("model:Qwen/demo:opencsg@");
    expect(downloadTaskKey("model", "Qwen/demo", "huggingface")).toBe("model:Qwen/demo:huggingface@");
    expect(downloadTaskKey("model", "Qwen/demo", "modelscope")).toBe("model:Qwen/demo:modelscope@");
  });

  it("separates dataset repositories by source and revision", () => {
    expect(downloadTaskKey("model", "Qwen/demo", "huggingface", "refs/pr/1")).toBe(
      "model:Qwen/demo:huggingface@refs/pr/1",
    );
    expect(downloadTaskKey("dataset", "acme/data")).toBe("dataset:acme/data:opencsg@");
    expect(downloadTaskKey("dataset", "acme/data", "huggingface", "refs/pr/1")).toBe(
      "dataset:acme/data:huggingface@refs/pr/1",
    );
  });
});

function task(overrides: Partial<DownloadTask> = {}): DownloadTask {
  return {
    key: "model:Qwen/demo:opencsg@",
    kind: "model",
    name: "Qwen/demo",
    status: "downloading",
    percent: 40,
    statusText: "downloading",
    completedBytes: 400,
    totalBytes: 1000,
    speedBytesPerSec: 0,
    createdAt: "2026-01-01T00:00:00.000Z",
    updatedAt: "2026-01-01T00:00:00.000Z",
    files: {},
    ...overrides,
  };
}

function job(status: string, progress: Record<string, unknown> = {}): PullJob {
  return {
    id: "job-1",
    status,
    kind: "model",
    name: "Qwen/demo",
    created_at: "2026-01-01T00:00:00.000Z",
    updated_at: "2026-01-01T00:00:00.000Z",
    progress: { status: "downloading demo.safetensors", ...progress },
  } as PullJob;
}

describe("download task transfer speed", () => {
  it("restores a persisted task without a stale rate", () => {
    const restored = downloadTasks.value[downloadTaskKey("model", "Qwen/persisted")];
    expect(restored?.speedBytesPerSec).toBe(0);
  });

  it("takes the rate from a running job", () => {
    const updated = applyJobToTask(task({ speedBytesPerSec: 4096 }), job("running", { speed_bytes_per_sec: 5 * 1024 * 1024 }));
    expect(updated.status).toBe("downloading");
    expect(updated.speedBytesPerSec).toBe(5 * 1024 * 1024);
  });

  it("treats a missing rate as no transfer", () => {
    const updated = applyJobToTask(task({ speedBytesPerSec: 4096 }), job("running"));
    expect(updated.speedBytesPerSec).toBe(0);
  });

  it("clears the rate in every finished state", () => {
    const base = task({ speedBytesPerSec: 4096 });
    expect(applyJobToTask(base, job("queued", { speed_bytes_per_sec: 4096 })).speedBytesPerSec).toBe(0);
    expect(applyJobToTask(base, job("succeeded")).speedBytesPerSec).toBe(0);
    expect(applyJobToTask(base, job("failed")).speedBytesPerSec).toBe(0);
    expect(applyJobToTask(base, job("cancelled")).speedBytesPerSec).toBe(0);
  });
});
