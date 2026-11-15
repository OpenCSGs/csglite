import { describe, expect, it, vi } from "vitest";

// The module graph reaches i18n, which reads the saved locale on import.
vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

import { candidateCategory } from "./modelServices";
import type { ModelInfo, ProviderPool } from "../api/client";
import { modelHotIcon, sortHotModelsFirst, preferModelServices } from "./modelOptions";

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

describe("published model service choices", () => {
  it("hides only the exact bound source, preserving other providers with the same model name", () => {
    const pool: ProviderPool = { id: "service", name: "Qwen", model: "served-qwen", enabled: true, policy: "failover", members: [{ id: "a", source: "provider:a", model: "qwen" }], source_states: { a: "configured" } };
    const models = [model({ model: "qwen", source: "provider:a" }), model({ model: "qwen", source: "provider:b" }), model({ model: "served-qwen", source: "pool:service" })];
    expect(preferModelServices(models, [pool]).map(m => m.source)).toEqual(["pool:service", "provider:b"]);
    const renamed = models.map(m => m.source === "provider:a" ? { ...m, model: "my-qwen", origin: "qwen" } : m);
    expect(preferModelServices(renamed, [pool]).map(m => m.source)).toEqual(["pool:service", "provider:b"]);
    expect(sortHotModelsFirst([model({ source: "cloud", hot: true }), models[2]])[0].source).toBe("pool:service");
    expect(preferModelServices(models, [pool], "provider:a:qwen").map(m => m.source)).toEqual(["pool:service", "provider:a", "provider:b"]);
    expect(preferModelServices(models, [{ ...pool, enabled: false }]).map(m => m.source)).toEqual(["provider:a", "provider:b"]);
    expect(preferModelServices(models, [{ ...pool, source_states: { a: "disabled" } }]).map(m => m.source)).toEqual(["provider:a", "provider:b"]);
  });

  it("does not merge candidates with incompatible capabilities", () => {
    expect(candidateCategory(model({ category: "embedding", pipeline_tag: "feature-extraction" }))).toBe("embedding");
    expect(candidateCategory(model({ pipeline_tag: "text-to-image" }))).toBe("image");
    expect(candidateCategory(model({ decision_only: true }))).toBe("decision");
    expect(candidateCategory(model({ category: "language_model", pipeline_tag: "text-generation" }))).toBe("chat");
    expect(candidateCategory(model({ pipeline_tag: "image-text-to-text" }))).toBe("chat");
  });
});
