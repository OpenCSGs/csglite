import { signal } from "@preact/signals";
import { getLicense } from "./api/client";
import type { LicenseCheckoutStatus, LicenseCheckoutStatusResponse, LicenseState, LicenseStatus } from "./api/client";
import { t } from "./i18n";

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

/** Localized name of an edition; editions without a translation pass through. */
export function editionLabel(edition: string): string {
  if (edition === ENTERPRISE_EDITION) return t("settings.licenseEditionEnterprise");
  if (edition === COMMUNITY_EDITION) return t("settings.licenseEditionCommunity");
  return edition;
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

/** How close to expiry the sidebar starts offering a renewal. */
export const RENEW_WINDOW_DAYS = 30;

/**
 * The upgrade action the sidebar offers: "upgrade" for the Community
 * edition, "renew" for a license in its grace period or expiring within
 * RENEW_WINDOW_DAYS, and null while a license is comfortably in effect.
 * Nothing is offered before the state loads, or while the license comes from
 * the environment, where a purchased license could not be installed.
 */
export function upgradeActionOf(state: LicenseState | null, now: Date = new Date()): "upgrade" | "renew" | null {
  if (state === null || state.source === "env") return null;
  if (!isLicensed(state)) return "upgrade";
  if (state?.status === "grace") return "renew";
  const expires = state?.license?.expire_time ? new Date(state.license.expire_time).getTime() : Number.NaN;
  if (Number.isNaN(expires)) return null;
  return expires - now.getTime() <= RENEW_WINDOW_DAYS * 24 * 60 * 60 * 1000 ? "renew" : null;
}

/** Poll interval while waiting for a purchase to complete. */
export const CHECKOUT_POLL_MS = 3000;

/** True once polling a purchase can stop. */
export function checkoutSettled(status: LicenseCheckoutStatus | undefined): boolean {
  return status === "completed" || status === "expired";
}

/**
 * What a purchase poll means for the upgrade dialog. The license goes to the
 * account that paid, so a purchase paid by another account never installs
 * here, even when this installation already holds a license.
 */
export type CheckoutOutcome = "waiting" | "expired" | "paidByOther" | "installed" | "installing";

export function checkoutOutcome(
  resp: Pick<LicenseCheckoutStatusResponse, "status" | "paid_by_other" | "license">,
): CheckoutOutcome {
  if (!checkoutSettled(resp.status)) return "waiting";
  if (resp.status === "expired") return "expired";
  if (resp.paid_by_other) return "paidByOther";
  return resp.license && isLicensed(resp.license) ? "installed" : "installing";
}

/** i18n key for a license origin label. */
export function licenseOriginKey(origin: LicenseState["origin"]): string {
  return origin === "auto" ? "settings.licenseOriginAuto" : "settings.licenseOriginManual";
}
