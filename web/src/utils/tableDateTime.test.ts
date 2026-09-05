import { describe, expect, it } from "vitest";
import { formatTableDateTime } from "./tableDateTime";

describe("formatTableDateTime", () => {
  it("formats local time as yy/m/d hh:mm", () => {
    const value = new Date(2026, 7, 22, 15, 30).toISOString();
    expect(formatTableDateTime(value)).toBe("26/8/22 15:30");
  });

  it("leaves month and day unpadded", () => {
    const value = new Date(2026, 0, 5, 9, 5).toISOString();
    expect(formatTableDateTime(value)).toBe("26/1/5 09:05");
  });

  it("returns a dash for missing or invalid values", () => {
    expect(formatTableDateTime()).toBe("—");
    expect(formatTableDateTime("not-a-date")).toBe("—");
  });
});
