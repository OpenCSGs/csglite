import { describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

import { ApiError } from "./api/client";
import type { LicenseCloudStatus, LicenseState } from "./api/client";
import { t } from "./i18n";
import {
  cloudDeviceUsage,
  cloudImportAvailability,
  cloudImportErrorMessage,
  cloudLicenseCanReplace,
  deviceLimitOf,
  editionOf,
  formatLicenseDate,
  isLicensed,
  licenseStatusKey,
  licenseTone,
  productTitle,
} from "./license";

function state(status: LicenseState["status"], edition = "Enterprise"): LicenseState {
  return { status, edition, license: null, features: [], limits: {}, checked_at: "2026-09-16T00:00:00Z" };
}

function licensedState(key: string, expire: string): LicenseState {
  return {
    status: "valid",
    edition: "Enterprise",
    license: {
      key,
      company: "Test Co",
      email: "",
      product: "CSGLite",
      edition: "Enterprise",
      max_user: 10,
      start_time: "2026-01-01T00:00:00Z",
      expire_time: expire,
    },
    features: [],
    limits: { "quota.lite.max_devices": 10 },
    checked_at: "2026-09-16T00:00:00Z",
  };
}

function cloud(overrides: Partial<LicenseCloudStatus> = {}): LicenseCloudStatus {
  return {
    logged_in: true,
    online: true,
    supported: true,
    state: "active",
    has_license: true,
    not_started: false,
    license_key: "subscription:4:1",
    expire_time: "2027-06-01T00:00:00Z",
    grace_until: "",
    subscription_status: "active",
    used: 3,
    limit: 10,
    env_managed: false,
    ...overrides,
  };
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

describe("cloud import availability", () => {
  it("requires a usable account and an online, supported server", () => {
    expect(cloudImportAvailability(null, null)).toEqual({
      enabled: false,
      hintKey: "settings.licenseCloudHintUnavailable",
    });
    expect(cloudImportAvailability(null, cloud({ logged_in: false, reason: "login_required" })).hintKey).toBe(
      "settings.licenseCloudHintLogin",
    );
    expect(cloudImportAvailability(null, cloud({ logged_in: false, reason: "token_invalid" })).hintKey).toBe(
      "settings.licenseCloudHintToken",
    );
    expect(cloudImportAvailability(null, cloud({ online: false, reason: "unreachable" })).hintKey).toBe(
      "settings.licenseCloudHintOffline",
    );
    expect(cloudImportAvailability(null, cloud({ supported: false, reason: "unsupported" })).hintKey).toBe(
      "settings.licenseCloudHintUnsupported",
    );
    expect(cloudImportAvailability(null, cloud({ reason: "server_error" })).hintKey).toBe(
      "settings.licenseCloudHintServerError",
    );
    expect(cloudImportAvailability(null, cloud({ has_license: false, state: "none" })).hintKey).toBe(
      "settings.licenseCloudHintNoSubscription",
    );
  });

  it("disables the button for env-managed licenses and unstarted cloud licenses", () => {
    expect(cloudImportAvailability(state("none"), cloud({ env_managed: true })).hintKey).toBe(
      "settings.licenseCloudHintEnv",
    );
    const env = state("valid");
    env.source = "env";
    expect(cloudImportAvailability(env, cloud()).hintKey).toBe("settings.licenseCloudHintEnv");
    expect(
      cloudImportAvailability(licensedState("subscription:4:1", "2027-01-01T00:00:00Z"), cloud({ state: "not_started", not_started: true })),
    ).toEqual({ enabled: false, hintKey: "settings.licenseCloudHintNotStarted" });
  });

  it("enables the button only when the cloud has something newer", () => {
    const local = licensedState("subscription:4:1", "2027-01-01T00:00:00Z");
    expect(cloudImportAvailability(local, cloud({ expire_time: "2027-01-01T00:00:00Z" }))).toEqual({
      enabled: false,
      hintKey: "",
    });
    expect(cloudImportAvailability(local, cloud({ expire_time: "2027-06-01T00:00:00Z" })).enabled).toBe(true);
    expect(
      cloudImportAvailability(local, cloud({ license_key: "subscription:4:2", expire_time: "2027-01-01T00:00:00Z" }))
        .enabled,
    ).toBe(true);
    expect(cloudImportAvailability(state("expired"), cloud()).enabled).toBe(true);
    expect(cloudImportAvailability(state("not_started"), cloud()).enabled).toBe(true);
    expect(cloudImportAvailability(null, cloud()).enabled).toBe(true);
  });

  it("replaces the installed license on a new key or a later expiry", () => {
    const local = licensedState("subscription:4:1", "2027-01-01T00:00:00Z");
    expect(cloudLicenseCanReplace(local, cloud({ expire_time: "2027-02-01T00:00:00Z" }))).toBe(true);
    expect(cloudLicenseCanReplace(local, cloud({ expire_time: "2026-12-01T00:00:00Z" }))).toBe(false);
    expect(cloudLicenseCanReplace(local, cloud({ expire_time: "2027-01-01T00:00:00Z" }))).toBe(false);
    expect(cloudLicenseCanReplace(local, cloud({ expire_time: undefined }))).toBe(false);
    expect(cloudLicenseCanReplace(null, cloud())).toBe(false);
    // A re-issued license or another account: a different key wins even when
    // the expiry date is the same or older.
    expect(
      cloudLicenseCanReplace(local, cloud({ license_key: "subscription:4:2", expire_time: "2027-01-01T00:00:00Z" })),
    ).toBe(true);
    expect(
      cloudLicenseCanReplace(local, cloud({ license_key: "subscription:4:2", expire_time: "2026-12-01T00:00:00Z" })),
    ).toBe(true);
  });
});

describe("device limits", () => {
  it("shows the server-enforced device limit for a licensed install", () => {
    expect(deviceLimitOf(null)).toBeNull();
    expect(deviceLimitOf(state("none"))).toBeNull();
    expect(deviceLimitOf(licensedState("k", "2027-01-01T00:00:00Z"))).toBe(10);
    // The server ignores an Extra override, so the UI must not display one.
    const custom = licensedState("k", "2027-01-01T00:00:00Z");
    custom.limits = { "quota.lite.max_devices": 5 };
    expect(deviceLimitOf(custom)).toBe(10);
  });

  it("shows cloud usage only for the installed license and clamps it", () => {
    const local = licensedState("subscription:4:1", "2027-01-01T00:00:00Z");
    expect(cloudDeviceUsage(local, cloud())).toEqual({ used: 3, limit: 10, overLimit: false });
    expect(cloudDeviceUsage(local, cloud({ used: 12 }))).toEqual({ used: 10, limit: 10, overLimit: true });
    expect(cloudDeviceUsage(local, cloud({ license_key: "subscription:4:2" }))).toBeNull();
    expect(cloudDeviceUsage(local, cloud({ has_license: false }))).toBeNull();
    expect(cloudDeviceUsage(null, cloud())).toBeNull();
  });
});

describe("cloud import error messages", () => {
  it("uses the quota message only when the server reported a limit", () => {
    expect(
      cloudImportErrorMessage(new ApiError("limit reached", 409, { code: "LICENSE-ERR-7", used: 10, limit: 10 })),
    ).toBe(t("settings.licenseCloudLimitReached", 10, 10));
    expect(cloudImportErrorMessage(new ApiError("limit reached", 409, { code: "LICENSE-ERR-7" }))).toBe(
      t("settings.licenseCloudDeviceLimit"),
    );
  });

  it("localizes a hub-side expiry instead of showing the server text", () => {
    expect(cloudImportErrorMessage(new ApiError("license expired", 409, { code: "LICENSE-ERR-1" }))).toBe(
      t("settings.licenseCloudExpired"),
    );
  });

  it("maps local codes to localized text", () => {
    expect(cloudImportErrorMessage(new ApiError("too many", 429, { code: "rate_limited" }))).toBe(
      t("settings.licenseCloudRateLimited"),
    );
    expect(cloudImportErrorMessage(new Error("plain"))).toBe("plain");
  });
});
