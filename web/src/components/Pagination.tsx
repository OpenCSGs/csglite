import { t } from "../i18n";

export type PageSize = number | "all";

export const PAGE_SIZE_OPTIONS: PageSize[] = [10, 20, 50, "all"];
export const DEFAULT_PAGE_SIZE: PageSize = 20;

export function pageCount(total: number, pageSize: PageSize): number {
  if (pageSize === "all") return 1;
  return Math.max(1, Math.ceil(total / pageSize));
}

export function clampPage(page: number, total: number, pageSize: PageSize): number {
  return Math.min(Math.max(1, page), pageCount(total, pageSize));
}

export function paginate<T>(items: T[], page: number, pageSize: PageSize): T[] {
  if (pageSize === "all") return items;
  const current = clampPage(page, items.length, pageSize);
  const start = (current - 1) * pageSize;
  return items.slice(start, start + pageSize);
}

function visiblePages(current: number, totalPages: number): (number | "gap")[] {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  }
  const pages = new Set<number>([1, totalPages, current - 1, current, current + 1]);
  const sorted = [...pages].filter((p) => p >= 1 && p <= totalPages).sort((a, b) => a - b);
  const out: (number | "gap")[] = [];
  let prev = 0;
  for (const p of sorted) {
    if (prev && p - prev > 1) out.push("gap");
    out.push(p);
    prev = p;
  }
  return out;
}

export function Pagination({
  total,
  totalLabel,
  page,
  pageSize,
  onPageChange,
  onPageSizeChange,
}: {
  total: number;
  totalLabel: string;
  page: number;
  pageSize: PageSize;
  onPageChange: (page: number) => void;
  onPageSizeChange: (size: PageSize) => void;
}) {
  const totalPages = pageCount(total, pageSize);
  const current = clampPage(page, total, pageSize);
  const navButton =
    "px-2.5 py-1.5 text-xs rounded-md border border-gray-200 bg-white text-gray-600 hover:bg-gray-50 disabled:opacity-40 disabled:cursor-not-allowed transition-colors";

  return (
    <div class="flex items-center justify-between gap-4 flex-wrap px-4 py-3 border-t border-gray-100 bg-gray-50/50 text-sm">
      <span class="text-xs text-gray-500">{totalLabel}</span>
      <div class="flex items-center gap-1.5 flex-wrap">
        <button class={navButton} disabled={current <= 1} onClick={() => onPageChange(1)} title={t("pager.first")}>
          «
        </button>
        <button class={navButton} disabled={current <= 1} onClick={() => onPageChange(current - 1)}>
          {t("pager.prev")}
        </button>
        {visiblePages(current, totalPages).map((p, i) =>
          p === "gap" ? (
            <span key={`gap-${i}`} class="px-1 text-xs text-gray-400">
              …
            </span>
          ) : (
            <button
              key={p}
              onClick={() => onPageChange(p)}
              class={`min-w-[30px] px-2 py-1.5 text-xs rounded-md border transition-colors ${
                p === current
                  ? "border-indigo-600 bg-indigo-600 text-white font-medium"
                  : "border-gray-200 bg-white text-gray-600 hover:bg-gray-50"
              }`}
            >
              {p}
            </button>
          )
        )}
        <button class={navButton} disabled={current >= totalPages} onClick={() => onPageChange(current + 1)}>
          {t("pager.next")}
        </button>
        <button class={navButton} disabled={current >= totalPages} onClick={() => onPageChange(totalPages)} title={t("pager.last")}>
          »
        </button>
      </div>
      <select
        value={String(pageSize)}
        onInput={(e) => {
          const raw = (e.currentTarget as HTMLSelectElement).value;
          onPageSizeChange(raw === "all" ? "all" : Number(raw));
        }}
        class="px-2 py-1.5 text-xs rounded-md border border-gray-200 bg-white text-gray-600 focus:outline-none focus:ring-2 focus:ring-indigo-500"
      >
        {PAGE_SIZE_OPTIONS.map((option) => (
          <option key={String(option)} value={String(option)}>
            {option === "all" ? t("pager.pageSizeAll") : t("pager.pageSize", option)}
          </option>
        ))}
      </select>
    </div>
  );
}
