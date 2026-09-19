import type { ClusterNodeHealth, ClusterNodeState } from "./api/client";

/**
 * Pure helpers shared by the Dashboard cluster section and the Cluster page.
 * They carry no UI so they can be unit tested without a DOM.
 */

const GB = 1024 * 1024 * 1024;

/** Bytes to gigabytes with one decimal, e.g. 25769803776 -> "24.0". */
export function fmtGB(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0.0";
  return (bytes / GB).toFixed(1);
}

/** Bytes to gigabytes for tight table cells: no decimals from 100 GB up. */
export function fmtGBShort(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0";
  const gb = bytes / GB;
  return gb >= 100 ? Math.round(gb).toString() : gb.toFixed(1);
}

/** "used / total GB" in the short form. */
export function fmtGBPairShort(used: number, total: number): string {
  return `${fmtGBShort(used)} / ${fmtGBShort(total)} GB`;
}

/** Strip the commit suffix from a dev version: "v0.9.82-19-g90e3644" -> "v0.9.82". */
export function shortVersion(version: string | undefined): string {
  if (!version) return "—";
  const match = /^(v?\d+\.\d+\.\d+)/.exec(version);
  return match ? match[1] : version;
}

/** True when a host name is just an IP address or a number, which adds nothing next to the node name. */
export function isNoiseHostname(hostname: string | undefined, name: string): boolean {
  if (!hostname) return true;
  if (hostname === name) return true;
  return /^[\d.:]+$/.test(hostname);
}

/** "used / total GB" with one decimal each. */
export function fmtGBPair(used: number, total: number): string {
  return `${fmtGB(used)} / ${fmtGB(total)} GB`;
}

/** Whole-number percentage clamped to 0..100; 0 when the total is unknown. */
export function percentOf(used: number, total: number): number {
  if (!Number.isFinite(used) || !Number.isFinite(total) || total <= 0) return 0;
  return Math.max(0, Math.min(100, Math.round((used / total) * 100)));
}

export type HealthTone = "ok" | "warn" | "error" | "neutral";

/**
 * Visual tone of a node's status dot. An offline node is always red because
 * whatever the gossip health says, it cannot take work right now.
 */
export function healthTone(health: ClusterNodeHealth | string | undefined, online: boolean): HealthTone {
  if (!online) return "error";
  switch (health) {
    case "healthy":
      return "ok";
    case "suspect":
    case "probing":
      return "warn";
    case "down":
      return "error";
    default:
      return "neutral";
  }
}

export const healthDotClass: Record<HealthTone, string> = {
  ok: "bg-green-500",
  warn: "bg-amber-400",
  error: "bg-red-500",
  neutral: "bg-gray-300",
};

/** i18n key for a node health value. */
export function healthKey(health: ClusterNodeHealth | string | undefined, online: boolean): string {
  if (!online) return "cluster.healthOffline";
  switch (health) {
    case "healthy":
      return "cluster.healthHealthy";
    case "suspect":
      return "cluster.healthSuspect";
    case "probing":
      return "cluster.healthProbing";
    case "down":
      return "cluster.healthDown";
    default:
      return "cluster.healthUnknown";
  }
}

/** i18n key for a node state; the empty string for "active" (nothing worth showing). */
export function stateKey(state: ClusterNodeState | string | undefined): string {
  switch (state) {
    case "drain":
      return "cluster.stateDrain";
    case "maintenance":
      return "cluster.stateMaintenance";
    case "active":
      return "cluster.stateActive";
    default:
      return "";
  }
}

export interface NodeLimitLabel {
  /** i18n key: unlimited, within the cap, or at the cap. */
  key: "cluster.nodesUnlimited" | "cluster.nodesOfLimit" | "cluster.nodesAtLimit";
  args: number[];
  atLimit: boolean;
}

/**
 * Which "Nodes n / limit" label to show. `limit` 0 means unlimited; at or above
 * the cap the label carries the Enterprise upgrade hint.
 */
export function nodeLimitLabel(count: number, limit: number): NodeLimitLabel {
  if (!limit || limit <= 0) {
    return { key: "cluster.nodesUnlimited", args: [count], atLimit: false };
  }
  if (count >= limit) {
    return { key: "cluster.nodesAtLimit", args: [count, limit], atLimit: true };
  }
  return { key: "cluster.nodesOfLimit", args: [count, limit], atLimit: false };
}

/** Accepts an 8-digit admission code with optional whitespace or dashes. */
export function normalizeAdmissionCode(input: string): string {
  return input.replace(/[\s-]/g, "");
}

export function isValidAdmissionCode(input: string): boolean {
  return /^\d{8}$/.test(normalizeAdmissionCode(input));
}

/** Seconds until an ISO timestamp, floored at 0; null when unparsable. */
export function secondsUntil(iso: string | undefined, now: number = Date.now()): number | null {
  if (!iso) return null;
  const at = new Date(iso).getTime();
  if (Number.isNaN(at)) return null;
  return Math.max(0, Math.round((at - now) / 1000));
}

/** Format estimated seconds compactly: "<1s", "12s", "1m 05s". */
export function fmtSeconds(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "—";
  if (seconds < 1) return "<1s";
  if (seconds < 60) return `${Math.round(seconds)}s`;
  const m = Math.floor(seconds / 60);
  const s = Math.round(seconds % 60);
  return `${m}m ${String(s).padStart(2, "0")}s`;
}
