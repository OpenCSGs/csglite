import { describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

import type { LicenseState } from "./api/client";
import { editionOf, formatLicenseDate, isLicensed, licenseStatusKey, licenseTone, productTitle } from "./license";

function state(status: LicenseState["status"], edition = "Enterprise"): LicenseState {
  return { status, edition, license: null, features: [], limits: {}, checked_at: "2026-09-16T00:00:00Z" };
}

describe("license helpers", () => {
  it("treats valid and grace as licensed, everything else as community", () => {
    expect(isLicensed(null)).toBe(false);
    expect(isLicensed(state("none", "Community"))).toBe(false);
    expect(isLicensed(state("valid"))).toBe(true);
    expect(isLicensed(state("grace"))).toBe(true);
    expect(isLicensed(state("expired"))).toBe(false);
    expect(isLicensed(state("invalid"))).toBe(false);
    expect(isLicensed(state("not_started"))).toBe(false);
  });

  it("shows CSGLite EE only while licensed", () => {
    expect(productTitle(null)).toBe("CSGLite");
    expect(productTitle(state("valid"))).toBe("CSGLite EE");
    expect(productTitle(state("grace"))).toBe("CSGLite EE");
    expect(productTitle(state("expired"))).toBe("CSGLite");
  });

  it("reports the edition from the state only when licensed", () => {
    expect(editionOf(null)).toBe("Community");
    expect(editionOf(state("valid", "Trial"))).toBe("Trial");
    expect(editionOf(state("expired", "Enterprise"))).toBe("Community");
  });

  it("maps statuses to tones and label keys", () => {
    expect(licenseTone("valid")).toBe("ok");
    expect(licenseTone("grace")).toBe("warn");
    expect(licenseTone("invalid")).toBe("error");
    expect(licenseTone(undefined)).toBe("neutral");
    expect(licenseStatusKey("grace")).toBe("settings.licenseStatusGrace");
    expect(licenseStatusKey(undefined)).toBe("settings.licenseStatusNone");
  });

  it("formats dates per locale and tolerates bad input", () => {
    expect(formatLicenseDate("", "en")).toBe("");
    expect(formatLicenseDate("not-a-date", "en")).toBe("not-a-date");
    expect(formatLicenseDate("2027-10-01T00:00:00Z", "en")).toContain("2027");
    expect(formatLicenseDate("2027-10-01T00:00:00Z", "zh")).toContain("2027");
  });
});
