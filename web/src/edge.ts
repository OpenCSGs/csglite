import type { EdgeApplication, EdgeApplicationStatus, EdgeServingStats, EdgeState, EdgeStatementStatus } from "./api/client";

/** Converts a price in yuan per million tokens (what owners type) to cents. */
export function yuanToCents(value: string): number | null {
  const trimmed = value.trim();
  if (trimmed === "" || !/^\d+(\.\d{1,2})?$/.test(trimmed)) return null;
  return Math.round(Number(trimmed) * 100);
}

/** Formats cents as yuan with two decimals. */
export function centsToYuan(cents: number): string {
  return (Math.round(cents) / 100).toFixed(2);
}

/** Model names must match the gateway's rule: 1-128 of letters, digits, ".", "_" or "-". */
export function isValidEdgeModelName(name: string): boolean {
  return /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(name.trim());
}

/** Suggests a public model name from a local model ID ("org/Qwen3-8B-GGUF" -> "Qwen3-8B-GGUF"). */
export function suggestEdgeModelName(localModelID: string): string {
  const last = localModelID.split("/").pop() ?? localModelID;
  return last.replace(/[^A-Za-z0-9._-]/g, "-").replace(/^[^A-Za-z0-9]+/, "").slice(0, 128);
}

/** True when a gateway failure is its own missing-route page, not an application error. */
export function isEdgeGatewayUnreachableMessage(message: string): boolean {
  const normalized = message.trim().toLowerCase().replace(/[.。]+$/g, "");
  if (normalized === "404 page not found" || normalized === "404 not found" || normalized === "not found") return true;
  if (normalized.includes("cannot connect to the csghub ai gateway")) return true;
  if (message.includes("不能连接 CSGHub AI 网关")) return true;
  return false;
}

export function currentMonth(now = new Date()): string {
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`;
}



/** A "YYYY-MM" month in the reader's language, e.g. "2026年10月" or "October 2026". */
export function formatMonth(month: string, locale: string): string {
  const [year, mon] = month.split("-").map(Number);
  return new Intl.DateTimeFormat(locale, { year: "numeric", month: "long" }).format(new Date(year, mon - 1, 1));
}

export function edgeStateTone(state: EdgeState): string {
  switch (state) {
    case "connected":
      return "bg-emerald-500";
    case "connecting":
      return "bg-amber-400";
    case "error":
    case "signed_out":
      return "bg-red-500";
    default:
      return "bg-gray-300";
  }
}

export function applicationTone(status: EdgeApplicationStatus): string {
  switch (status) {
    case "approved":
      return "bg-emerald-50 text-emerald-700 ring-emerald-200";
    case "pending":
      return "bg-amber-50 text-amber-700 ring-amber-200";
    case "suspended":
      return "bg-orange-50 text-orange-700 ring-orange-200";
    case "rejected":
    case "revoked":
      return "bg-red-50 text-red-700 ring-red-200";
    default:
      return "bg-gray-50 text-gray-600 ring-gray-200";
  }
}

/** Badge colours of a monthly statement status. */
export function statementTone(status: EdgeStatementStatus): string {
  switch (status) {
    case "settled":
      return "bg-emerald-50 text-emerald-700 ring-emerald-200";
    case "unsettled":
      return "bg-amber-50 text-amber-700 ring-amber-200";
    default:
      return "bg-sky-50 text-sky-700 ring-sky-200";
  }
}

/** Applications the owner can still withdraw. */
export function canWithdraw(status: EdgeApplicationStatus): boolean {
  return status === "pending" || status === "approved" || status === "suspended";
}

/** Formats cents as a yuan amount; a positive amount under one fen reads "<¥0.01" rather than a misleading ¥0.00. */
export function formatYuan(cents: number): string {
  if (cents > 0 && cents < 1) return "<¥0.01";
  return `¥${centsToYuan(cents)}`;
}

/** Applications that still hold a model on the gateway (as opposed to history). */
export function isLiveApplication(status: EdgeApplicationStatus): boolean {
  return status === "pending" || status === "approved" || status === "suspended";
}

/**
 * Whether the gateway is serving an application right now, independent of
 * its review status: "none" until approved (or after it leaves review),
 * "stopped" while suspended, then the node and model decide.
 */
export type ServingState = "none" | "stopped" | "offline" | "unavailable" | "serving";

export function servingState(app: Pick<EdgeApplication, "status" | "node_status" | "model_state">): ServingState {
  if (app.status === "suspended") return "stopped";
  if (app.status !== "approved") return "none";
  if (app.node_status !== "online" || app.model_state === "offline") return "offline";
  if (app.model_state === "unavailable") return "unavailable";
  return "serving";
}

export function servingTone(state: ServingState): string {
  switch (state) {
    case "serving":
      return "bg-emerald-500";
    case "unavailable":
      return "bg-amber-400";
    default:
      return "bg-gray-300";
  }
}

export interface EarningRowLike {
  public_model_id: string;
  requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  amount: number;
  owner_earning: number;
}

/** Merges earning rows of one public model (several nodes or re-filed applications) into one line. */
export function mergeEarningsByModel<T extends EarningRowLike>(rows: T[]): EarningRowLike[] {
  const merged = new Map<string, EarningRowLike>();
  for (const row of rows) {
    const cur = merged.get(row.public_model_id) ?? {
      public_model_id: row.public_model_id,
      requests: 0,
      prompt_tokens: 0,
      completion_tokens: 0,
      amount: 0,
      owner_earning: 0,
    };
    cur.requests += row.requests;
    cur.prompt_tokens += row.prompt_tokens;
    cur.completion_tokens += row.completion_tokens;
    cur.amount += row.amount;
    cur.owner_earning += row.owner_earning;
    merged.set(row.public_model_id, cur);
  }
  return [...merged.values()].sort((a, b) => b.owner_earning - a.owner_earning || b.requests - a.requests);
}

/** Formats a duration since an ISO time as a compact "3h 12m" / "5m" / "40s". */
export function sinceLabel(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  return durationLabel(Math.max(0, Math.floor((now - new Date(iso).getTime()) / 1000)));
}

/** Formats seconds as a compact "2d 3h" / "3h 12m" / "5m" / "40s". */
export function durationLabel(secs: number): string {
  const d = Math.floor(secs / 86400);
  const h = Math.floor((secs % 86400) / 3600);
  const m = Math.floor((secs % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${secs}s`;
}

/** Median time to first token as a bucket label: "≤500 ms", "≤2 s", ">5 s"; "" when unknown. */
export function ttftLabel(stats: EdgeServingStats | undefined): string {
  if (!stats || stats.p50_ttft_ms === 0) return "";
  if (stats.p50_ttft_ms < 0) return ">5 s";
  return stats.p50_ttft_ms >= 1000 ? `≤${stats.p50_ttft_ms / 1000} s` : `≤${stats.p50_ttft_ms} ms`;
}

/** Ratio (0-1) as a whole percentage. */
export function percent(ratio: number): string {
  return `${Math.round(ratio * 100)}%`;
}

/** Whether a stats summary has anything to show yet. */
export function hasStats(stats: EdgeServingStats | undefined): stats is EdgeServingStats {
  return !!stats && (stats.requests > 0 || stats.online_seconds > 0);
}

/** Online time over the stats window: the duration, plus the share of the window once it reaches 1%. */
export function onlineLabel(stats: EdgeServingStats): string {
  const dur = durationLabel(stats.online_seconds);
  return stats.uptime_ratio >= 0.01 ? `${dur} (${percent(stats.uptime_ratio)})` : dur;
}
