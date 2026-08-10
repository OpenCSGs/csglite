import { describe, expect, it } from "vitest";
import { formatObservabilityDuration, observabilityFromForPeriod } from "./observability";

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
});
