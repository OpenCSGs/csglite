import { describe, expect, it } from "vitest";
import { displayLocalDatasetID, localDatasetID } from "./datasetIds";

describe("local dataset IDs", () => {
  it("keeps OpenCSG IDs backward compatible", () => {
    const dataset = { name: "acme/data", repository: "acme/data", artifact_source: "opencsg" as const };
    expect(displayLocalDatasetID(dataset)).toBe("acme/data");
    expect(localDatasetID(dataset)).toBe("acme/data");
  });

  it("hides provider qualification while retaining it for API identity", () => {
    const dataset = {
      name: "huggingface/acme/data",
      repository: "acme/data",
      artifact_source: "huggingface" as const,
    };
    expect(displayLocalDatasetID(dataset)).toBe("acme/data");
    expect(localDatasetID(dataset)).toBe("huggingface/acme/data");
  });
});
