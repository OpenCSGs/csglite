import { describe, expect, it } from "vitest";
import { buildApiExamples } from "./apiExamples";

describe("buildApiExamples", () => {
  it("uses a provider-scoped base URL for OpenAI examples", () => {
    const examples = buildApiExamples(
      "http://localhost:11435/providers/pool-one",
      "public-model",
      "chat",
    );

    expect(examples.curl).toContain("http://localhost:11435/providers/pool-one/v1/chat/completions");
    expect(examples.python).toContain('base_url="http://localhost:11435/providers/pool-one/v1"');
    expect(examples.python).toContain('model="public-model"');
  });

  it("uses the embeddings endpoint for embedding pools", () => {
    const examples = buildApiExamples(
      "http://localhost:11435/providers/pool-one",
      "embedding-pool",
      "embedding",
    );

    expect(examples.curl).toContain("/providers/pool-one/v1/embeddings");
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
