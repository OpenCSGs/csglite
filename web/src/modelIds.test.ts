import { describe, expect, it } from "vitest";
import { displayLocalModelID } from "./modelIds";

describe("displayLocalModelID", () => {
  it("shows the repository without its internal source prefix", () => {
    expect(displayLocalModelID({
      name: "huggingface/Qwen/Qwen3-8B",
      repository: "Qwen/Qwen3-8B",
      artifact_source: "huggingface",
    })).toBe("Qwen/Qwen3-8B");
  });

  it("keeps legacy OpenCSG IDs unchanged", () => {
    expect(displayLocalModelID({
      name: "Qwen/Qwen3-8B",
      artifact_source: "opencsg",
    })).toBe("Qwen/Qwen3-8B");
  });
});
