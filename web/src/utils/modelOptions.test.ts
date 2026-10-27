import { describe, expect, it, vi } from "vitest";

// The module graph reaches i18n, which reads the saved locale on import.
vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

import type { ModelInfo } from "../api/client";
import { modelHotIcon, sortHotModelsFirst } from "./modelOptions";

function model(overrides: Partial<ModelInfo>): ModelInfo {
  return { name: "m", model: "m", size: 0, format: "", modified_at: "", ...overrides };
}

describe("hot model markers", () => {
  it("prefixes only hot models", () => {
    expect(modelHotIcon({ hot: true })).toBe("🔥 ");
    expect(modelHotIcon({ hot: false })).toBe("");
    expect(modelHotIcon({})).toBe("");
  });

  it("moves hot models first and keeps the rest in order", () => {
    const models = [model({ model: "b" }), model({ model: "auto", hot: true }), model({ model: "c" })];
    expect(sortHotModelsFirst(models).map((item) => item.model)).toEqual(["auto", "b", "c"]);
  });
});
