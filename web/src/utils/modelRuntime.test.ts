import { describe, expect, it } from "vitest";
import type { ModelInfo, MarketplaceModel } from "../api/client";
import { deploymentModelKind, isLlamaDeployment } from "../deploymentModels";
import { contextWindowApplies, modelContextWindowApplies, modelRuntimeKind } from "./modelRuntime";

describe("shared model runtime parameters", () => {
  it.each([
    ["text-generation", "chat", true],
    ["image-text-to-text", "chat", true],
    ["feature-extraction", "embedding", true],
    ["sentence-similarity", "embedding", true],
    ["text-to-image", "image", false],
    ["image-to-image", "image", false],
    ["automatic-speech-recognition", "asr", false],
    ["text-to-speech", "tts", false],
    ["text-to-audio", "tts", false],
  ])("library and deployment forms agree for task %s", (task, kind, context) => {
    const local = { pipeline_tag: task } as ModelInfo;
    const remote = { tags: [{ category: "task", name: task }] } as MarketplaceModel;
    expect(modelRuntimeKind(local)).toBe(kind);
    expect(deploymentModelKind(remote)).toBe(kind);
    expect(modelContextWindowApplies(local)).toBe(context);
    expect(isLlamaDeployment(deploymentModelKind(remote))).toBe(context);
  });
  it("uses categories and inspected runtimes when task metadata is absent", () => {
    for (const kind of ["image", "asr", "tts"] as const) {
      expect(modelContextWindowApplies({ category: kind })).toBe(false);
      expect(modelRuntimeKind({ local_inference: { supported: true, mode: kind } })).toBe(kind);
    }
    expect(modelContextWindowApplies({ local_inference: { supported: true, mode: "embedding" } })).toBe(true);
  });
  it("keeps legacy local LLM settings and audio modality fallbacks", () => {
    expect(modelContextWindowApplies({})).toBe(true);
    expect(modelRuntimeKind({ input_modalities: ["audio"] })).toBe("asr");
    expect(modelRuntimeKind({ output_modalities: ["speech"] })).toBe("tts");
    expect(contextWindowApplies("unknown")).toBe(false);
  });
  it("does not mistake a multimodal language model for image generation or ASR", () => {
    const model = { pipeline_tag: "image-text-to-text", input_modalities: ["text", "image", "audio"] };
    expect(modelRuntimeKind(model)).toBe("chat");
    expect(modelContextWindowApplies(model)).toBe(true);
  });
});
