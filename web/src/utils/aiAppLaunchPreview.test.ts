import { describe, expect, it } from "vitest";
import { aiAppLaunchPreview, parseAIAppModelKey } from "./aiAppLaunchPreview";

describe("AI app launch examples", () => {
  it("uses the configured provider name instead of its internal ID", () => {
    expect(aiAppLaunchPreview("claude-code", "MiniMax-M3", "provider:a0712465fcc9492f", "MiniMax")).toContain(
      '--provider "MiniMax"',
    );
  });

  it("uses a provider pool name and does not expose its ID", () => {
    const preview = aiAppLaunchPreview("claude-code", "code-pool", "pool:c4ed2ee85360025e", "聚合Model API");

    expect(preview.split("\n").slice(0, 2)).toEqual([
      "csghub-lite launch claude",
      'csghub-lite launch claude --pool "聚合Model API"',
    ]);
    expect(preview).not.toContain("c4ed2ee85360025e");
  });

  it("keeps a nested pool source separate from its public model ID", () => {
    expect(parseAIAppModelKey("pool:c4ed2ee85360025e:code-pool")).toEqual({
      source: "pool:c4ed2ee85360025e",
      model: "code-pool",
    });
  });
});
