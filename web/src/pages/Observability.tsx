import { useEffect, useMemo, useState } from "preact/hooks";
import { useLocation, type RoutePropsForPath } from "preact-iso";
import {
  createTraceDatasetExport,
  getObservabilityRequest,
  getObservabilityRequests,
  getObservabilityTrace,
  getObservabilityTraces,
  getTraceDatasetExportJob,
  previewTraceDatasetExport,
} from "../api/client";
import type {
  DatasetExport,
  DatasetExportFormat,
  DatasetExportPreview,
  DatasetExportTraceFilter,
  DatasetRedactionPolicy,
  ObservabilityQuery,
  ObservabilityRequest,
  ObservabilityRequestListResponse,
  ObservabilityTraceDetailResponse,
  ObservabilityTraceListResponse,
} from "../api/client";
import { locale, t } from "../i18n";
import {
  formatObservabilityDateTime,
  formatObservabilityDuration,
  formatObservabilityModel,
  observabilityFromForPeriod,
  type ObservabilityPeriod,
} from "../utils/observability";
import {
  buildTraceFlowSections,
  parseTracePayload,
  type TraceFlowSection,
  type TraceSectionKind,
} from "../utils/tracePayload";

type ObservabilityTab = "requests" | "traces";

const defaultPageSize = 20;

function initialParam(name: string, fallback = ""): string {
  if (typeof window === "undefined") return fallback;
  return new URLSearchParams(window.location.search).get(name) || fallback;
}

function dateTimeLocalValue(value: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
}

function dateTimeISO(value: string): string | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString();
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat(locale.value === "zh" ? "zh-CN" : "en-US").format(value || 0);
}

function prettyBody(value?: string): string {
  if (!value) return "";
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}

function sourceLabel(record: Pick<ObservabilityRequest, "source" | "source_name" | "pool_name" | "member_model">): string {
  if (record.pool_name) {
    return record.member_model ? `${record.pool_name} → ${record.member_model}` : record.pool_name;
  }
  return record.source_name || record.source || "—";
}

function StatusBadge({ status }: { status: string }) {
  const success = status === "completed";
  return (
    <span class={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium ${
      success ? "bg-emerald-50 text-emerald-700 ring-1 ring-emerald-200" : "bg-red-50 text-red-700 ring-1 ring-red-200"
    }`}>
      <span class={`h-1.5 w-1.5 rounded-full ${success ? "bg-emerald-500" : "bg-red-500"}`} />
      {success ? t("observability.statusCompleted") : t("observability.statusFailed")}
    </span>
  );
}

function MetricCard({ label, value, hint, tone }: { label: string; value: string; hint?: string; tone: string }) {
  return (
    <div class="relative overflow-hidden rounded-2xl border border-gray-200 bg-white p-5 shadow-sm">
      <div class={`absolute -right-5 -top-5 h-20 w-20 rounded-full opacity-10 ${tone}`} />
      <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{label}</p>
      <p class="mt-2 text-2xl font-semibold tabular-nums text-gray-900">{value}</p>
      {hint && <p class="mt-1 text-xs text-gray-400">{hint}</p>}
    </div>
  );
}

export function Observability() {
  void locale.value;
  const { route } = useLocation();
  const [tab, setTab] = useState<ObservabilityTab>(initialParam("tab", "requests") === "traces" ? "traces" : "requests");
  const initialPeriod = initialParam("period", "24h") as ObservabilityPeriod;
  const [period, setPeriod] = useState<ObservabilityPeriod>(
    ["24h", "7d", "30d", "custom", "all"].includes(initialPeriod) ? initialPeriod : "24h",
  );
  const [customFrom, setCustomFrom] = useState(dateTimeLocalValue(initialParam("from")));
  const [customTo, setCustomTo] = useState(dateTimeLocalValue(initialParam("to")));
  const [status, setStatus] = useState(initialParam("status"));
  const [model, setModel] = useState(initialParam("model"));
  const [source, setSource] = useState(initialParam("source"));
  const [search, setSearch] = useState(initialParam("q"));
  const [page, setPage] = useState(Math.max(1, Number(initialParam("page", "1")) || 1));
  const [pageSize, setPageSize] = useState(
    [20, 50, 100].includes(Number(initialParam("page_size"))) ? Number(initialParam("page_size")) : defaultPageSize,
  );
  const [requests, setRequests] = useState<ObservabilityRequestListResponse | null>(null);
  const [traces, setTraces] = useState<ObservabilityTraceListResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [refreshKey, setRefreshKey] = useState(0);
  const [requestDetail, setRequestDetail] = useState<ObservabilityRequest | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [showExport, setShowExport] = useState(false);

  const query = useMemo<ObservabilityQuery>(() => ({
    from: period === "custom" ? dateTimeISO(customFrom) : observabilityFromForPeriod(period),
    to: period === "custom" ? dateTimeISO(customTo) : undefined,
    status: status || undefined,
    model: model.trim() || undefined,
    source: source.trim() || undefined,
    q: search.trim() || undefined,
    limit: pageSize,
    offset: (page - 1) * pageSize,
  }), [period, customFrom, customTo, status, model, source, search, page, pageSize]);

  const exportFilter = useMemo<DatasetExportTraceFilter>(() => ({
    from: query.from,
    to: query.to,
    status: query.status,
    model: query.model,
    source: query.source,
    q: query.q,
  }), [query]);

  useEffect(() => {
    const params = new URLSearchParams();
    params.set("tab", tab);
    params.set("period", period);
    if (period === "custom" && query.from) params.set("from", query.from);
    if (period === "custom" && query.to) params.set("to", query.to);
    if (status) params.set("status", status);
    if (model.trim()) params.set("model", model.trim());
    if (source.trim()) params.set("source", source.trim());
    if (search.trim()) params.set("q", search.trim());
    if (page > 1) params.set("page", String(page));
    if (pageSize !== defaultPageSize) params.set("page_size", String(pageSize));
    window.history.replaceState(null, "", `/observability?${params}`);
  }, [tab, period, status, model, source, search, page, pageSize, query.from, query.to]);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    const load = tab === "requests" ? getObservabilityRequests(query) : getObservabilityTraces(query);
    load.then((data) => {
      if (!active) return;
      if (tab === "requests") setRequests(data as ObservabilityRequestListResponse);
      else setTraces(data as ObservabilityTraceListResponse);
    }).catch((err) => {
      if (active) setError(err?.message || t("observability.loadFailed"));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [tab, query, refreshKey]);

  function changeFilter(action: () => void) {
    action();
    setPage(1);
  }

  async function openRequest(id: string) {
    setDetailLoading(true);
    setError("");
    try {
      setRequestDetail(await getObservabilityRequest(id));
    } catch (err: any) {
      setError(err?.message || t("observability.detailLoadFailed"));
    } finally {
      setDetailLoading(false);
    }
  }

  function navigateToTrace(traceID: string) {
    route(`/observability/traces/${encodeURIComponent(traceID)}`);
  }

  const successRate = requests?.summary.requests
    ? (requests.summary.succeeded / requests.summary.requests) * 100
    : 0;

  return (
    <div class="page-shell space-y-6">
      <header class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <div class="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.18em] text-indigo-500">
            <span class="h-px w-7 bg-indigo-300" />
            {t("observability.eyebrow")}
          </div>
          <h1 class="text-2xl font-bold text-gray-950">{t("observability.title")}</h1>
          <p class="mt-2 max-w-2xl text-sm leading-6 text-gray-500">{t("observability.subtitle")}</p>
        </div>
        <div class="flex flex-wrap gap-2">
          {tab === "traces" && (
            <button
              type="button"
              disabled={(traces?.total || 0) === 0}
              onClick={() => setShowExport(true)}
              class="inline-flex items-center justify-center rounded-xl bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white shadow-sm transition hover:bg-indigo-700 disabled:opacity-40"
            >
              {t("observability.exportDataset")}
            </button>
          )}
          <button
            type="button"
            disabled={loading}
            onClick={() => setRefreshKey((value) => value + 1)}
            class="inline-flex items-center justify-center gap-2 rounded-xl border border-gray-200 bg-white px-4 py-2.5 text-sm font-medium text-gray-700 shadow-sm transition hover:border-indigo-200 hover:text-indigo-700 disabled:opacity-50"
          >
            <svg class={`h-4 w-4 ${loading ? "animate-spin" : ""}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M20 11a8 8 0 10-2.34 5.66M20 4v7h-7" />
            </svg>
            {loading ? t("observability.refreshing") : t("observability.refresh")}
          </button>
        </div>
      </header>

      <div class="inline-flex rounded-xl border border-gray-200 bg-white p-1 shadow-sm">
        {(["requests", "traces"] as ObservabilityTab[]).map((value) => (
          <button
            key={value}
            type="button"
            onClick={() => { setTab(value); setPage(1); }}
            class={`rounded-lg px-5 py-2 text-sm font-medium transition ${
              tab === value ? "bg-indigo-600 text-white shadow-sm" : "text-gray-500 hover:bg-indigo-50 hover:text-indigo-700"
            }`}
          >
            {t(value === "requests" ? "observability.tabRequests" : "observability.tabTraces")}
          </button>
        ))}
      </div>

      {tab === "requests" && (
        <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <MetricCard label={t("observability.metricRequests")} value={formatNumber(requests?.summary.requests || 0)} tone="bg-indigo-500" />
          <MetricCard label={t("observability.metricSuccessRate")} value={`${successRate.toFixed(1)}%`} tone="bg-emerald-500" />
          <MetricCard label={t("observability.metricAverageLatency")} value={formatObservabilityDuration(requests?.summary.average_latency_ms || 0)} tone="bg-amber-500" />
          <MetricCard label={t("observability.metricTokens")} value={formatNumber(requests?.summary.total_tokens || 0)} tone="bg-violet-500" />
        </div>
      )}

      <section class="rounded-2xl border border-gray-200 bg-white shadow-sm">
        <div class="grid gap-3 border-b border-gray-100 p-4 md:grid-cols-2 xl:grid-cols-[auto_1fr_1fr_1fr_1.25fr]">
          <select
            value={period}
            onInput={(event) => changeFilter(() => {
              const next = (event.currentTarget as HTMLSelectElement).value as ObservabilityPeriod;
              setPeriod(next);
              if (next === "custom" && !customFrom && !customTo) {
                const now = new Date();
                setCustomTo(dateTimeLocalValue(now.toISOString()));
                setCustomFrom(dateTimeLocalValue(new Date(now.getTime() - 24 * 60 * 60 * 1000).toISOString()));
              }
            })}
            class="rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-700 focus:ring-2 focus:ring-indigo-500"
          >
            <option value="24h">{t("observability.period24h")}</option>
            <option value="7d">{t("observability.period7d")}</option>
            <option value="30d">{t("observability.period30d")}</option>
            <option value="custom">{t("observability.periodCustom")}</option>
            <option value="all">{t("observability.periodAll")}</option>
          </select>
          <select value={status} onInput={(event) => changeFilter(() => setStatus((event.currentTarget as HTMLSelectElement).value))} class="rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-700 focus:ring-2 focus:ring-indigo-500">
            <option value="">{t("observability.statusAll")}</option>
            <option value="completed">{t("observability.statusCompleted")}</option>
            <option value="failed">{t("observability.statusFailed")}</option>
          </select>
          <input value={model} onInput={(event) => changeFilter(() => setModel((event.currentTarget as HTMLInputElement).value))} placeholder={t("observability.filterModel")} class="rounded-lg border border-gray-200 px-3 py-2 text-sm focus:ring-2 focus:ring-indigo-500" />
          <input value={source} onInput={(event) => changeFilter(() => setSource((event.currentTarget as HTMLInputElement).value))} placeholder={t("observability.filterSource")} class="rounded-lg border border-gray-200 px-3 py-2 text-sm focus:ring-2 focus:ring-indigo-500" />
          <div class="relative">
            <svg class="absolute left-3 top-2.5 h-4 w-4 text-gray-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg>
            <input value={search} onInput={(event) => changeFilter(() => setSearch((event.currentTarget as HTMLInputElement).value))} placeholder={t("observability.filterSearch")} class="w-full rounded-lg border border-gray-200 py-2 pl-9 pr-3 text-sm focus:ring-2 focus:ring-indigo-500" />
          </div>
        </div>
        {period === "custom" && (
          <div class="grid gap-3 border-b border-gray-100 bg-indigo-50/30 px-4 py-3 sm:grid-cols-2">
            <label class="space-y-1 text-xs font-medium text-gray-600">
              <span>{t("observability.timeFrom")}</span>
              <input
                type="datetime-local"
                value={customFrom}
                max={customTo || undefined}
                onInput={(event) => changeFilter(() => setCustomFrom((event.currentTarget as HTMLInputElement).value))}
                class="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm font-normal text-gray-700 focus:ring-2 focus:ring-indigo-500"
              />
            </label>
            <label class="space-y-1 text-xs font-medium text-gray-600">
              <span>{t("observability.timeTo")}</span>
              <input
                type="datetime-local"
                value={customTo}
                min={customFrom || undefined}
                onInput={(event) => changeFilter(() => setCustomTo((event.currentTarget as HTMLInputElement).value))}
                class="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm font-normal text-gray-700 focus:ring-2 focus:ring-indigo-500"
              />
            </label>
          </div>
        )}

        {error && <div class="m-4 rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">{error}</div>}
        {tab === "requests" ? (
          <RequestTable data={requests} loading={loading} onOpen={openRequest} onOpenTrace={navigateToTrace} />
        ) : (
          <TraceTable
            data={traces}
            loading={loading}
            onOpen={navigateToTrace}
            onOpenFull={navigateToTrace}
          />
        )}
        {(() => {
          const total = tab === "requests" ? requests?.total || 0 : traces?.total || 0;
          const pages = Math.max(1, Math.ceil(total / pageSize));
          const first = total === 0 ? 0 : (page - 1) * pageSize + 1;
          const last = Math.min(page * pageSize, total);
          return (
            <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 bg-gray-50/50 px-4 py-3">
              <span class="text-xs text-gray-500">{t("observability.pageRange", first, last, total)}</span>
              <div class="flex items-center gap-2">
                <select
                  value={pageSize}
                  onInput={(event) => { setPageSize(Number((event.currentTarget as HTMLSelectElement).value)); setPage(1); }}
                  aria-label={t("observability.pageSize")}
                  class="rounded-lg border border-gray-200 bg-white px-2 py-1.5 text-xs text-gray-600"
                >
                  {[20, 50, 100].map((size) => <option key={size} value={size}>{t("observability.perPage", size)}</option>)}
                </select>
                <button type="button" disabled={page <= 1} onClick={() => setPage((value) => Math.max(1, value - 1))} class="rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-xs text-gray-600 hover:bg-gray-50 disabled:opacity-40">{t("pager.prev")}</button>
                <span class="min-w-[64px] text-center text-xs tabular-nums text-gray-500">{page} / {pages}</span>
                <button type="button" disabled={page >= pages} onClick={() => setPage((value) => Math.min(pages, value + 1))} class="rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-xs text-gray-600 hover:bg-gray-50 disabled:opacity-40">{t("pager.next")}</button>
              </div>
            </div>
          );
        })()}
      </section>

      {(requestDetail || detailLoading) && (
        <DetailDrawer
          request={requestDetail}
          loading={detailLoading}
          onClose={() => setRequestDetail(null)}
          onOpenTrace={navigateToTrace}
        />
      )}
      {showExport && (
        <DatasetExportWizard
          filter={exportFilter}
          matchingTotal={traces?.total || 0}
          onClose={() => setShowExport(false)}
        />
      )}
    </div>
  );
}

function RequestTable({ data, loading, onOpen, onOpenTrace }: {
  data: ObservabilityRequestListResponse | null;
  loading: boolean;
  onOpen: (id: string) => void;
  onOpenTrace: (id: string) => void;
}) {
  const rows = data?.items || [];
  if (!loading && rows.length === 0) return <EmptyState />;
  return (
    <div class="overflow-x-auto">
      <table class="w-full min-w-[1120px] text-left text-sm">
        <thead class="bg-gray-50/80 text-xs uppercase tracking-wide text-gray-400">
          <tr>
            <th class="px-4 py-3">{t("observability.columnModel")}</th>
            <th class="px-4 py-3">{t("observability.columnTime")}</th>
            <th class="px-4 py-3">{t("observability.columnStatus")}</th>
            <th class="px-4 py-3">{t("observability.columnRoute")}</th>
            <th class="px-4 py-3">{t("observability.columnLatency")}</th>
            <th class="px-4 py-3">{t("observability.columnCacheRead")}</th>
            <th class="px-4 py-3">{t("observability.columnTokens")}</th>
            <th class="px-4 py-3">{t("observability.columnCacheHitRate")}</th>
            <th class="px-4 py-3">{t("observability.columnTrace")}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100">
          {rows.map((row) => (
            <tr key={row.id} onClick={() => onOpen(row.id)} class="cursor-pointer transition hover:bg-indigo-50/40">
              <td class="max-w-[190px] truncate px-4 py-3 font-medium text-gray-900" title={row.model}>{formatObservabilityModel(row.model)}</td>
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-500">{formatObservabilityDateTime(row.started_at)}</td>
              <td class="px-4 py-3"><StatusBadge status={row.status} /></td>
              <td class="max-w-[190px] truncate px-4 py-3 text-gray-600" title={sourceLabel(row)}>{sourceLabel(row)}</td>
              <td class="whitespace-nowrap px-4 py-3 tabular-nums text-gray-600">
                {formatObservabilityDuration(row.duration_ms)}
                {row.stream && <span class="block text-[11px] text-gray-400">TTFT {formatObservabilityDuration(row.first_token_latency_ms)}</span>}
              </td>
              <td class="whitespace-nowrap px-4 py-3 tabular-nums text-gray-600">{formatCacheTokens(row, row.cache_read_input_tokens)}</td>
              <td class="whitespace-nowrap px-4 py-3 tabular-nums text-gray-600">{formatNumber(row.total_tokens)}</td>
              <td class="whitespace-nowrap px-4 py-3 tabular-nums text-gray-600" title={
                row.cache_eligible_input_tokens > 0
                  ? `${t("observability.cacheReadTokens")}: ${formatNumber(row.cache_read_input_tokens)} / ${formatNumber(row.cache_eligible_input_tokens)}`
                  : undefined
              }>
                {formatCacheHitRate(row)}
              </td>
              <td class="px-4 py-3">
                <button type="button" onClick={(event) => { event.stopPropagation(); onOpenTrace(row.trace_id); }} class="max-w-[130px] truncate font-mono text-xs text-indigo-600 hover:text-indigo-800" title={row.trace_id}>
                  {row.trace_id.replace(/^trace-/, "").slice(0, 10)}
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {loading && <LoadingRow />}
    </div>
  );
}

function TraceTable({ data, loading, onOpen, onOpenFull }: {
  data: ObservabilityTraceListResponse | null;
  loading: boolean;
  onOpen: (id: string) => void;
  onOpenFull: (id: string) => void;
}) {
  const rows = data?.items || [];
  if (!loading && rows.length === 0) return <EmptyState />;
  return (
    <div class="overflow-x-auto">
      <table class="w-full min-w-[900px] text-left text-sm">
        <thead class="bg-gray-50/80 text-xs uppercase tracking-wide text-gray-400">
          <tr>
            <th class="px-4 py-3">{t("observability.columnTrace")}</th>
            <th class="px-4 py-3">{t("observability.columnStatus")}</th>
            <th class="px-4 py-3">{t("observability.columnTime")}</th>
            <th class="px-4 py-3">{t("observability.columnThread")}</th>
            <th class="px-4 py-3">{t("observability.columnRequests")}</th>
            <th class="px-4 py-3">{t("observability.columnModel")}</th>
            <th class="px-4 py-3">{t("observability.columnLatency")}</th>
            <th class="px-4 py-3">{t("observability.columnTokens")}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100">
          {rows.map((row) => (
            <tr key={row.trace_id} onClick={() => onOpen(row.trace_id)} class="cursor-pointer transition hover:bg-indigo-50/40">
              <td class="max-w-[150px] px-4 py-3">
                <button
                  type="button"
                  onClick={(event) => { event.stopPropagation(); onOpenFull(row.trace_id); }}
                  class="block max-w-[150px] truncate font-mono text-xs font-medium text-indigo-600 hover:text-indigo-800 hover:underline"
                  title={row.trace_id}
                >
                  {row.trace_id}
                </button>
              </td>
              <td class="px-4 py-3"><StatusBadge status={row.status} /></td>
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-500">{formatObservabilityDateTime(row.started_at)}</td>
              <td class="max-w-[150px] truncate px-4 py-3 font-mono text-xs text-gray-500" title={row.thread_id}>{row.thread_id || "—"}</td>
              <td class="px-4 py-3 tabular-nums text-gray-700">{row.request_count}</td>
              <td class="max-w-[220px] truncate px-4 py-3 text-gray-700" title={row.models.join(", ")}>{formatObservabilityModel(row.models.join(", "))}</td>
              <td class="px-4 py-3 tabular-nums text-gray-600">{formatObservabilityDuration(row.duration_ms)}</td>
              <td class="px-4 py-3 tabular-nums text-gray-600">{formatNumber(row.total_tokens)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {loading && <LoadingRow />}
    </div>
  );
}

function EmptyState() {
  return (
    <div class="flex min-h-[300px] flex-col items-center justify-center px-6 text-center">
      <div class="flex h-12 w-12 items-center justify-center rounded-2xl bg-indigo-50 text-indigo-500">
        <svg class="h-6 w-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 19V9m5 10V5m5 14v-7m5 7V3" /></svg>
      </div>
      <p class="mt-4 font-medium text-gray-700">{t("observability.empty")}</p>
      <p class="mt-1 text-sm text-gray-400">{t("observability.emptyHint")}</p>
    </div>
  );
}

function LoadingRow() {
  return <div class="border-t border-gray-100 px-4 py-6 text-center text-sm text-gray-400">{t("observability.loading")}</div>;
}

function DatasetExportWizard({ filter, matchingTotal, onClose }: {
  filter: DatasetExportTraceFilter;
  matchingTotal: number;
  onClose: () => void;
}) {
  const [step, setStep] = useState<1 | 2>(1);
  const [exportFrom, setExportFrom] = useState(dateTimeLocalValue(filter.from || ""));
  const [exportTo, setExportTo] = useState(dateTimeLocalValue(filter.to || (filter.from ? new Date().toISOString() : "")));
  const [exportStatus, setExportStatus] = useState(filter.status || "");
  const [exportModel, setExportModel] = useState(filter.model || "");
  const [exportSource, setExportSource] = useState(filter.source || "");
  const [exportQuery, setExportQuery] = useState(filter.q || "");
  const [format, setFormat] = useState<DatasetExportFormat>("openai_messages");
  const [policy, setPolicy] = useState<DatasetRedactionPolicy>("redact");
  const [datasetName, setDatasetName] = useState("");
  const [preview, setPreview] = useState<DatasetExportPreview | null>(null);
  const [previewing, setPreviewing] = useState(true);
  const [confirmed, setConfirmed] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [result, setResult] = useState<DatasetExport | null>(null);
  const [error, setError] = useState("");

  const selection = useMemo(() => ({
    filter: {
      from: dateTimeISO(exportFrom),
      to: dateTimeISO(exportTo),
      status: exportStatus || undefined,
      model: exportModel.trim() || undefined,
      source: exportSource.trim() || undefined,
      q: exportQuery.trim() || undefined,
    },
  }), [exportFrom, exportTo, exportStatus, exportModel, exportSource, exportQuery]);

  useEffect(() => {
    if (step !== 2) return;
    let active = true;
    const timer = window.setTimeout(() => {
      setPreviewing(true);
      setError("");
      previewTraceDatasetExport({
        ...selection,
        format,
        redaction_policy: policy,
      }).then((data) => {
        if (active) setPreview(data);
      }).catch((err: any) => {
        if (active) setError(err?.message || t("observability.exportPreviewFailed"));
      }).finally(() => {
        if (active) setPreviewing(false);
      });
    }, 150);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [step, selection, format, policy]);

  async function exportDataset() {
    setExporting(true);
    setError("");
    try {
      let job = await createTraceDatasetExport({
        ...selection,
        format,
        redaction_policy: policy,
        confirmed: policy !== "detect" || confirmed,
        dataset_name: datasetName.trim() || undefined,
      });
      while (job.status === "queued" || job.status === "running") {
        await new Promise((resolve) => window.setTimeout(resolve, 400));
        job = await getTraceDatasetExportJob(job.id);
      }
      if (job.status !== "completed" || !job.export) {
        throw new Error(job.error || t("observability.exportFailed"));
      }
      setResult(job.export);
    } catch (err: any) {
      setError(err?.message || t("observability.exportFailed"));
    } finally {
      setExporting(false);
    }
  }

  const requiresConfirmation = policy === "detect" && (preview?.risks.length || 0) > 0;
  const canExport = !!preview?.exported && !previewing && !exporting && (!requiresConfirmation || confirmed);
  const invalidRange = !!exportFrom && !!exportTo && new Date(exportFrom).getTime() > new Date(exportTo).getTime();
  const canContinue = !!exportFrom && !!exportTo && !invalidRange;
  const formats: Array<{ value: DatasetExportFormat; label: string }> = [
    { value: "openai_messages", label: t("observability.exportFormat.openai") },
    { value: "sharegpt", label: t("observability.exportFormat.sharegpt") },
    { value: "alpaca", label: t("observability.exportFormat.alpaca") },
    { value: "prompt_completion", label: t("observability.exportFormat.completion") },
  ];

  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-950/40 p-4" onClick={() => { if (!exporting) onClose(); }}>
      <section class="max-h-[90vh] w-full max-w-3xl overflow-y-auto rounded-2xl bg-white shadow-2xl" onClick={(event) => event.stopPropagation()}>
        <header class="sticky top-0 z-10 flex items-start justify-between border-b border-gray-200 bg-white px-6 py-5">
          <div>
            <h2 class="text-lg font-semibold text-gray-950">{t("observability.exportTitle")}</h2>
            <p class="mt-1 text-sm text-gray-500">{t("observability.exportStep", step, 2)}</p>
          </div>
          <button type="button" disabled={exporting} onClick={onClose} class="rounded-lg p-2 text-gray-400 hover:bg-gray-100 disabled:opacity-40" aria-label={t("observability.close")}>×</button>
        </header>
        <div class="space-y-5 p-6">
          {result ? (
            <div class="space-y-4">
              <div class="rounded-xl border border-emerald-200 bg-emerald-50 p-4 text-sm text-emerald-800">
                <p class="font-semibold">{t("observability.exportCompleted")}</p>
                <p class="mt-1 break-all font-mono text-xs">{result.dataset_id}</p>
              </div>
              <div class="flex flex-wrap gap-3">
                <a href="/datasets" class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700">{t("observability.openLocalDataset")}</a>
                <a href={result.download_url} class="rounded-lg border border-gray-200 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50">{t("observability.downloadExport")}</a>
                <button type="button" onClick={onClose} class="rounded-lg border border-gray-200 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50">{t("observability.close")}</button>
              </div>
            </div>
          ) : step === 1 ? (
            <DatasetExportFilterStep
              matchingTotal={matchingTotal}
              from={exportFrom}
              to={exportTo}
              status={exportStatus}
              model={exportModel}
              source={exportSource}
              query={exportQuery}
              invalidRange={invalidRange}
              canContinue={canContinue}
              onFrom={setExportFrom}
              onTo={setExportTo}
              onStatus={setExportStatus}
              onModel={setExportModel}
              onSource={setExportSource}
              onQuery={setExportQuery}
              onCancel={onClose}
              onNext={() => {
                setPreview(null);
                setPreviewing(true);
                setError("");
                setStep(2);
              }}
            />
          ) : (
            <>
              <div class="grid gap-4 md:grid-cols-2">
                <label class="space-y-1.5 text-sm">
                  <span class="font-medium text-gray-700">{t("observability.exportFormat")}</span>
                  <select value={format} onInput={(event) => setFormat((event.currentTarget as HTMLSelectElement).value as DatasetExportFormat)} class="w-full rounded-lg border border-gray-200 px-3 py-2">
                    {formats.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
                  </select>
                </label>
                <label class="space-y-1.5 text-sm">
                  <span class="font-medium text-gray-700">{t("observability.redactionPolicy")}</span>
                  <select value={policy} onInput={(event) => { setPolicy((event.currentTarget as HTMLSelectElement).value as DatasetRedactionPolicy); setConfirmed(false); }} class="w-full rounded-lg border border-gray-200 px-3 py-2">
                    <option value="redact">{t("observability.redaction.redact")}</option>
                    <option value="exclude">{t("observability.redaction.exclude")}</option>
                    <option value="detect">{t("observability.redaction.detect")}</option>
                  </select>
                </label>
              </div>
              <label class="block space-y-1.5 text-sm">
                <span class="font-medium text-gray-700">{t("observability.localDatasetName")}</span>
                <input value={datasetName} onInput={(event) => setDatasetName((event.currentTarget as HTMLInputElement).value)} placeholder={t("observability.localDatasetNameHint")} class="w-full rounded-lg border border-gray-200 px-3 py-2" />
              </label>
              <section class="rounded-xl border border-gray-200 bg-gray-50 p-4">
                <h3 class="text-sm font-semibold text-gray-900">{t("observability.exportPreview")}</h3>
                {previewing ? <p class="mt-3 text-sm text-gray-400">{t("observability.loading")}</p> : preview && (
                  <div class="mt-3 space-y-3">
                    <p class="text-xs text-gray-500">{t("observability.exportMatched", preview.selected)}</p>
                    <div class="grid grid-cols-3 gap-3 text-center text-sm">
                      <div class="rounded-lg bg-white p-3"><strong class="block text-lg text-emerald-700">{preview.exported}</strong>{t("observability.exportable")}</div>
                      <div class="rounded-lg bg-white p-3"><strong class="block text-lg text-amber-700">{preview.degraded}</strong>{t("observability.degraded")}</div>
                      <div class="rounded-lg bg-white p-3"><strong class="block text-lg text-red-700">{preview.excluded}</strong>{t("observability.excluded")}</div>
                    </div>
                    {preview.risks.length > 0 && (
                      <div class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800">
                        {t("observability.risksFound", preview.risks.reduce((sum, risk) => sum + risk.count, 0))}
                        <span class="ml-2">{preview.risks.map((risk) => `${risk.type}: ${risk.count}`).join(" · ")}</span>
                      </div>
                    )}
                    {preview.sample !== undefined && <pre class="max-h-52 overflow-auto rounded-lg bg-gray-950 p-3 text-xs text-gray-200">{JSON.stringify(preview.sample, null, 2)}</pre>}
                  </div>
                )}
              </section>
              {requiresConfirmation && (
                <label class="flex items-start gap-3 rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-800">
                  <input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.currentTarget.checked)} class="mt-0.5 h-4 w-4 rounded border-red-300" />
                  <span>{t("observability.confirmSensitiveExport")}</span>
                </label>
              )}
              {error && <div class="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700">{error}</div>}
              <div class="flex justify-end gap-3">
                <button type="button" disabled={exporting} onClick={() => setStep(1)} class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 disabled:opacity-40">{t("observability.back")}</button>
                <button type="button" disabled={!canExport} onClick={() => void exportDataset()} class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-40">
                  {exporting ? t("observability.exporting") : t("observability.createLocalDataset")}
                </button>
              </div>
            </>
          )}
        </div>
      </section>
    </div>
  );
}

function DatasetExportFilterStep({
  matchingTotal,
  from,
  to,
  status,
  model,
  source,
  query,
  invalidRange,
  canContinue,
  onFrom,
  onTo,
  onStatus,
  onModel,
  onSource,
  onQuery,
  onCancel,
  onNext,
}: {
  matchingTotal: number;
  from: string;
  to: string;
  status: string;
  model: string;
  source: string;
  query: string;
  invalidRange: boolean;
  canContinue: boolean;
  onFrom: (value: string) => void;
  onTo: (value: string) => void;
  onStatus: (value: string) => void;
  onModel: (value: string) => void;
  onSource: (value: string) => void;
  onQuery: (value: string) => void;
  onCancel: () => void;
  onNext: () => void;
}) {
  const [showValidation, setShowValidation] = useState(false);
  const missingFrom = !from;
  const missingTo = !to;
  return (
    <>
      <section class="space-y-4 rounded-xl border border-gray-200 bg-gray-50 p-4">
          <div>
            <h3 class="text-sm font-semibold text-gray-900">{t("observability.exportConditions")}</h3>
            <p class="mt-1 text-xs text-gray-500">{t("observability.exportAllMatching", matchingTotal)}</p>
          </div>
          <div class="grid gap-4 md:grid-cols-2">
            <label class="space-y-1.5 text-sm">
              <span class="font-medium text-gray-700">{t("observability.timeFrom")} <span class="text-red-500">*</span></span>
              <input required aria-invalid={showValidation && missingFrom} type="datetime-local" value={from} max={to || undefined} onInput={(event) => onFrom((event.currentTarget as HTMLInputElement).value)} class={`w-full rounded-lg border bg-white px-3 py-2 ${showValidation && missingFrom ? "border-red-400" : "border-gray-200"}`} />
              {showValidation && missingFrom && <span class="block text-xs text-red-600">{t("observability.requiredField")}</span>}
            </label>
            <label class="space-y-1.5 text-sm">
              <span class="font-medium text-gray-700">{t("observability.timeTo")} <span class="text-red-500">*</span></span>
              <input required aria-invalid={showValidation && missingTo} type="datetime-local" value={to} min={from || undefined} onInput={(event) => onTo((event.currentTarget as HTMLInputElement).value)} class={`w-full rounded-lg border bg-white px-3 py-2 ${showValidation && missingTo ? "border-red-400" : "border-gray-200"}`} />
              {showValidation && missingTo && <span class="block text-xs text-red-600">{t("observability.requiredField")}</span>}
            </label>
            <label class="space-y-1.5 text-sm">
              <span class="font-medium text-gray-700">{t("observability.columnStatus")}</span>
              <select value={status} onInput={(event) => onStatus((event.currentTarget as HTMLSelectElement).value)} class="w-full rounded-lg border border-gray-200 bg-white px-3 py-2">
                <option value="">{t("observability.statusAll")}</option>
                <option value="completed">{t("observability.statusCompleted")}</option>
                <option value="failed">{t("observability.statusFailed")}</option>
              </select>
            </label>
            <label class="space-y-1.5 text-sm">
              <span class="font-medium text-gray-700">{t("observability.columnModel")}</span>
              <input value={model} onInput={(event) => onModel((event.currentTarget as HTMLInputElement).value)} placeholder={t("observability.filterModel")} class="w-full rounded-lg border border-gray-200 bg-white px-3 py-2" />
            </label>
            <label class="space-y-1.5 text-sm">
              <span class="font-medium text-gray-700">{t("observability.columnRoute")}</span>
              <input value={source} onInput={(event) => onSource((event.currentTarget as HTMLInputElement).value)} placeholder={t("observability.filterSource")} class="w-full rounded-lg border border-gray-200 bg-white px-3 py-2" />
            </label>
            <label class="space-y-1.5 text-sm">
              <span class="font-medium text-gray-700">{t("observability.filterKeyword")}</span>
              <input value={query} onInput={(event) => onQuery((event.currentTarget as HTMLInputElement).value)} placeholder={t("observability.filterSearch")} class="w-full rounded-lg border border-gray-200 bg-white px-3 py-2" />
            </label>
          </div>
          {showValidation && invalidRange && <p class="text-xs text-red-600">{t("observability.invalidTimeRange")}</p>}
      </section>

      <div class="flex justify-end gap-3">
        <button type="button" onClick={onCancel} class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700">{t("observability.cancel")}</button>
        <button
          type="button"
          onClick={() => {
            setShowValidation(true);
            if (canContinue) onNext();
          }}
          class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700"
        >
          {t("observability.next")}
        </button>
      </div>
    </>
  );
}

function DetailDrawer({ request, loading, onClose, onOpenTrace }: {
  request: ObservabilityRequest | null;
  loading: boolean;
  onClose: () => void;
  onOpenTrace: (id: string) => void;
}) {
  return (
    <div class="fixed inset-0 z-50 flex justify-end bg-gray-950/30 backdrop-blur-[1px]" onClick={onClose}>
      <aside class="h-full w-full max-w-3xl overflow-y-auto bg-gray-50 shadow-2xl" onClick={(event) => event.stopPropagation()}>
        <div class="sticky top-0 z-10 flex items-center justify-between border-b border-gray-200 bg-white/95 px-6 py-4 backdrop-blur">
          <div class="min-w-0">
            <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{t("observability.requestDetail")}</p>
            <p class="mt-1 truncate font-mono text-sm text-gray-800">{request?.id || ""}</p>
          </div>
          <button type="button" onClick={onClose} class="rounded-lg p-2 text-gray-400 hover:bg-gray-100 hover:text-gray-700" aria-label={t("observability.close")}>
            <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 6l12 12M18 6 6 18" /></svg>
          </button>
        </div>
        {loading ? <div class="p-12 text-center text-sm text-gray-400">{t("observability.loading")}</div> : request ? (
          <RequestDetail request={request} onOpenTrace={onOpenTrace} />
        ) : null}
      </aside>
    </div>
  );
}

function RequestDetail({ request, onOpenTrace }: { request: ObservabilityRequest; onOpenTrace: (id: string) => void }) {
  const metadata = [
    [t("observability.columnStatus"), <StatusBadge status={request.status} />],
    [t("observability.columnModel"), request.model || "—"],
    [t("observability.columnRoute"), sourceLabel(request)],
    [t("observability.columnEndpoint"), `${request.method} ${request.path}`],
    [t("observability.columnLatency"), formatObservabilityDuration(request.duration_ms)],
    ["TTFT", formatObservabilityDuration(request.first_token_latency_ms)],
    [t("observability.inputTokens"), formatNumber(request.input_tokens)],
    [t("observability.outputTokens"), formatNumber(request.output_tokens)],
    [t("observability.columnCacheRead"), formatCacheTokens(request, request.cache_read_input_tokens)],
    [t("observability.columnTokens"), formatNumber(request.total_tokens)],
    [t("observability.columnCacheHitRate"), formatCacheHitRate(request)],
    [t("observability.caller"), request.api_key_name || "—"],
    [t("observability.columnTime"), formatObservabilityDateTime(request.started_at)],
  ];
  return (
    <div class="space-y-5 p-6">
      <div class="grid gap-3 sm:grid-cols-2">
        {metadata.map(([label, value]) => (
          <div key={String(label)} class="rounded-xl border border-gray-200 bg-white p-4">
            <p class="text-xs text-gray-400">{label}</p>
            <div class="mt-1.5 break-all text-sm font-medium text-gray-800">{value}</div>
          </div>
        ))}
      </div>
      <button type="button" onClick={() => onOpenTrace(request.trace_id)} class="w-full rounded-xl border border-indigo-200 bg-indigo-50 px-4 py-3 text-left text-sm text-indigo-700 hover:bg-indigo-100">
        <span class="text-xs text-indigo-400">{t("observability.columnTrace")}</span>
        <span class="mt-1 block break-all font-mono">{request.trace_id}</span>
      </button>
      <div class="grid gap-3 sm:grid-cols-2">
        {request.request_id && <CopyableID label={t("observability.correlationRequestID")} value={request.request_id} />}
        {request.b3_trace_id && <CopyableID label={t("observability.b3TraceID")} value={request.b3_trace_id} />}
      </div>
      {request.error_message && <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">{request.error_message}</div>}
      <PayloadPanel title={t("observability.requestPayload")} value={request.request_body} truncated={request.request_body_truncated} />
      <PayloadPanel title={t("observability.responsePayload")} value={request.response_body} truncated={request.response_body_truncated} />
    </div>
  );
}

function PayloadPanel({ title, value, truncated }: { title: string; value?: string; truncated: boolean }) {
  async function copy() {
    await navigator.clipboard.writeText(value || "");
  }
  return (
    <section class="overflow-hidden rounded-xl border border-gray-200 bg-white">
      <div class="flex items-center justify-between border-b border-gray-100 px-4 py-3">
        <div class="flex items-center gap-2">
          <h3 class="text-sm font-semibold text-gray-900">{title}</h3>
          {truncated && <span class="rounded bg-amber-50 px-2 py-0.5 text-[11px] text-amber-700">{t("observability.truncated")}</span>}
        </div>
        <button type="button" onClick={() => void copy()} disabled={!value} class="text-xs font-medium text-indigo-600 hover:text-indigo-800 disabled:opacity-40">{t("observability.copy")}</button>
      </div>
      <pre class="max-h-[420px] overflow-auto whitespace-pre-wrap break-words bg-gray-950 p-4 font-mono text-xs leading-5 text-gray-200">{prettyBody(value) || t("observability.noPayload")}</pre>
    </section>
  );
}

const traceSectionTone: Record<TraceSectionKind, string> = {
  system: "border-violet-200 bg-violet-50 text-violet-700",
  user: "border-blue-200 bg-blue-50 text-blue-700",
  assistant: "border-emerald-200 bg-emerald-50 text-emerald-700",
  thinking: "border-amber-200 bg-amber-50 text-amber-700",
  toolUse: "border-fuchsia-200 bg-fuchsia-50 text-fuchsia-700",
  toolResult: "border-cyan-200 bg-cyan-50 text-cyan-700",
  raw: "border-gray-200 bg-gray-50 text-gray-600",
};

function TracePayloadView({ title, value, output, truncated }: { title: string; value?: string; output: boolean; truncated: boolean }) {
  const sections = parseTracePayload(value, output);
  return (
    <section class="overflow-hidden rounded-xl border border-gray-200 bg-gray-50/60">
      <div class="flex items-center justify-between border-b border-gray-200 bg-white px-4 py-3">
        <h3 class="text-sm font-semibold text-gray-900">{title}</h3>
        {truncated && <span class="rounded bg-amber-50 px-2 py-0.5 text-[11px] text-amber-700">{t("observability.truncated")}</span>}
      </div>
      <div class="space-y-3 p-3">
        {sections.length === 0 ? (
          <p class="p-4 text-center text-sm text-gray-400">{t("observability.noPayload")}</p>
        ) : sections.map((section, index) => (
          <article key={`${section.kind}-${index}`} class="overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm">
            <div class="flex items-center gap-2 border-b border-gray-100 px-3 py-2">
              <span class={`rounded-md border px-2 py-0.5 text-[11px] font-semibold ${traceSectionTone[section.kind]}`}>
                {t(`observability.span.${section.kind}`)}
              </span>
              {section.name && <span class="truncate font-mono text-xs text-gray-500">{section.name}</span>}
            </div>
            <pre class="max-h-72 overflow-auto whitespace-pre-wrap break-words px-3 py-3 font-sans text-sm leading-6 text-gray-700">{section.content}</pre>
          </article>
        ))}
        {value && sections.some((section) => section.kind !== "raw") && (
          <details class="rounded-lg border border-gray-200 bg-white">
            <summary class="cursor-pointer px-3 py-2 text-xs font-medium text-gray-500 hover:text-gray-700">{t("observability.rawPayload")}</summary>
            <pre class="max-h-72 overflow-auto whitespace-pre-wrap break-words border-t border-gray-100 bg-gray-950 p-3 font-mono text-xs leading-5 text-gray-200">{prettyBody(value)}</pre>
          </details>
        )}
      </div>
    </section>
  );
}

type ObservabilityTraceDetailPageProps = RoutePropsForPath<"/observability/traces/:traceID">;

export function ObservabilityTraceDetailPage({ traceID }: ObservabilityTraceDetailPageProps) {
  void locale.value;
  const { route } = useLocation();
  const decodedTraceID = decodeURIComponent(traceID);
  const [trace, setTrace] = useState<ObservabilityTraceDetailResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    getObservabilityTrace(decodedTraceID)
      .then((data) => {
        if (active) setTrace(data);
      })
      .catch((err) => {
        if (active) setError(err?.message || t("observability.detailLoadFailed"));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => { active = false; };
  }, [decodedTraceID]);

  return (
    <div class="page-shell space-y-6">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <button type="button" onClick={() => route("/observability?tab=traces")} class="inline-flex items-center gap-2 text-sm font-medium text-indigo-600 hover:text-indigo-800">
            <span aria-hidden="true">←</span>
            {t("observability.backToTraces")}
          </button>
          <h1 class="mt-3 text-2xl font-bold text-gray-950">{t("observability.traceDetail")}</h1>
          <p class="mt-2 break-all font-mono text-sm text-gray-500">{decodedTraceID}</p>
        </div>
        {trace && <StatusBadge status={trace.trace.status} />}
      </header>

      {error && <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">{error}</div>}
      {loading ? (
        <div class="rounded-2xl border border-gray-200 bg-white p-16 text-center text-sm text-gray-400">{t("observability.loading")}</div>
      ) : trace ? (
        <div class="rounded-2xl border border-gray-200 bg-gray-50 shadow-sm">
          <TraceDetail trace={trace} />
        </div>
      ) : null}
    </div>
  );
}

function TraceDetail({ trace }: { trace: ObservabilityTraceDetailResponse }) {
  const start = new Date(trace.trace.started_at).getTime();
  const duration = Math.max(1, trace.trace.duration_ms);
  const [selectedRequestID, setSelectedRequestID] = useState(trace.requests[0]?.id || "");
  const [detailTab, setDetailTab] = useState<"overview" | "input" | "output">("overview");
  const [traceView, setTraceView] = useState<"waterfall" | "flow">("waterfall");
  useEffect(() => {
    setSelectedRequestID(trace.requests[0]?.id || "");
    setDetailTab("overview");
    setTraceView("waterfall");
  }, [trace.trace.trace_id]);
  const selectedRequest = trace.requests.find((request) => request.id === selectedRequestID) || trace.requests[0];
  return (
    <div class="space-y-5 p-5 lg:p-6">
      <div class="grid grid-cols-2 gap-3 xl:grid-cols-4">
        <MetricCard label={t("observability.columnRequests")} value={formatNumber(trace.trace.request_count)} tone="bg-indigo-500" />
        <MetricCard label={t("observability.columnLatency")} value={formatObservabilityDuration(trace.trace.duration_ms)} tone="bg-amber-500" />
        <MetricCard label={t("observability.columnTokens")} value={formatNumber(trace.trace.total_tokens)} tone="bg-violet-500" />
        <MetricCard label={t("observability.columnStatus")} value={trace.trace.status === "completed" ? t("observability.statusCompleted") : t("observability.statusFailed")} tone="bg-emerald-500" />
      </div>
      {trace.trace.thread_id && (
        <div class="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-xl border border-gray-200 bg-white px-4 py-3">
          <span class="text-xs font-medium uppercase tracking-wide text-gray-400">{t("observability.columnThread")}</span>
          <span class="break-all font-mono text-sm text-gray-700">{trace.trace.thread_id}</span>
        </div>
      )}

      <section class="overflow-hidden rounded-xl border border-gray-200 bg-white">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-5 py-4">
          <div>
            <h3 class="text-sm font-semibold text-gray-900">
              {traceView === "waterfall" ? t("observability.timeline") : t("observability.flowTitle")}
            </h3>
            <p class="mt-1 text-xs text-gray-400">
              {traceView === "waterfall" ? t("observability.timelineHint") : t("observability.flowHint")}
            </p>
          </div>
          <div class="flex rounded-lg bg-gray-100 p-1">
            {(["waterfall", "flow"] as const).map((view) => (
              <button
                type="button"
                key={view}
                onClick={() => setTraceView(view)}
                class={`rounded-md px-3 py-1.5 text-xs font-medium transition ${
                  traceView === view ? "bg-white text-indigo-700 shadow-sm" : "text-gray-500 hover:text-gray-800"
                }`}
              >
                {t(`observability.traceView.${view}`)}
              </button>
            ))}
          </div>
        </div>
        {traceView === "waterfall" ? (
          <div class="overflow-x-auto">
            <div class="min-w-[760px] px-5 py-4">
              <div class="grid grid-cols-[230px_minmax(360px,1fr)_80px] items-end gap-4 border-b border-gray-100 pb-2 text-[10px] tabular-nums text-gray-400">
                <span>{t("observability.columnRequests")}</span>
                <div class="flex justify-between">
                  {[0, 25, 50, 75, 100].map((percent) => (
                    <span key={percent}>{formatObservabilityDuration(Math.round(duration * percent / 100))}</span>
                  ))}
                </div>
                <span class="text-right">{t("observability.columnLatency")}</span>
              </div>
              <div class="mt-2 space-y-1">
                {trace.requests.map((request, index) => {
                  const offset = Math.max(0, new Date(request.started_at).getTime() - start);
                  const left = Math.min(98, (offset / duration) * 100);
                  const width = Math.max(1.5, Math.min(100 - left, (request.duration_ms / duration) * 100));
                  return (
                    <button
                      type="button"
                      key={request.id}
                      onClick={() => setSelectedRequestID(request.id)}
                      class={`grid w-full grid-cols-[230px_minmax(360px,1fr)_80px] items-center gap-4 rounded-lg border px-3 py-2.5 text-left transition ${
                        selectedRequest?.id === request.id
                          ? "border-indigo-200 bg-indigo-50/70 ring-1 ring-indigo-100"
                          : "border-transparent hover:border-gray-200 hover:bg-gray-50"
                      }`}
                    >
                      <div class="flex min-w-0 items-center gap-3">
                        <span class={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold ${
                          request.status === "completed" ? "bg-indigo-100 text-indigo-700" : "bg-red-100 text-red-700"
                        }`}>{index + 1}</span>
                        <div class="min-w-0">
                          <p class="truncate text-xs font-semibold text-gray-800">{request.model || request.protocol}</p>
                          <p class="mt-0.5 truncate font-mono text-[10px] text-gray-400">{request.id}</p>
                        </div>
                      </div>
                      <div class="relative h-7">
                        <div class="absolute inset-0 grid grid-cols-4 divide-x divide-gray-100 border-x border-gray-100" aria-hidden="true">
                          <span /><span /><span /><span />
                        </div>
                        <div
                          class={`absolute top-1/2 h-3 -translate-y-1/2 rounded-sm shadow-sm ${
                            request.status === "completed" ? "bg-indigo-500" : "bg-red-500"
                          } ${selectedRequest?.id === request.id ? "ring-2 ring-white" : ""}`}
                          style={{ left: `${left}%`, width: `${width}%` }}
                        />
                      </div>
                      <span class="text-right text-xs font-medium tabular-nums text-gray-600">{formatObservabilityDuration(request.duration_ms)}</span>
                    </button>
                  );
                })}
              </div>
            </div>
          </div>
        ) : (
          <TraceFlowDiagram
            trace={trace}
            selectedRequestID={selectedRequest?.id}
            onSelectRequest={setSelectedRequestID}
          />
        )}
      </section>

      {selectedRequest && (
        <section class="overflow-hidden rounded-xl border border-gray-200 bg-white">
          <div class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-200 px-5 py-4">
            <div class="min-w-0">
              <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{t("observability.requestDetail")}</p>
              <p class="mt-1 break-all font-mono text-sm font-medium text-gray-800">{selectedRequest.id}</p>
            </div>
            <StatusBadge status={selectedRequest.status} />
          </div>
          <div class="border-b border-gray-200 px-5">
            <div class="flex gap-6">
              {(["overview", "input", "output"] as const).map((tab) => (
                <button
                  type="button"
                  key={tab}
                  onClick={() => setDetailTab(tab)}
                  class={`border-b-2 py-3 text-sm font-medium transition ${
                    detailTab === tab
                      ? "border-indigo-600 text-indigo-700"
                      : "border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-800"
                  }`}
                >
                  {t(`observability.detailTab.${tab}`)}
                </button>
              ))}
            </div>
          </div>
          <div class="p-5">
            {detailTab === "overview" && (
              <div class="space-y-5">
                <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                  <TraceDetailField label={t("observability.columnModel")} value={selectedRequest.model || "—"} />
                  <TraceDetailField label={t("observability.columnRoute")} value={sourceLabel(selectedRequest)} />
                  <TraceDetailField label={t("observability.columnEndpoint")} value={`${selectedRequest.method} ${selectedRequest.path}`} mono />
                  <TraceDetailField label={t("observability.caller")} value={selectedRequest.api_key_name || "—"} />
                  <TraceDetailField label={t("observability.columnTime")} value={formatObservabilityDateTime(selectedRequest.started_at)} />
                  {selectedRequest.request_id && <CopyableID label={t("observability.correlationRequestID")} value={selectedRequest.request_id} />}
                  {selectedRequest.b3_trace_id && <CopyableID label={t("observability.b3TraceID")} value={selectedRequest.b3_trace_id} />}
                  <TraceDetailField label={t("observability.columnLatency")} value={formatObservabilityDuration(selectedRequest.duration_ms)} />
                  <TraceDetailField label="TTFT" value={formatObservabilityDuration(selectedRequest.first_token_latency_ms)} />
                  <TraceDetailField
                    label={t("observability.columnTokens")}
                    value={`${t("observability.inputTokens")}: ${formatNumber(selectedRequest.input_tokens)} · ${t("observability.outputTokens")}: ${formatNumber(selectedRequest.output_tokens)}`}
                  />
                  <TraceDetailField
                    label={t("observability.columnCacheHitRate")}
                    value={formatCacheHitRate(selectedRequest)}
                  />
                  <TraceDetailField
                    label={t("observability.columnCacheRead")}
                    value={formatCacheTokens(selectedRequest, selectedRequest.cache_read_input_tokens)}
                  />
                </div>
                {selectedRequest.error_message && (
                  <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">{selectedRequest.error_message}</div>
                )}
              </div>
            )}
            {detailTab === "input" && (
              <TracePayloadView title={t("observability.traceInput")} value={selectedRequest.request_body} output={false} truncated={selectedRequest.request_body_truncated} />
            )}
            {detailTab === "output" && (
              <TracePayloadView title={t("observability.traceOutput")} value={selectedRequest.response_body} output truncated={selectedRequest.response_body_truncated} />
            )}
          </div>
        </section>
      )}
    </div>
  );
}

function TraceFlowDiagram({ trace, selectedRequestID, onSelectRequest }: {
  trace: ObservabilityTraceDetailResponse;
  selectedRequestID?: string;
  onSelectRequest: (id: string) => void;
}) {
  const sections = useMemo(() => buildTraceFlowSections(trace.requests), [trace.requests]);
  const [selectedSectionIndex, setSelectedSectionIndex] = useState<number | null>(null);
  useEffect(() => {
    setSelectedSectionIndex(null);
  }, [trace.trace.trace_id]);
  const toolCallIndexes = useMemo(() => {
    const indexes = new Map<string, number>();
    sections.forEach((section, index) => {
      if (section.kind === "toolUse" && section.callID) indexes.set(section.callID, index);
    });
    return indexes;
  }, [sections]);
  const toolResultIndexes = useMemo(() => {
    const indexes = new Map<string, number>();
    sections.forEach((section, index) => {
      if (section.kind === "toolResult" && section.callID) indexes.set(section.callID, index);
    });
    return indexes;
  }, [sections]);
  const toolCalls = sections.filter((section) => section.kind === "toolUse").length;
  const toolResults = sections.filter((section) => section.kind === "toolResult").length;
  const jumpTo = (index: number) => {
    setSelectedSectionIndex(index);
    const section = sections[index];
    if (section) onSelectRequest(section.requestID);
    document.getElementById(`trace-flow-section-${index}`)?.scrollIntoView({ behavior: "smooth", block: "center" });
  };
  const selectedSection = selectedSectionIndex == null ? null : sections[selectedSectionIndex];

  return (
    <div class="bg-gray-50/60 p-4 sm:p-6">
      <div class="mb-5 flex flex-wrap items-center gap-2">
        <span class="rounded-full border border-indigo-200 bg-indigo-50 px-3 py-1 text-xs font-medium text-indigo-700">
          {t("observability.flowItems", sections.length)}
        </span>
        <span class="rounded-full border border-fuchsia-200 bg-fuchsia-50 px-3 py-1 text-xs font-medium text-fuchsia-700">
          {t("observability.flowToolCalls", toolCalls)}
        </span>
        <span class="rounded-full border border-cyan-200 bg-cyan-50 px-3 py-1 text-xs font-medium text-cyan-700">
          {t("observability.flowToolResults", toolResults)}
        </span>
      </div>

      <div class="mx-auto flex max-w-3xl flex-col items-center">
        <div class="flex w-full max-w-xl items-center gap-3 rounded-xl border border-indigo-200 bg-white px-4 py-3 shadow-sm">
          <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-indigo-600 text-white shadow-sm">
            <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="5" cy="12" r="2" /><circle cx="19" cy="5" r="2" /><circle cx="19" cy="19" r="2" />
              <path d="M7 12h4a4 4 0 0 0 4-4V7M11 12h4a4 4 0 0 1 4 4v1" />
            </svg>
          </span>
          <div class="min-w-0">
            <p class="text-xs font-semibold text-gray-900">{t("observability.flowRoot")}</p>
            <p class="mt-1 break-all font-mono text-[10px] text-gray-400">{trace.trace.trace_id}</p>
          </div>
        </div>

        {trace.requests.map((request, requestIndex) => {
          const requestSections = sections
            .map((section, index) => ({ section, index }))
            .filter(({ section }) => section.requestID === request.id);
          const selected = selectedRequestID === request.id;
          return (
            <div key={request.id} class="flex w-full flex-col items-center">
              <TraceFlowConnector tone="indigo" />
              <button
                type="button"
                onClick={() => onSelectRequest(request.id)}
                class={`flex w-full max-w-2xl flex-wrap items-center justify-between gap-3 rounded-xl border bg-white px-4 py-3 text-left shadow-sm transition ${
                  selected ? "border-indigo-400 ring-2 ring-indigo-100" : "border-gray-200 hover:border-indigo-200"
                }`}
                aria-label={t("observability.flowSelectRequest", requestIndex + 1)}
              >
                <div class="flex min-w-0 items-center gap-3">
                  <span class={`flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-xs font-bold ${
                    request.status === "completed" ? "bg-indigo-100 text-indigo-700" : "bg-red-100 text-red-700"
                  }`}>{requestIndex + 1}</span>
                  <div class="min-w-0">
                    <div class="flex flex-wrap items-center gap-2">
                      <span class="text-xs font-semibold text-gray-900">{t("observability.flowRequest", requestIndex + 1)}</span>
                      <span class="rounded bg-gray-100 px-2 py-0.5 font-mono text-[10px] text-gray-600">{request.model || request.protocol}</span>
                      <StatusBadge status={request.status} />
                    </div>
                    <p class="mt-1 truncate font-mono text-[10px] text-gray-400">{request.id}</p>
                  </div>
                </div>
                <div class="flex shrink-0 items-center gap-3 text-[11px] text-gray-500">
                  <span>{formatObservabilityDuration(request.duration_ms)}</span>
                  <span>{formatNumber(request.total_tokens)} {t("observability.columnTokens")}</span>
                </div>
              </button>
              {requestSections.length === 0 ? (
                <>
                  <TraceFlowConnector />
                  <p class="w-full max-w-xl rounded-xl border border-dashed border-gray-300 bg-white px-4 py-4 text-center text-sm text-gray-400">{t("observability.flowNoSpans")}</p>
                </>
              ) : (
                requestSections.map(({ section, index }) => (
                  <div key={`${section.kind}-${index}`} class="flex w-full flex-col items-center">
                    <TraceFlowConnector tone={section.isError ? "red" : traceFlowConnectorTone(section.kind)} />
                    <TraceFlowStepNode
                        key={`${section.kind}-${index}`}
                        section={section}
                        index={index}
                        selected={selectedSectionIndex === index}
                        onSelect={() => {
                          setSelectedSectionIndex(index);
                          onSelectRequest(section.requestID);
                        }}
                      />
                  </div>
                ))
              )}
            </div>
          );
        })}
      </div>
      {selectedSection && selectedSectionIndex != null && (
        <TraceFlowStepDrawer
          section={selectedSection}
          index={selectedSectionIndex}
          request={trace.requests[selectedSection.requestIndex]}
          linkedIndex={selectedSection.callID
            ? selectedSection.kind === "toolUse"
              ? toolResultIndexes.get(selectedSection.callID)
              : toolCallIndexes.get(selectedSection.callID)
            : undefined}
          onJump={jumpTo}
          onClose={() => setSelectedSectionIndex(null)}
        />
      )}
    </div>
  );
}

function TraceFlowConnector({ tone = "gray" }: { tone?: "gray" | "indigo" | "fuchsia" | "cyan" | "amber" | "red" }) {
  const colors = {
    gray: "text-gray-300",
    indigo: "text-indigo-300",
    fuchsia: "text-fuchsia-300",
    cyan: "text-cyan-300",
    amber: "text-amber-300",
    red: "text-red-300",
  };
  return (
    <div class={`flex h-10 flex-col items-center ${colors[tone]}`} aria-hidden="true">
      <span class="w-0.5 flex-1 bg-current" />
      <svg class="h-3 w-4 shrink-0" viewBox="0 0 16 10" fill="currentColor">
        <path d="M1 1h14L8 9 1 1Z" />
      </svg>
    </div>
  );
}

function TraceFlowStepNode({ section, index, selected, onSelect }: {
  section: TraceFlowSection;
  index: number;
  selected: boolean;
  onSelect: () => void;
}) {
  const preview = traceFlowPreview(section.content);
  return (
    <button
      id={`trace-flow-section-${index}`}
      type="button"
      onClick={onSelect}
      class={`flex w-full max-w-xl scroll-mt-24 items-center gap-3 rounded-xl border bg-white px-4 py-3 text-left shadow-sm transition ${
        selected ? "border-indigo-400 bg-indigo-50 ring-2 ring-indigo-100" : "border-gray-200 hover:-translate-y-0.5 hover:border-indigo-200 hover:shadow-md"
      }`}
    >
      <span class={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border text-xs font-bold ${traceSectionTone[section.kind]}`}>
        {traceFlowStepIcon(section.kind)}
      </span>
      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-2">
          <span class={`rounded-md border px-2 py-0.5 text-[11px] font-semibold ${traceSectionTone[section.kind]}`}>
            {t(`observability.span.${section.kind}`)}
          </span>
          {section.name && <span class="font-mono text-xs font-semibold text-gray-700">{section.name}</span>}
          {section.isError && <span class="rounded bg-red-100 px-2 py-0.5 text-[10px] font-semibold text-red-700">{t("observability.flowToolError")}</span>}
        </div>
        <p class="mt-1 truncate text-xs text-gray-500">{preview || t("observability.flowNoContent")}</p>
      </div>
      <span class="shrink-0 text-[10px] uppercase tracking-wide text-gray-400">
        {section.phase === "input" ? t("observability.flowPhaseInput") : t("observability.flowPhaseOutput")}
      </span>
      <span class="shrink-0 text-gray-300" aria-hidden="true">›</span>
    </button>
  );
}

function TraceFlowStepDrawer({ section, index, request, linkedIndex, onJump, onClose }: {
  section: TraceFlowSection;
  index: number;
  request?: ObservabilityRequest;
  linkedIndex?: number;
  onJump: (index: number) => void;
  onClose: () => void;
}) {
  const content = prettyTraceFlowContent(section.content);
  const copy = async () => {
    if (content) await navigator.clipboard.writeText(content);
  };
  return (
    <div class="fixed inset-0 z-50 bg-gray-950/30" onClick={onClose}>
      <aside class="absolute inset-y-0 right-0 flex w-full max-w-xl flex-col bg-white shadow-2xl" onClick={(event) => event.stopPropagation()}>
        <header class="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <span class={`rounded-md border px-2 py-0.5 text-[11px] font-semibold ${traceSectionTone[section.kind]}`}>
                {t(`observability.span.${section.kind}`)}
              </span>
              {section.name && <span class="font-mono text-sm font-semibold text-gray-800">{section.name}</span>}
              {section.isError && <span class="rounded bg-red-100 px-2 py-0.5 text-[10px] font-semibold text-red-700">{t("observability.flowToolError")}</span>}
            </div>
            <h3 class="mt-2 text-lg font-semibold text-gray-950">{t("observability.flowStepDetail")}</h3>
          </div>
          <button type="button" onClick={onClose} class="rounded-lg p-2 text-gray-400 hover:bg-gray-100 hover:text-gray-700" aria-label={t("observability.close")}>×</button>
        </header>

        <div class="flex-1 space-y-5 overflow-y-auto p-5">
          <div class="grid gap-3 sm:grid-cols-2">
            <TraceDetailField label={t("observability.flowStep")} value={String(index + 1)} />
            <TraceDetailField label={t("observability.flowPhase")} value={section.phase === "input" ? t("observability.flowPhaseInput") : t("observability.flowPhaseOutput")} />
            <TraceDetailField label={t("observability.flowRequestID")} value={request?.id || section.requestID} mono />
            <TraceDetailField label={t("observability.columnModel")} value={request?.model || request?.protocol || "—"} />
          </div>

          {section.callID && (
            <div class="rounded-xl border border-gray-200 bg-gray-50 p-4">
              <p class="text-[11px] font-medium uppercase tracking-wide text-gray-400">{t("observability.flowCallID")}</p>
              <p class="mt-1 break-all font-mono text-xs text-gray-700">{section.callID}</p>
            </div>
          )}

          {linkedIndex !== undefined && linkedIndex !== index && (
            <button type="button" onClick={() => onJump(linkedIndex)} class="w-full rounded-xl border border-indigo-200 bg-indigo-50 px-4 py-3 text-left text-sm font-medium text-indigo-700 hover:bg-indigo-100">
              {section.kind === "toolUse" ? t("observability.flowViewResult") : t("observability.flowViewCall")}
            </button>
          )}

          <section class="overflow-hidden rounded-xl border border-gray-200">
            <div class="flex items-center justify-between border-b border-gray-200 bg-gray-50 px-4 py-3">
              <h4 class="text-sm font-semibold text-gray-900">
                {section.kind === "toolUse"
                  ? t("observability.flowToolArguments")
                  : section.kind === "toolResult"
                    ? t("observability.flowToolResultContent")
                    : t("observability.flowContent")}
              </h4>
              <button type="button" disabled={!content} onClick={() => void copy()} class="text-xs font-medium text-indigo-600 hover:text-indigo-800 disabled:opacity-40">{t("observability.copy")}</button>
            </div>
            <pre class={`min-h-32 max-h-[60vh] overflow-auto whitespace-pre-wrap break-words p-4 text-sm leading-6 text-gray-700 ${
              section.kind === "toolUse" || section.kind === "toolResult" ? "font-mono text-xs" : "font-sans"
            }`}>{content || t("observability.flowNoContent")}</pre>
          </section>
        </div>
      </aside>
    </div>
  );
}

function traceFlowStepIcon(kind: TraceSectionKind): string {
  switch (kind) {
    case "system": return "S";
    case "user": return "U";
    case "assistant": return "A";
    case "thinking": return "T";
    case "toolUse": return "↗";
    case "toolResult": return "↙";
    default: return "•";
  }
}

function traceFlowConnectorTone(kind: TraceSectionKind): "gray" | "fuchsia" | "cyan" | "amber" {
  switch (kind) {
    case "toolUse": return "fuchsia";
    case "toolResult": return "cyan";
    case "thinking": return "amber";
    default: return "gray";
  }
}

function traceFlowPreview(value: string): string {
  return value.replace(/\s+/g, " ").trim();
}

function prettyTraceFlowContent(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return "";
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2);
  } catch {
    return value;
  }
}

function TraceDetailField({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div class="min-w-0 rounded-xl border border-gray-200 bg-gray-50/60 p-3">
      <p class="text-[11px] font-medium uppercase tracking-wide text-gray-400">{label}</p>
      <p class={`mt-1.5 truncate text-sm font-medium text-gray-800 ${mono ? "font-mono text-xs" : ""}`} title={value}>{value}</p>
    </div>
  );
}

function CopyableID({ label, value }: { label: string; value: string }) {
  return (
    <div class="min-w-0 rounded-xl border border-gray-200 bg-gray-50/60 p-3">
      <div class="flex items-center justify-between gap-2">
        <p class="text-[11px] font-medium uppercase tracking-wide text-gray-400">{label}</p>
        <button type="button" onClick={() => void navigator.clipboard.writeText(value)} class="text-xs font-medium text-indigo-600 hover:text-indigo-800">
          {t("observability.copy")}
        </button>
      </div>
      <p class="mt-1.5 truncate font-mono text-xs font-medium text-gray-800" title={value}>{value}</p>
    </div>
  );
}

function formatCacheHitRate(request: ObservabilityRequest): string {
  if (request.cache_eligible_input_tokens <= 0) return "—";
  return `${request.cache_hit_rate.toFixed(1)}%`;
}

function formatCacheTokens(request: ObservabilityRequest, tokens: number): string {
  if (request.cache_eligible_input_tokens <= 0) return "—";
  return formatNumber(tokens);
}
