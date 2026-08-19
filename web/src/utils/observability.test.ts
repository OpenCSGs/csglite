import { describe, expect, it } from "vitest";
import {
  formatObservabilityDateTime,
  formatObservabilityDuration,
  formatObservabilityModel,
  observabilityFromForPeriod,
} from "./observability";

describe("observability helpers", () => {
  it("builds rolling period timestamps", () => {
    const now = Date.parse("2026-08-10T10:00:00.000Z");
    expect(observabilityFromForPeriod("24h", now)).toBe("2026-08-09T10:00:00.000Z");
    expect(observabilityFromForPeriod("7d", now)).toBe("2026-08-03T10:00:00.000Z");
    expect(observabilityFromForPeriod("all", now)).toBeUndefined();
  });

  it("formats millisecond and second durations", () => {
    expect(formatObservabilityDuration(42.4)).toBe("42 ms");
    expect(formatObservabilityDuration(1250)).toBe("1.25 s");
    expect(formatObservabilityDuration(12500)).toBe("12.5 s");
  });

  it("formats timestamps with a short local date", () => {
    const value = new Date(2026, 7, 19, 21, 0, 13).toISOString();
    expect(formatObservabilityDateTime(value)).toBe("26/8/19 21:00:13");
    expect(formatObservabilityDateTime("invalid")).toBe("—");
  });

  it("limits model names to ten characters", () => {
    expect(formatObservabilityModel("Qwen3-7B")).toBe("Qwen3-7B");
    expect(formatObservabilityModel("Qwen3-Embedding-0.6B")).toBe("Qwen3-Embe…");
  });
});
