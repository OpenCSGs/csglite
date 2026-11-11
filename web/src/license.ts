import { signal } from "@preact/signals";
import { getLicense, isApiError } from "./api/client";
import type { LicenseCloudStatus, LicenseState, LicenseStatus } from "./api/client";
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

/**
 * Device cap enforced by the server for every CSGLite tier. The server uses a
 * fixed constant and ignores any Extra override, so the UI displays the same
 * number instead of trusting the license payload.
 */
export const CSGLITE_DEVICE_LIMIT = 10;

/** The device cap to display, or null when no license is in effect. */
export function deviceLimitOf(state: LicenseState | null): number | null {
  return isLicensed(state) ? CSGLITE_DEVICE_LIMIT : null;
}

export interface CloudImportAvailability {
  enabled: boolean;
  /**
   * i18n key explaining why the button is disabled. Empty when the button is
   * enabled or when the state speaks for itself (an up-to-date license needs
   * no hint).
   */
  hintKey: string;
}

/**
 * Decides whether "import from the cloud" is offered. The conditions mirror
 * the backend guards: the account must be usable, the cloud license must exist
 * and be able to replace the installed one (a new key, or a later expiry), and
 * the installed license must not be environment-managed.
 */
export function cloudImportAvailability(
  state: LicenseState | null,
  cloud: LicenseCloudStatus | null,
): CloudImportAvailability {
  if (!cloud) return { enabled: false, hintKey: "settings.licenseCloudHintUnavailable" };
  if (!cloud.logged_in) {
    return {
      enabled: false,
      hintKey: cloud.reason === "token_invalid" ? "settings.licenseCloudHintToken" : "settings.licenseCloudHintLogin",
    };
  }
  if (!cloud.online) return { enabled: false, hintKey: "settings.licenseCloudHintOffline" };
  if (!cloud.supported) return { enabled: false, hintKey: "settings.licenseCloudHintUnsupported" };
  if (cloud.reason === "server_error") return { enabled: false, hintKey: "settings.licenseCloudHintServerError" };
  if (cloud.env_managed || state?.source === "env") {
    // Defensive: Settings replaces the whole button block when the license is
    // env-managed, so this branch only protects future callers.
    return { enabled: false, hintKey: "settings.licenseCloudHintEnv" };
  }
  if (!cloud.has_license) {
    // An expired license still has a key and a state, so "no subscription"
    // would be misleading: the row exists, it is just no longer usable.
    return {
      enabled: false,
      hintKey:
        cloud.state === "expired"
          ? "settings.licenseCloudExpired"
          : "settings.licenseCloudHintNoSubscription",
    };
  }
  if ((cloud.not_started || cloud.state === "not_started") && isLicensed(state)) {
    return { enabled: false, hintKey: "settings.licenseCloudHintNotStarted" };
  }
  if (isLicensed(state) && !cloudLicenseCanReplace(state, cloud)) {
    // Already installed and the cloud cannot replace it: no hint, the state is
    // exactly what the user wants.
    return { enabled: false, hintKey: "" };
  }
  return { enabled: true, hintKey: "" };
}

/**
 * True when the cloud license may replace the installed one. A different key
 * means a re-issued license or another account, so it is worth importing even
 * when the expiry date did not move forward; the same key must expire later.
 */
export function cloudLicenseCanReplace(state: LicenseState | null, cloud: LicenseCloudStatus | null): boolean {
  const localKey = state?.license?.key;
  const remoteKey = cloud?.license_key;
  if (localKey && remoteKey && localKey !== remoteKey) return true;
  const local = state?.license?.expire_time;
  const remote = cloud?.expire_time;
  if (!local || !remote) return false;
  const localTime = Date.parse(local);
  const remoteTime = Date.parse(remote);
  if (Number.isNaN(localTime) || Number.isNaN(remoteTime)) return false;
  return remoteTime > localTime;
}

/**
 * "used/limit" for the installed license, or null when the cloud numbers
 * belong to a different license.
 */
export function cloudDeviceUsage(
  state: LicenseState | null,
  cloud: LicenseCloudStatus | null,
): { used: number; limit: number; overLimit: boolean } | null {
  if (!cloud || !cloud.has_license || !cloud.license_key || cloud.limit <= 0) return null;
  if (state?.license?.key !== cloud.license_key) return null;
  const used = Math.max(cloud.used, 0);
  // The status endpoint reports the real count, which can exceed the limit when
  // an offline device was reported late. Clamp the displayed number but keep
  // the overflow so the page can warn that new devices are refused.
  return { used: Math.min(used, cloud.limit), limit: cloud.limit, overLimit: used > cloud.limit };
}

/**
 * Maps an import failure onto a localized message. The machine-readable code
 * matters more than the server text; a quota message is only used when the
 * server actually reported a limit.
 */
export function cloudImportErrorMessage(err: unknown): string {
  if (!isApiError(err)) return err instanceof Error ? err.message : t("settings.licenseCloudImportFailed");
  switch (err.code) {
    case "LICENSE-ERR-7": {
      const limit = err.limit ?? 0;
      // Without the server counts there is no quota to show, but the reason is
      // still known, so the message stays localized.
      if (limit <= 0) return t("settings.licenseCloudDeviceLimit");
      const used = Math.min(Math.max(err.used ?? 0, 0), limit);
      return t("settings.licenseCloudLimitReached", used, limit);
    }
    case "LICENSE-ERR-1":
      return t("settings.licenseCloudExpired");
    case "rate_limited":
      return t("settings.licenseCloudRateLimited");
    case "unreachable":
      return t("settings.licenseCloudUnreachable");
    case "no_license":
      return t("settings.licenseCloudNoSubscription");
    case "cloud_not_started":
      return t("settings.licenseCloudNotStarted");
    case "unsupported":
      return t("settings.licenseCloudUnsupported");
    case "token_invalid":
      return t("settings.licenseCloudTokenInvalid");
    case "login_required":
      return t("settings.licenseCloudLoginRequired");
    default:
      return err.message || t("settings.licenseCloudImportFailed");
  }
}
