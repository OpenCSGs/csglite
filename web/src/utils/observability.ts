export type ObservabilityPeriod = "24h" | "7d" | "30d" | "custom" | "all";

export function observabilityFromForPeriod(period: ObservabilityPeriod, now = Date.now()): string | undefined {
  if (period === "all" || period === "custom") return undefined;
  const hours = period === "24h" ? 24 : period === "7d" ? 24 * 7 : 24 * 30;
  return new Date(now - hours * 60 * 60 * 1000).toISOString();
}

export function formatObservabilityDuration(milliseconds: number): string {
  if (milliseconds < 1000) return `${Math.max(0, Math.round(milliseconds))} ms`;
  return `${(milliseconds / 1000).toFixed(milliseconds < 10000 ? 2 : 1)} s`;
}
