import { describe, expect, it } from "vitest";
import { formatCardParams, providerDatasetCardInfo, providerModelCardInfo } from "./marketplaceCardInfo";

describe("providerModelCardInfo", () => {
  it("surfaces Hugging Face author, task, params, and gated state", () => {
    const info = providerModelCardInfo({
      id: 1,
      name: "Qwen3-8B",
      path: "Qwen/Qwen3-8B",
      description: "A chat model",
      likes: 10,
      downloads: 20,
      tags: [
        { name: "gguf", category: "framework", show_name: "GGUF" },
        { name: "apache-2.0", category: "license", show_name: "Apache 2.0" },
      ],
      license: "apache-2.0",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-02T00:00:00Z",
      artifact_source: "huggingface",
      metadata: { model_params: 8, architecture: "Qwen3ForCausalLM" },
      provider: {
        huggingface: {
          author: "Qwen",
          pipeline_tag: "text-generation",
          library_name: "transformers",
          languages: ["en", "zh"],
          gated: true,
        },
      },
    });

    expect(info.title).toBe("Qwen3-8B");
    expect(info.author).toBe("Qwen");
    expect(info.task).toBe("text-generation");
    expect(info.library).toBe("transformers");
    expect(info.params).toBe("8B");
    expect(info.architecture).toBe("Qwen3ForCausalLM");
    expect(info.license).toBe("apache-2.0");
    expect(info.gated).toBe(true);
  });

  it("prefers ModelScope display name and model type", () => {
    const info = providerModelCardInfo({
      id: 2,
      name: "demo",
      path: "org/demo",
      description: "",
      likes: 0,
      downloads: 0,
      tags: [],
      license: "MIT",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-02T00:00:00Z",
      artifact_source: "modelscope",
      provider: {
        modelscope: {
          display_name: "Demo Chat",
          tasks: ["text-generation"],
          libraries: ["modelscope"],
          model_type: "llm",
        },
      },
    });

    expect(info.title).toBe("Demo Chat");
    expect(info.path).toBe("org/demo");
    expect(info.task).toBe("text-generation");
    expect(info.library).toBe("modelscope");
    expect(info.modelType).toBe("llm");
    expect(info.license).toBe("MIT");
  });
});

describe("providerDatasetCardInfo", () => {
  it("uses Hugging Face pretty name and dataset card metadata", () => {
    const info = providerDatasetCardInfo({
      id: 3,
      name: "data",
      path: "acme/data",
      description: "Labeled reviews",
      likes: 1,
      downloads: 2,
      tags: [
        { name: "task_categories:text-classification", category: "task", show_name: "text-classification" },
        { name: "size_categories:1K<n<10K", category: "size", show_name: "1K<n<10K" },
        { name: "language:en", category: "language", show_name: "en" },
      ],
      license: "cc-by-4.0",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-02T00:00:00Z",
      artifact_source: "huggingface",
      provider: {
        huggingface: {
          author: "acme",
          pretty_name: "Review Bench",
          languages: ["en"],
          task_categories: ["text-classification"],
          gated: false,
        },
      },
    });

    expect(info.title).toBe("Review Bench");
    expect(info.author).toBe("acme");
    expect(info.tasks).toEqual(expect.arrayContaining(["text-classification"]));
    expect(info.sizeCategory).toBe("1K<n<10K");
    expect(info.license).toBe("cc-by-4.0");
  });
});

describe("formatCardParams", () => {
  it("formats billions and trillions", () => {
    expect(formatCardParams(0)).toBe("");
    expect(formatCardParams(1.5)).toBe("1.5B");
    expect(formatCardParams(8)).toBe("8B");
    expect(formatCardParams(1200)).toBe("1.2T");
  });
});
