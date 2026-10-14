import { describe, expect, it } from "vitest";
import { isDecisionTag, orderModelTags } from "./modelTags";

describe("model tags", () => {
  it("recognizes decision tags case-insensitively", () => {
    expect(isDecisionTag("JEV")).toBe(true);
    expect(isDecisionTag(" typed-decisions ")).toBe(true);
    expect(isDecisionTag("jevons")).toBe(false);
  });

  it("puts decision tags first and keeps card order otherwise", () => {
    expect(orderModelTags(["lora", "typed-decisions", "qwen3_5", "jev"])).toEqual(["typed-decisions", "jev", "lora", "qwen3_5"]);
  });
});
