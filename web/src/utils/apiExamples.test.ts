import { describe, expect, it } from "vitest";
import { apiExampleModeFromPipelineTag, buildApiExamples } from "./apiExamples";

describe("buildApiExamples", () => {
  it.each([
    ["text-to-image", "text-to-image", "images/generations"],
    ["image-to-image", "image-to-image", "images/edits"],
    ["automatic-speech-recognition", "asr", "audio/transcriptions"],
    ["text-to-speech", "tts", "audio/speech"],
  ] as const)("publishes the %s API at the unified gateway", (tag, mode, endpoint) => {
    expect(apiExampleModeFromPipelineTag(tag)).toBe(mode);
    const examples = buildApiExamples("http://localhost:11435/ai-gateway", "service-model", mode);
    expect(examples.curl).toContain(`/ai-gateway/v1/${endpoint}`);
    for (const code of Object.values(examples)) {
      expect(code).toContain("service-model");
      expect(code).toContain("YOUR_API_KEY");
      expect(code).not.toContain("chat/completions");
    }
  });
  it("uses the unified gateway base URL and API Key for OpenAI examples", () => {
    const examples = buildApiExamples(
      "http://localhost:11435/ai-gateway",
      "public-model",
      "chat",
    );

    expect(examples.curl).toContain("http://localhost:11435/ai-gateway/v1/chat/completions");
    expect(examples.python).toContain('base_url="http://localhost:11435/ai-gateway/v1"');
    expect(examples.python).toContain('model="public-model"');
    expect(examples.curl).toContain('Authorization: Bearer YOUR_API_KEY');
    expect(examples.python).toContain('api_key="YOUR_API_KEY"');
    expect(examples.javascript).toContain('"Authorization": "Bearer YOUR_API_KEY"');
  });

  it("uses the embeddings endpoint for embedding pools", () => {
    const examples = buildApiExamples(
      "http://localhost:11435/ai-gateway",
      "embedding-pool",
      "embedding",
    );

    expect(examples.curl).toContain("/ai-gateway/v1/embeddings");
    expect(examples.python).toContain("client.embeddings.create");
  });

  it("uses the OpenAI Decisions API for typed-decision models", () => {
    const examples = buildApiExamples("http://localhost:11435", "huggingface/mradermacher/JEV-9B-GGUF", "decision");

    expect(examples.curl).toContain("http://localhost:11435/v1/decisions");
    expect(examples.python).toContain('base_url="http://localhost:11435/v1"');
    expect(examples.python).toContain("client.decisions.create(");
    expect(examples.javascript).toContain('baseURL: "http://localhost:11435/v1"');
    expect(examples.javascript).toContain("client.decisions.create(");
    expect(examples.javascript).toContain("console.log(`Department: ${answer.choice}");
    for (const code of [examples.curl, examples.python, examples.javascript]) {
      expect(code).toContain("huggingface/mradermacher/JEV-9B-GGUF");
      expect(code).toContain('"billing"');
    }
  });

  it("guards against empty choices and null delta in streaming examples", () => {
    for (const mode of ["chat", "vision"] as const) {
      const { python } = buildApiExamples("http://localhost:11435", "m", mode);
      expect(python).toContain("if not chunk.choices");
      expect(python).not.toMatch(/if chunk\.choices\[0\]/);
    }
  });
});
