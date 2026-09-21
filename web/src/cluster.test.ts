import { describe, expect, it } from "vitest";

import {
  fmtGB,
  fmtGBPair,
  fmtSeconds,
  healthKey,
  healthTone,
  isValidAdmissionCode,
  nodeLimitLabel,
  normalizeAdmissionCode,
  percentOf,
  secondsUntil,
  stateKey,
} from "./cluster";

describe("cluster formatting helpers", () => {
  it("formats bytes as gigabytes with one decimal", () => {
    expect(fmtGB(24 * 1024 ** 3)).toBe("24.0");
    expect(fmtGB(1.25 * 1024 ** 3)).toBe("1.3");
    expect(fmtGB(0)).toBe("0.0");
    expect(fmtGB(Number.NaN)).toBe("0.0");
    expect(fmtGBPair(1024 ** 3, 4 * 1024 ** 3)).toBe("1.0 / 4.0 GB");
  });

  it("clamps percentages and tolerates unknown totals", () => {
    expect(percentOf(1, 4)).toBe(25);
    expect(percentOf(5, 4)).toBe(100);
    expect(percentOf(1, 0)).toBe(0);
    expect(percentOf(-1, 4)).toBe(0);
  });

  it("formats estimated seconds compactly", () => {
    expect(fmtSeconds(0.4)).toBe("<1s");
    expect(fmtSeconds(12.2)).toBe("12s");
    expect(fmtSeconds(65)).toBe("1m 05s");
    expect(fmtSeconds(-1)).toBe("—");
  });

  it("counts down to an ISO timestamp", () => {
    const now = Date.parse("2026-09-18T10:00:00Z");
    expect(secondsUntil("2026-09-18T10:00:30Z", now)).toBe(30);
    expect(secondsUntil("2026-09-18T09:59:00Z", now)).toBe(0);
    expect(secondsUntil("not a date", now)).toBeNull();
    expect(secondsUntil(undefined, now)).toBeNull();
  });
});

describe("node health mapping", () => {
  it("colours online nodes by gossip health", () => {
    expect(healthTone("healthy", true)).toBe("ok");
    expect(healthTone("suspect", true)).toBe("warn");
    expect(healthTone("probing", true)).toBe("warn");
    expect(healthTone("down", true)).toBe("error");
    expect(healthTone("unknown", true)).toBe("neutral");
    expect(healthTone(undefined, true)).toBe("neutral");
  });

  it("shows an offline node as red whatever its health says", () => {
    expect(healthTone("healthy", false)).toBe("error");
    expect(healthKey("healthy", false)).toBe("cluster.healthOffline");
    expect(healthKey("suspect", true)).toBe("cluster.healthSuspect");
  });

  it("maps states to i18n keys", () => {
    expect(stateKey("drain")).toBe("cluster.stateDrain");
    expect(stateKey("maintenance")).toBe("cluster.stateMaintenance");
    expect(stateKey("active")).toBe("cluster.stateActive");
    expect(stateKey(undefined)).toBe("");
  });
});

describe("node limit label", () => {
  it("treats a zero limit as unlimited", () => {
    expect(nodeLimitLabel(5, 0)).toEqual({ key: "cluster.nodesUnlimited", args: [5], atLimit: false });
  });

  it("reports the cap with the upgrade hint once it is reached", () => {
    expect(nodeLimitLabel(1, 2)).toEqual({ key: "cluster.nodesOfLimit", args: [1, 2], atLimit: false });
    expect(nodeLimitLabel(2, 2)).toEqual({ key: "cluster.nodesAtLimit", args: [2, 2], atLimit: true });
    expect(nodeLimitLabel(3, 2).atLimit).toBe(true);
  });
});

describe("admission code", () => {
  it("accepts eight digits with cosmetic separators", () => {
    expect(normalizeAdmissionCode("1234 5678")).toBe("12345678");
    expect(isValidAdmissionCode("1234-5678")).toBe(true);
    expect(isValidAdmissionCode("12345678")).toBe(true);
    expect(isValidAdmissionCode("1234567")).toBe(false);
    expect(isValidAdmissionCode("abcd1234")).toBe(false);
  });
});
