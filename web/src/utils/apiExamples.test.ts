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
});
