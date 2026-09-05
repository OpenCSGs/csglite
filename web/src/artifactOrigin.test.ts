import { describe, expect, it } from "vitest";
import { downloadRowOriginKey, modelOriginKey } from "./artifactOrigin";

describe("modelOriginKey", () => {
  it("names marketplace sources for downloaded models", () => {
    expect(modelOriginKey("marketplace", "huggingface")).toBe("lib.originHuggingFace");
    expect(modelOriginKey("marketplace", "modelscope")).toBe("lib.originModelScope");
    expect(modelOriginKey("marketplace", "opencsg")).toBe("lib.originOpenCSG");
    expect(modelOriginKey("upload")).toBe("lib.originUpload");
  });

  it("uses the download task source instead of a blank placeholder", () => {
    expect(downloadRowOriginKey("huggingface")).toBe("lib.originHuggingFace");
    expect(downloadRowOriginKey("modelscope")).toBe("lib.originModelScope");
    expect(downloadRowOriginKey(undefined)).toBe("lib.originOpenCSG");
  });
});
