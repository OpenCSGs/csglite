import { describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

import type { LicenseState } from "./api/client";
import {
  checkoutOutcome,
  checkoutSettled,
  editionLabel,
  editionOf,
  formatLicenseDate,
  isLicensed,
  licenseOriginKey,
  licenseStatusKey,
  licenseTone,
  productTitle,
  upgradeActionOf,
} from "./license";

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

  it("offers an upgrade to Community and a renewal near expiry", () => {
    const now = new Date("2026-10-10T00:00:00Z");
    const withExpiry = (status: LicenseState["status"], expire: string): LicenseState => ({
      ...state(status),
      license: {
        key: "k",
        company: "c",
        email: "e",
        product: "CSGLite",
        edition: "Enterprise",
        max_user: 1,
        start_time: "2026-01-01T00:00:00Z",
        expire_time: expire,
      },
    });
    expect(upgradeActionOf(null, now)).toBe(null);
    expect(upgradeActionOf(state("none", "Community"), now)).toBe("upgrade");
    expect(upgradeActionOf(state("expired"), now)).toBe("upgrade");
    expect(upgradeActionOf({ ...state("none", "Community"), source: "env" }, now)).toBe(null);
    expect(upgradeActionOf(withExpiry("valid", "2027-10-10T00:00:00Z"), now)).toBe(null);
    expect(upgradeActionOf(withExpiry("valid", "2026-11-01T00:00:00Z"), now)).toBe("renew");
    expect(upgradeActionOf(withExpiry("grace", "2026-10-01T00:00:00Z"), now)).toBe("renew");
    expect(upgradeActionOf(state("valid"), now)).toBe(null);
  });

  it("stops polling once a purchase completes or expires", () => {
    expect(checkoutSettled("pending")).toBe(false);
    expect(checkoutSettled("paying")).toBe(false);
    expect(checkoutSettled(undefined)).toBe(false);
    expect(checkoutSettled("completed")).toBe(true);
    expect(checkoutSettled("expired")).toBe(true);
  });

  it("tells a purchase paid by another account apart from an installed one", () => {
    expect(checkoutOutcome({ status: "paying" })).toBe("waiting");
    expect(checkoutOutcome({ status: "expired", paid_by_other: true })).toBe("expired");
    expect(checkoutOutcome({ status: "completed", license: state("valid") })).toBe("installed");
    expect(checkoutOutcome({ status: "completed", license: state("none") })).toBe("installing");
    expect(checkoutOutcome({ status: "completed" })).toBe("installing");
    // This installation may already be licensed; the new purchase still went elsewhere.
    expect(checkoutOutcome({ status: "completed", paid_by_other: true, license: state("valid") })).toBe("paidByOther");
  });

  it("labels origins and editions", () => {
    expect(licenseOriginKey("auto")).toBe("settings.licenseOriginAuto");
    expect(licenseOriginKey("manual")).toBe("settings.licenseOriginManual");
    expect(licenseOriginKey(undefined)).toBe("settings.licenseOriginManual");
    expect(editionLabel("Trial")).toBe("Trial");
    expect(editionLabel("Enterprise")).not.toBe("");
  });
});
