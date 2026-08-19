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

export function formatObservabilityDateTime(value: string): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  const year = String(date.getFullYear()).slice(-2);
  const time = [date.getHours(), date.getMinutes(), date.getSeconds()]
    .map((part) => String(part).padStart(2, "0"))
    .join(":");
  return `${year}/${date.getMonth() + 1}/${date.getDate()} ${time}`;
}

export function formatObservabilityModel(value: string, maxLength = 10): string {
  const model = value.trim();
  if (!model) return "—";
  const characters = Array.from(model);
  if (characters.length <= maxLength) return model;
  return `${characters.slice(0, maxLength).join("")}…`;
}
