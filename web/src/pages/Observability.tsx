import { useEffect, useMemo, useState } from "preact/hooks";
import { useLocation, type RoutePropsForPath } from "preact-iso";
import {
  getObservabilityRequest,
  getObservabilityRequests,
  getObservabilityTrace,
  getObservabilityTraces,
} from "../api/client";
import type {
  ObservabilityQuery,
  ObservabilityRequest,
  ObservabilityRequestListResponse,
  ObservabilityTraceDetailResponse,
  ObservabilityTraceListResponse,
} from "../api/client";
import { locale, t } from "../i18n";
import {
  formatObservabilityDuration,
  observabilityFromForPeriod,
  type ObservabilityPeriod,
} from "../utils/observability";
import { parseTracePayload, type TraceSectionKind } from "../utils/tracePayload";

type ObservabilityTab = "requests" | "traces";

const pageSize = 20;

function initialParam(name: string, fallback = ""): string {
  if (typeof window === "undefined") return fallback;
  return new URLSearchParams(window.location.search).get(name) || fallback;
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat(locale.value === "zh" ? "zh-CN" : "en-US").format(value || 0);
}

function formatDateTime(value: string): string {
  if (!value) return "—";
  return new Date(value).toLocaleString(locale.value === "zh" ? "zh-CN" : "en-US");
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
  const [period, setPeriod] = useState<ObservabilityPeriod>((initialParam("period", "24h") as ObservabilityPeriod));
  const [status, setStatus] = useState(initialParam("status"));
  const [model, setModel] = useState(initialParam("model"));
  const [source, setSource] = useState(initialParam("source"));
  const [search, setSearch] = useState(initialParam("q"));
  const [page, setPage] = useState(Math.max(1, Number(initialParam("page", "1")) || 1));
  const [requests, setRequests] = useState<ObservabilityRequestListResponse | null>(null);
  const [traces, setTraces] = useState<ObservabilityTraceListResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [refreshKey, setRefreshKey] = useState(0);
  const [requestDetail, setRequestDetail] = useState<ObservabilityRequest | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const query = useMemo<ObservabilityQuery>(() => ({
    from: observabilityFromForPeriod(period),
    status: status || undefined,
    model: model.trim() || undefined,
    source: source.trim() || undefined,
    q: search.trim() || undefined,
    limit: pageSize,
    offset: (page - 1) * pageSize,
  }), [period, status, model, source, search, page]);

  useEffect(() => {
    const params = new URLSearchParams();
    params.set("tab", tab);
    params.set("period", period);
    if (status) params.set("status", status);
    if (model.trim()) params.set("model", model.trim());
    if (source.trim()) params.set("source", source.trim());
    if (search.trim()) params.set("q", search.trim());
    if (page > 1) params.set("page", String(page));
    window.history.replaceState(null, "", `/observability?${params}`);
  }, [tab, period, status, model, source, search, page]);

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
          <select value={period} onInput={(event) => changeFilter(() => setPeriod((event.currentTarget as HTMLSelectElement).value as ObservabilityPeriod))} class="rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-700 focus:ring-2 focus:ring-indigo-500">
            <option value="24h">{t("observability.period24h")}</option>
            <option value="7d">{t("observability.period7d")}</option>
            <option value="30d">{t("observability.period30d")}</option>
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

        {error && <div class="m-4 rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">{error}</div>}
        {tab === "requests" ? (
          <RequestTable data={requests} loading={loading} onOpen={openRequest} onOpenTrace={navigateToTrace} />
        ) : (
          <TraceTable data={traces} loading={loading} onOpen={navigateToTrace} onOpenFull={navigateToTrace} />
        )}
        {(() => {
          const total = tab === "requests" ? requests?.total || 0 : traces?.total || 0;
          const pages = Math.max(1, Math.ceil(total / pageSize));
          return (
            <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 bg-gray-50/50 px-4 py-3">
              <span class="text-xs text-gray-500">{t("observability.total", total)}</span>
              <div class="flex items-center gap-2">
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
            <th class="px-4 py-3">{t("observability.columnTokens")}</th>
            <th class="px-4 py-3">{t("observability.columnCacheRead")}</th>
            <th class="px-4 py-3">{t("observability.columnCacheWrite")}</th>
            <th class="px-4 py-3">{t("observability.columnCacheHitRate")}</th>
            <th class="px-4 py-3">{t("observability.columnTrace")}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100">
          {rows.map((row) => (
            <tr key={row.id} onClick={() => onOpen(row.id)} class="cursor-pointer transition hover:bg-indigo-50/40">
              <td class="max-w-[190px] truncate px-4 py-3 font-medium text-gray-900" title={row.model}>{row.model || "—"}</td>
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-500">{formatDateTime(row.started_at)}</td>
              <td class="px-4 py-3"><StatusBadge status={row.status} /></td>
              <td class="max-w-[190px] truncate px-4 py-3 text-gray-600" title={sourceLabel(row)}>{sourceLabel(row)}</td>
              <td class="whitespace-nowrap px-4 py-3 tabular-nums text-gray-600">
                {formatObservabilityDuration(row.duration_ms)}
                {row.stream && <span class="block text-[11px] text-gray-400">TTFT {formatObservabilityDuration(row.first_token_latency_ms)}</span>}
              </td>
              <td class="whitespace-nowrap px-4 py-3 tabular-nums text-gray-600">{formatNumber(row.total_tokens)}</td>
              <td class="whitespace-nowrap px-4 py-3 tabular-nums text-gray-600">{formatCacheTokens(row, row.cache_read_input_tokens)}</td>
              <td class="whitespace-nowrap px-4 py-3 tabular-nums text-gray-600">{formatCacheTokens(row, row.cache_creation_input_tokens)}</td>
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
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-500">{formatDateTime(row.started_at)}</td>
              <td class="max-w-[150px] truncate px-4 py-3 font-mono text-xs text-gray-500" title={row.thread_id}>{row.thread_id || "—"}</td>
              <td class="px-4 py-3 tabular-nums text-gray-700">{row.request_count}</td>
              <td class="max-w-[220px] truncate px-4 py-3 text-gray-700" title={row.models.join(", ")}>{row.models.join(", ") || "—"}</td>
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
    [t("observability.columnCacheWrite"), formatCacheTokens(request, request.cache_creation_input_tokens)],
    [t("observability.columnCacheHitRate"), formatCacheHitRate(request)],
    [t("observability.caller"), request.api_key_name || "—"],
    [t("observability.columnTime"), formatDateTime(request.started_at)],
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
            <p class="mt-1 text-xs text-gray-400">{t("observability.timelineHint")}</p>
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
                  <TraceDetailField label={t("observability.columnTime")} value={formatDateTime(selectedRequest.started_at)} />
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
                  <TraceDetailField
                    label={t("observability.columnCacheWrite")}
                    value={formatCacheTokens(selectedRequest, selectedRequest.cache_creation_input_tokens)}
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
  return (
    <div
      class="overflow-x-auto bg-gray-50/60 p-6"
      style={{ backgroundImage: "radial-gradient(circle, rgb(209 213 219 / 0.7) 1px, transparent 1px)", backgroundSize: "18px 18px" }}
    >
      <div class="flex min-h-[280px] min-w-max items-center">
        <div class="w-56 shrink-0 rounded-xl border border-indigo-200 bg-white shadow-sm">
          <div class="flex items-center gap-3 border-b border-gray-100 px-4 py-3">
            <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-100 text-indigo-700">
              <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <circle cx="5" cy="12" r="2" /><circle cx="19" cy="5" r="2" /><circle cx="19" cy="19" r="2" />
                <path d="M7 12h4a4 4 0 0 0 4-4V7M11 12h4a4 4 0 0 1 4 4v1" />
              </svg>
            </span>
            <div>
              <p class="text-xs font-semibold text-gray-900">{t("observability.flowRoot")}</p>
              <p class="mt-0.5 text-[10px] text-gray-400">{formatNumber(trace.trace.request_count)} {t("observability.columnRequests")}</p>
            </div>
          </div>
          <div class="space-y-2 px-4 py-3 text-[11px]">
            <p class="truncate font-mono text-gray-500" title={trace.trace.trace_id}>{trace.trace.trace_id}</p>
            <div class="flex items-center justify-between text-gray-500">
              <span>{formatNumber(trace.trace.total_tokens)} {t("observability.columnTokens")}</span>
              <span>{formatObservabilityDuration(trace.trace.duration_ms)}</span>
            </div>
          </div>
        </div>

        {trace.requests.map((request, index) => (
          <div key={request.id} class="flex items-center">
            <div class="flex w-16 items-center" aria-hidden="true">
              <div class="h-px flex-1 bg-indigo-300" />
              <svg class="h-3 w-3 -ml-0.5 text-indigo-400" viewBox="0 0 12 12" fill="currentColor">
                <path d="M2 1l8 5-8 5V1z" />
              </svg>
            </div>
            <button
              type="button"
              onClick={() => onSelectRequest(request.id)}
              class={`w-64 shrink-0 overflow-hidden rounded-xl border bg-white text-left shadow-sm transition hover:-translate-y-0.5 hover:shadow-md ${
                selectedRequestID === request.id
                  ? "border-indigo-400 ring-2 ring-indigo-100"
                  : request.status === "completed" ? "border-gray-200" : "border-red-200"
              }`}
            >
              <div class={`h-1 ${request.status === "completed" ? "bg-indigo-500" : "bg-red-500"}`} />
              <div class="flex items-start gap-3 border-b border-gray-100 px-4 py-3">
                <span class={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold ${
                  request.status === "completed" ? "bg-indigo-100 text-indigo-700" : "bg-red-100 text-red-700"
                }`}>{index + 1}</span>
                <div class="min-w-0 flex-1">
                  <div class="flex items-center justify-between gap-2">
                    <p class="truncate text-xs font-semibold text-gray-900">{request.model || request.protocol}</p>
                    <span class={`h-2 w-2 shrink-0 rounded-full ${request.status === "completed" ? "bg-emerald-500" : "bg-red-500"}`} />
                  </div>
                  <p class="mt-1 truncate font-mono text-[10px] text-gray-400">{request.id}</p>
                </div>
              </div>
              <div class="space-y-2 px-4 py-3 text-[11px] text-gray-500">
                <div class="flex items-center justify-between gap-3">
                  <span class="truncate" title={sourceLabel(request)}>{sourceLabel(request)}</span>
                  <span class="shrink-0 font-medium tabular-nums text-gray-700">{formatObservabilityDuration(request.duration_ms)}</span>
                </div>
                <div class="flex items-center justify-between gap-3 border-t border-gray-100 pt-2">
                  <span>↑ {formatNumber(request.input_tokens)} · ↓ {formatNumber(request.output_tokens)}</span>
                  {request.cache_eligible_input_tokens > 0 && (
                    <span class="shrink-0 text-emerald-600">{formatCacheHitRate(request)}</span>
                  )}
                </div>
              </div>
            </button>
          </div>
        ))}
      </div>
    </div>
  );
}

function TraceDetailField({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div class="min-w-0 rounded-xl border border-gray-200 bg-gray-50/60 p-3">
      <p class="text-[11px] font-medium uppercase tracking-wide text-gray-400">{label}</p>
      <p class={`mt-1.5 truncate text-sm font-medium text-gray-800 ${mono ? "font-mono text-xs" : ""}`} title={value}>{value}</p>
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
