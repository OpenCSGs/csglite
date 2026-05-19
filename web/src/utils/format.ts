import { t, locale } from "../i18n";

export function formatNumber(value: number): string {
  return new Intl.NumberFormat().format(value || 0);
}

export function formatDateTime(value?: string): string {
  if (!value) return t("settings.never");
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return t("settings.never");
  return new Intl.DateTimeFormat(locale.value === "zh" ? "zh-CN" : "en-US", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(date);
}

export function formatChartDate(value: string): string {
  const date = new Date(`${value}T00:00:00`);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(locale.value === "zh" ? "zh-CN" : "en-US", {
    month: "numeric",
    day: "numeric",
  }).format(date);
}

export function chartXAxisLabels(xAxis: string[]): Array<{ index: number; label: string }> {
  if (xAxis.length <= 6) {
    return xAxis.map((label, index) => ({ index, label }));
  }
  const maxLabels = 6;
  const seen = new Set<number>();
  const labels: Array<{ index: number; label: string }> = [];
  for (let i = 0; i < maxLabels; i++) {
    const index = Math.round((i / (maxLabels - 1)) * (xAxis.length - 1));
    if (!seen.has(index)) {
      seen.add(index);
      labels.push({ index, label: xAxis[index] });
    }
  }
  return labels;
}
