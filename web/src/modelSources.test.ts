import { describe, expect, it } from "vitest";
import { modelOwnerIdentity, normalizeMarketplaceModelSource } from "./modelSources";

describe("normalizeMarketplaceModelSource", () => {
  it("restores supported persisted sources", () => {
    expect(normalizeMarketplaceModelSource("huggingface")).toBe("huggingface");
    expect(normalizeMarketplaceModelSource("modelscope")).toBe("modelscope");
  });

  it("keeps old or invalid settings backward compatible", () => {
    expect(normalizeMarketplaceModelSource(undefined)).toBe("opencsg");
    expect(normalizeMarketplaceModelSource("unknown")).toBe("opencsg");
  });
});

describe("modelOwnerIdentity", () => {
  it("derives a stable organization initial from the repository path", () => {
    const first = modelOwnerIdentity("Qwen/Qwen3.8-27B");
    const second = modelOwnerIdentity("Qwen/Another-Model");
    expect(first.owner).toBe("Qwen");
    expect(first.initial).toBe("Q");
    expect(second.palette).toBe(first.palette);
  });

  it("supports non-Latin organization names", () => {
    expect(modelOwnerIdentity("通义实验室/model").initial).toBe("通");
  });
});
