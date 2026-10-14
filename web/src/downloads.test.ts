import { beforeAll, describe, expect, it, vi } from "vitest";

let downloadTaskKey: typeof import("./downloads").downloadTaskKey;
let compareDownloadTasks: typeof import("./downloads").compareDownloadTasks;
let downloadRowTime: typeof import("./downloads").downloadRowTime;

beforeAll(async () => {
  const values = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  });
  ({ downloadTaskKey, compareDownloadTasks, downloadRowTime } = await import("./downloads"));
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
