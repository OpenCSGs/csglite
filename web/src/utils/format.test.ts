import { beforeAll, describe, expect, it, vi } from "vitest";

let formatSpeed: typeof import("./format").formatSpeed;

beforeAll(async () => {
  const values = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  });
  ({ formatSpeed } = await import("./format"));
});

describe("formatSpeed", () => {
  it("uses binary units with one decimal", () => {
    expect(formatSpeed(1024)).toBe("1.0 KB/s");
    expect(formatSpeed(12.3 * 1024 * 1024)).toBe("12.3 MB/s");
    expect(formatSpeed(1.5 * 1024 ** 3)).toBe("1.5 GB/s");
  });

  it("drops the decimal below one KB/s", () => {
    expect(formatSpeed(512)).toBe("512 B/s");
  });

  it("renders nothing when no data is flowing", () => {
    expect(formatSpeed(0)).toBe("");
    expect(formatSpeed(0.3)).toBe("");
    expect(formatSpeed(undefined)).toBe("");
    expect(formatSpeed(-1)).toBe("");
  });
});
