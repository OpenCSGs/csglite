import { signal } from "@preact/signals";
import { getLicense } from "./api/client";
import type { LicenseState, LicenseStatus } from "./api/client";

/**
 * Shared license state. The sidebar reads it to show the edition and the
 * Settings page writes it after an import or removal, so both stay in sync
 * without an extra round trip.
 */
export const licenseState = signal<LicenseState | null>(null);
export const licenseLoadError = signal("");

export const ENTERPRISE_EDITION = "Enterprise";
export const COMMUNITY_EDITION = "Community";

export async function loadLicense(): Promise<LicenseState | null> {
  try {
    const state = await getLicense();
    licenseState.value = state;
    licenseLoadError.value = "";
    return state;
  } catch (err: any) {
    licenseLoadError.value = err?.message || "";
    return null;
  }
}

/** True while an enterprise license is in effect (valid or in its grace period). */
export function isLicensed(state: LicenseState | null): boolean {
  return state !== null && (state.status === "valid" || state.status === "grace");
}

/** Edition label to display; falls back to Community until the state is known. */
export function editionOf(state: LicenseState | null): string {
  if (!isLicensed(state)) return COMMUNITY_EDITION;
  return state?.edition || ENTERPRISE_EDITION;
}

/** The product name shown in the sidebar: "CSGLite" or "CSGLite EE". */
export function productTitle(state: LicenseState | null): string {
  return isLicensed(state) ? "CSGLite EE" : "CSGLite";
}

export type LicenseTone = "neutral" | "ok" | "warn" | "error";

/** Visual tone for a license status badge. */
export function licenseTone(status: LicenseStatus | undefined): LicenseTone {
  switch (status) {
    case "valid":
      return "ok";
    case "grace":
    case "not_started":
      return "warn";
    case "expired":
    case "invalid":
      return "error";
    default:
      return "neutral";
  }
}

/** i18n key for a license status label. */
export function licenseStatusKey(status: LicenseStatus | undefined): string {
  switch (status) {
    case "valid":
      return "settings.licenseStatusValid";
    case "grace":
      return "settings.licenseStatusGrace";
    case "expired":
      return "settings.licenseStatusExpired";
    case "invalid":
      return "settings.licenseStatusInvalid";
    case "not_started":
      return "settings.licenseStatusNotStarted";
    default:
      return "settings.licenseStatusNone";
  }
}

export function formatLicenseDate(iso: string | undefined | null, locale: string): string {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleDateString(locale === "zh" ? "zh-CN" : "en-US", {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}
