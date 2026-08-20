import { beforeAll, describe, expect, it, vi } from "vitest";

let downloadTaskKey: typeof import("./downloads").downloadTaskKey;

beforeAll(async () => {
  const values = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  });
  ({ downloadTaskKey } = await import("./downloads"));
});

describe("downloadTaskKey", () => {
  it("separates identical model repositories by artifact source", () => {
    expect(downloadTaskKey("model", "Qwen/demo", "opencsg")).toBe("model:Qwen/demo:opencsg@");
    expect(downloadTaskKey("model", "Qwen/demo", "huggingface")).toBe("model:Qwen/demo:huggingface@");
    expect(downloadTaskKey("model", "Qwen/demo", "modelscope")).toBe("model:Qwen/demo:modelscope@");
  });

  it("includes revisions without changing dataset keys", () => {
    expect(downloadTaskKey("model", "Qwen/demo", "huggingface", "refs/pr/1")).toBe(
      "model:Qwen/demo:huggingface@refs/pr/1",
    );
    expect(downloadTaskKey("dataset", "acme/data")).toBe("dataset:acme/data");
  });
});
