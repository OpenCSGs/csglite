import { useEffect } from "preact/hooks";
import { signal } from "@preact/signals";
import {
  isFeatureNotLicensedError,
  getEdgeApplications,
  getEdgeEarnings,
  getEdgeBalance,
  getEdgeStatements,
  getEdgeStatus,
  setEdgeEnabled,
  submitEdgeApplication,
  withdrawEdgeApplication,
} from "../api/client";
import type { EdgeApplication, EdgeBalanceResponse, EdgeEarnings, EdgeStatement, EdgeStatus } from "../api/client";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { MonthPicker } from "../components/MonthPicker";
import { t, locale } from "../i18n";
import {
  applicationTone,
  canWithdraw,
  centsToYuan,
  currentMonth,
  formatMonth,
  statementTone,
  edgeStateTone,
  formatYuan,
  hasStats,
  isLiveApplication,
  isValidEdgeModelName,
  mergeEarningsByModel,
  onlineLabel,
  percent,
  servingState,
  servingTone,
  sinceLabel,
  suggestEdgeModelName,
  ttftLabel,
  yuanToCents,
} from "../edge";

type EdgeTab = "overview" | "models" | "earnings";

// ---- page state ------------------------------------------------------------
const activeTab = signal<EdgeTab>("overview");

const status = signal<EdgeStatus | null>(null);
const statusError = signal("");
/** Set while the instance has no Enterprise license (403 feature_not_licensed). */
const locked = signal(false);
const toggling = signal(false);

const applications = signal<EdgeApplication[]>([]);
const applicationsError = signal("");
const lastChanged = signal(-1);
const showHistory = signal(false);

const month = signal(currentMonth());
const earnings = signal<EdgeEarnings | null>(null);
const earningsError = signal("");
const balance = signal<EdgeBalanceResponse | null>(null);
const statements = signal<EdgeStatement[]>([]);
const settlementError = signal("");

const applyOpen = signal(false);
const formModel = signal("");
const formName = signal("");
const formPromptPrice = signal("");
const formCompletionPrice = signal("");
const formConcurrency = signal("4");
const formDescription = signal("");
const submitting = signal(false);
const submitError = signal("");

const withdrawTarget = signal<EdgeApplication | null>(null);

async function loadStatus() {
  try {
    const next = await getEdgeStatus();
    status.value = next;
    statusError.value = "";
    locked.value = false;
    if (next.signed_in && next.applications_changed !== lastChanged.value) {
      lastChanged.value = next.applications_changed;
      void loadApplications();
    }
  } catch (e) {
    if (isFeatureNotLicensedError(e)) {
      locked.value = true;
      status.value = null;
      return;
    }
    statusError.value = (e as Error).message;
  }
}

async function loadApplications() {
  try {
    applications.value = await getEdgeApplications();
    applicationsError.value = "";
  } catch (e) {
    applicationsError.value = (e as Error).message;
  }
}

async function loadEarnings() {
  try {
    earnings.value = await getEdgeEarnings(month.value);
    earningsError.value = "";
  } catch (e) {
    earnings.value = null;
    earningsError.value = (e as Error).message;
  }
}

// The balance and monthly statements are CSGHub's settlement records; they
// do not depend on the month picked below them.
async function loadSettlement() {
  try {
    const [b, list] = await Promise.all([getEdgeBalance(), getEdgeStatements()]);
    balance.value = b;
    statements.value = list;
    settlementError.value = "";
  } catch (e) {
    settlementError.value = (e as Error).message;
  }
}

async function toggle() {
  const current = status.value;
  if (!current) return;
  toggling.value = true;
  try {
    status.value = await setEdgeEnabled(!current.enabled);
  } catch (e) {
    statusError.value = (e as Error).message;
  } finally {
    toggling.value = false;
  }
}

function openApply() {
  submitError.value = "";
  applyOpen.value = true;
}

async function submit(e: Event) {
  e.preventDefault();
  submitError.value = "";
  const promptPrice = yuanToCents(formPromptPrice.value);
  const completionPrice = yuanToCents(formCompletionPrice.value);
  if (!formModel.value) {
    submitError.value = t("edge.form.errModel");
    return;
  }
  if (!isValidEdgeModelName(formName.value)) {
    submitError.value = t("edge.form.errName");
    return;
  }
  if (promptPrice === null || completionPrice === null) {
    submitError.value = t("edge.form.errPrice");
    return;
  }
  submitting.value = true;
  try {
    await submitEdgeApplication({
      model_name: formName.value.trim(),
      local_model_id: formModel.value,
      description: formDescription.value.trim(),
      prompt_price: promptPrice,
      completion_price: completionPrice,
      max_concurrency: Number(formConcurrency.value) || 4,
    });
    applyOpen.value = false;
    formModel.value = "";
    formName.value = "";
    formDescription.value = "";
    activeTab.value = "models";
    await loadApplications();
  } catch (err) {
    submitError.value = (err as Error).message;
  } finally {
    submitting.value = false;
  }
}

async function confirmWithdraw() {
  const target = withdrawTarget.value;
  withdrawTarget.value = null;
  if (!target) return;
  try {
    await withdrawEdgeApplication(target.id);
    await loadApplications();
  } catch (e) {
    applicationsError.value = (e as Error).message;
  }
}

function selectTab(tab: EdgeTab) {
  activeTab.value = tab;
  if (tab === "models") void loadApplications();
  if (tab === "earnings") {
    void loadSettlement();
    void loadEarnings();
  }
}

export function EdgeServing() {
  void locale.value;

  useEffect(() => {
    const requested = new URLSearchParams(window.location.search).get("tab");
    if (requested === "overview" || requested === "models" || requested === "earnings") {
      activeTab.value = requested;
    }
    void loadStatus().then(() => {
      if (status.value?.signed_in) {
        void loadApplications();
        void loadSettlement();
        void loadEarnings();
      }
    });
    const timer = setInterval(() => void loadStatus(), 3000);
    return () => clearInterval(timer);
  }, []);

  const current = status.value;
  const serving = applications.value.filter((a) => a.status === "approved").length;

  return (
    <div class="page-shell space-y-6">
      <div class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="text-xs font-semibold uppercase tracking-[0.28em] text-indigo-500">{t("edge.eyebrow")}</p>
          <h1 class="mt-2 text-3xl font-bold tracking-tight text-gray-950">{t("edge.title")}</h1>
          <p class="mt-2 max-w-2xl text-sm leading-6 text-gray-600">{t("edge.subtitle")}</p>
        </div>
        {current && <StatusControl status={current} />}
      </div>

      {locked.value ? (
        <LockedNotice />
      ) : !current ? (
        <section class="rounded-xl border border-gray-200 bg-white p-6 text-sm text-gray-400">
          {statusError.value ? <span class="text-red-600">{statusError.value}</span> : t("edge.loading")}
        </section>
      ) : !current.signed_in ? (
        <section class="rounded-xl border border-amber-200 bg-amber-50 p-6 text-sm text-amber-900">{t("edge.signInFirst")}</section>
      ) : (
        <>
          <div class="inline-flex rounded-2xl border border-gray-200 bg-white p-1 shadow-sm">
            <TabButton tab="overview" label={t("edge.tab.overview")} />
            <TabButton tab="models" label={t("edge.tab.models")} badge={serving > 0 ? String(serving) : ""} />
            <TabButton tab="earnings" label={t("edge.tab.earnings")} />
          </div>
          {activeTab.value === "overview" && <OverviewTab status={current} />}
          {activeTab.value === "models" && <ModelsTab />}
          {activeTab.value === "earnings" && <EarningsTab />}
        </>
      )}

      {applyOpen.value && current && <ApplyDialog status={current} />}
      <ConfirmDialog
        open={withdrawTarget.value !== null}
        title={t("edge.apps.withdrawTitle")}
        name={withdrawTarget.value?.public_model_id}
        description={t("edge.apps.withdrawConfirm")}
        confirmLabel={t("edge.apps.withdraw")}
        onConfirm={() => void confirmWithdraw()}
        onCancel={() => (withdrawTarget.value = null)}
      />
    </div>
  );
}

/** Shown instead of the feature when no Enterprise license is installed. */
function LockedNotice() {
  return (
    <section class="rounded-2xl border border-gray-200 bg-white px-8 py-12 text-center">
      <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-indigo-50 text-indigo-600">
        <svg class="h-6 w-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
        </svg>
      </div>
      <h2 class="mt-4 text-lg font-semibold text-gray-900">{t("edge.locked.title")}</h2>
      <p class="mx-auto mt-2 max-w-lg text-sm leading-6 text-gray-600">{t("edge.locked.desc")}</p>
      <a href="/settings#license" class="mt-6 inline-flex rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700">
        {t("edge.locked.action")}
      </a>
    </section>
  );
}

function TabButton({ tab, label, badge }: { tab: EdgeTab; label: string; badge?: string }) {
  const active = activeTab.value === tab;
  return (
    <button
      type="button"
      onClick={() => selectTab(tab)}
      class={`flex items-center gap-2 rounded-xl px-4 py-2 text-sm font-medium transition-colors ${
        active ? "bg-indigo-600 text-white shadow-sm" : "text-gray-600 hover:bg-gray-50 hover:text-gray-900"
      }`}
    >
      {label}
      {badge && (
        <span class={`rounded-full px-1.5 py-0.5 text-[10px] font-semibold leading-none ${active ? "bg-white/20 text-white" : "bg-indigo-50 text-indigo-700"}`}>
          {badge}
        </span>
      )}
    </button>
  );
}

/** The on/off switch and live state, always visible in the page header. */
function StatusControl({ status: st }: { status: EdgeStatus }) {
  return (
    <div class="flex items-center gap-3 rounded-2xl border border-gray-200 bg-white px-4 py-2.5 shadow-sm">
      <span class={`inline-block h-2.5 w-2.5 rounded-full ${edgeStateTone(st.state)}`} />
      <div class="text-sm">
        <div class="font-semibold text-gray-900">{t(`edge.state.${st.state}`)}</div>
        {st.state === "connected" && st.connected_since && (
          <div class="text-xs text-gray-500">{t("edge.connectedFor", sinceLabel(st.connected_since))}</div>
        )}
      </div>
      <button
        type="button"
        role="switch"
        aria-checked={st.enabled}
        aria-label={st.enabled ? t("edge.disable") : t("edge.enable")}
        title={st.enabled ? t("edge.disable") : t("edge.enable")}
        disabled={toggling.value || (!st.enabled && !st.signed_in)}
        onClick={() => void toggle()}
        class={`relative ml-2 inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors disabled:opacity-50 ${
          st.enabled ? "bg-indigo-600" : "bg-gray-300"
        }`}
      >
        <span class={`inline-block h-5 w-5 transform rounded-full bg-white shadow transition-transform ${st.enabled ? "translate-x-5" : "translate-x-0.5"}`} />
      </button>
    </div>
  );
}

// ---- overview --------------------------------------------------------------

function OverviewTab({ status: st }: { status: EdgeStatus }) {
  const apps = applications.value;
  const serving = apps.filter((a) => a.status === "approved" && a.node_status === "online").length;
  const approved = apps.filter((a) => a.status === "approved").length;
  const pending = apps.filter((a) => a.status === "pending").length;
  const e = earnings.value;
  return (
    <div class="space-y-6">
      <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label={t("edge.overview.serving")} value={`${serving} / ${approved}`} hint={t("edge.overview.servingHint")} />
        <Stat label={t("edge.overview.pending")} value={String(pending)} hint={t("edge.overview.pendingHint")} />
        <Stat label={t("edge.overview.monthRequests")} value={String(e?.requests ?? 0)} hint={e?.month} />
        <Stat label={t("edge.overview.monthEarning")} value={formatYuan(e?.owner_earning ?? 0)} hint={e?.month} strong />
      </div>

      <section class="rounded-xl border border-gray-200 bg-white p-6">
        <h2 class="text-base font-semibold text-gray-900">{t("edge.overview.connection")}</h2>
        <dl class="mt-4 grid gap-x-6 gap-y-3 text-sm sm:grid-cols-[8rem,1fr]">
          <dt class="text-gray-400">{t("edge.node")}</dt>
          <dd class="break-all text-gray-800">
            {st.node_name} <span class="font-mono text-xs text-gray-400">{st.node_uuid}</span>
          </dd>
          <dt class="text-gray-400">{t("edge.gateway")}</dt>
          <dd class="break-all font-mono text-xs text-gray-800">{st.gateway_url}</dd>
          <dt class="text-gray-400">{t("edge.overview.localModels")}</dt>
          <dd class="text-gray-800">
            {t("edge.overview.localModelsValue", st.models.filter((m) => m.ready).length, st.models.length)}
            <span class="text-gray-400">
              {" · "}
              {t("edge.overview.loadedSleeping", st.models.filter((m) => m.ready && m.state === "loaded").length, st.models.filter((m) => m.ready && m.state === "sleeping").length)}
            </span>
          </dd>
          <dt class="text-gray-400">{t("edge.overview.reconnects")}</dt>
          <dd class="text-gray-800">{st.reconnects}</dd>
        </dl>
        {st.last_error && st.state !== "connected" && (
          <p class="mt-4 rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 break-all">{st.last_error}</p>
        )}
        <p class="mt-4 text-xs leading-5 text-gray-500">{t("edge.networkHint")}</p>
      </section>

      <details class="group rounded-xl border border-indigo-100 bg-indigo-50/60 p-5 text-sm text-indigo-950">
        <summary class="cursor-pointer list-none font-semibold">
          <span class="mr-1 inline-block transition-transform group-open:rotate-90">›</span>
          {t("edge.privacy.title")}
        </summary>
        <ul class="mt-3 list-disc space-y-1 pl-6 leading-6 text-indigo-900">
          <li>{t("edge.privacy.paths")}</li>
          <li>{t("edge.privacy.identity")}</li>
          <li>{t("edge.privacy.noStore")}</li>
          <li>{t("edge.privacy.duty")}</li>
        </ul>
      </details>
    </div>
  );
}

// ---- my models -------------------------------------------------------------

function ModelsTab() {
  const all = applications.value;
  const live = all.filter((a) => isLiveApplication(a.status));
  const history = all.length - live.length;
  const rows = showHistory.value ? all : live;
  return (
    <section class="rounded-xl border border-gray-200 bg-white">
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-6 py-4">
        <div>
          <h2 class="text-base font-semibold text-gray-900">{t("edge.apps.title")}</h2>
          <p class="mt-0.5 text-xs text-gray-500">{t("edge.apps.desc")}</p>
        </div>
        <div class="flex items-center gap-3">
          {history > 0 && (
            <label class="flex items-center gap-1.5 text-xs text-gray-500">
              <input type="checkbox" checked={showHistory.value} onChange={(e) => (showHistory.value = (e.target as HTMLInputElement).checked)} />
              {t("edge.apps.showHistory", history)}
            </label>
          )}
          <button type="button" class="text-sm text-gray-500 hover:text-gray-800" onClick={() => void loadApplications()}>
            {t("edge.refresh")}
          </button>
          <button type="button" class="rounded-lg bg-indigo-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-indigo-700" onClick={openApply}>
            {t("edge.apps.apply")}
          </button>
        </div>
      </div>
      {applicationsError.value && <p class="px-6 pt-4 text-sm text-red-600 break-all">{applicationsError.value}</p>}
      {rows.length === 0 ? (
        <div class="px-6 py-12 text-center">
          <p class="text-sm text-gray-500">{t("edge.apps.empty")}</p>
          <button type="button" class="mt-3 text-sm font-medium text-indigo-600 hover:text-indigo-800" onClick={openApply}>
            {t("edge.apps.applyFirst")}
          </button>
        </div>
      ) : (
        <div class="overflow-x-auto">
          <table class="min-w-full text-sm">
            <thead class="bg-gray-50 text-left text-xs text-gray-500">
              <tr>
                <th class="px-6 py-2.5 font-medium">{t("edge.apps.model")}</th>
                <th class="px-3 py-2.5 font-medium">{t("edge.apps.review")}</th>
                <th class="px-3 py-2.5 font-medium">{t("edge.apps.servingCol")}</th>
                <th class="px-3 py-2.5 font-medium" title={t("edge.stats.columnHint")}>{t("edge.stats.column")}</th>
                <th class="px-3 py-2.5 font-medium">{t("edge.apps.price")}</th>
                <th class="px-3 py-2.5 font-medium">{t("edge.apps.shareCol")}</th>
                <th class="px-6 py-2.5" />
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100">
              {rows.map((app) => (
                <tr key={app.id} class={isLiveApplication(app.status) ? "" : "text-gray-400"}>
                  <td class="px-6 py-3 align-top">
                    <div class="flex items-baseline gap-2">
                      <span class="font-mono font-medium text-gray-900">{app.public_model_id}</span>
                      <span class="text-[11px] text-gray-400" title={t("edge.apps.idHint")}>#{app.id}</span>
                    </div>
                    <div class="mt-0.5 text-xs text-gray-400">{app.local_model_id}</div>
                    {app.review_msg && <div class="mt-1 text-xs text-gray-500">{t("edge.apps.reviewNote", app.review_msg)}</div>}
                  </td>
                  <td class="px-3 py-3 align-top whitespace-nowrap">
                    <span class={`inline-flex rounded-full px-2 py-0.5 text-xs ring-1 ring-inset ${applicationTone(app.status)}`}>
                      {t(`edge.appStatus.${app.status}`)}
                    </span>
                  </td>
                  <td class="px-3 py-3 align-top whitespace-nowrap text-xs">
                    <ServingCell app={app} />
                  </td>
                  <td class="px-3 py-3 align-top text-xs whitespace-nowrap text-gray-600">
                    {hasStats(app.stats) ? (
                      <>
                        <div>
                          {ttftLabel(app.stats) ? t("edge.stats.ttft", ttftLabel(app.stats)) : t("edge.stats.noStream")}
                          {app.stats.tokens_per_second > 0 && <span class="text-gray-400"> · {t("edge.stats.speed", app.stats.tokens_per_second)}</span>}
                        </div>
                        <div class="text-gray-400">
                          {t("edge.stats.uptime", onlineLabel(app.stats))}
                          {app.stats.requests > 0 && ` · ${t("edge.stats.success", percent(app.stats.success_rate))}`}
                        </div>
                      </>
                    ) : (
                      <span class="text-gray-300">—</span>
                    )}
                  </td>
                  <td class="px-3 py-3 align-top text-xs whitespace-nowrap text-gray-600">
                    <div>{t("edge.apps.inPrice", centsToYuan(app.prompt_price))}</div>
                    <div>{t("edge.apps.outPrice", centsToYuan(app.completion_price))}</div>
                  </td>
                  <td class="px-3 py-3 align-top text-xs text-gray-600">
                    {app.status === "approved" || app.status === "suspended" ? `${Math.round((1 - app.commission_rate) * 100)}%` : "—"}
                  </td>
                  <td class="px-6 py-3 align-top text-right">
                    {canWithdraw(app.status) && (
                      <button type="button" class="text-xs text-gray-400 hover:text-red-600" onClick={() => (withdrawTarget.value = app)}>
                        {t("edge.apps.withdraw")}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function ServingCell({ app }: { app: EdgeApplication }) {
  const state = servingState(app);
  if (state === "none") return <span class="text-gray-300">—</span>;
  const modelState = state === "serving" && (app.model_state === "loaded" || app.model_state === "loading" || app.model_state === "sleeping") ? app.model_state : undefined;
  return (
    <>
      <div class="flex items-center gap-1 text-gray-600">
        <span class={`inline-block h-1.5 w-1.5 rounded-full ${servingTone(state)}`} />
        {t(`edge.serving.${state}`)}
      </div>
      {modelState && (
        <div class="mt-0.5 text-gray-400" title={modelState === "sleeping" ? t("edge.modelState.sleepingHint") : undefined}>
          {t(`edge.modelState.${modelState}`)}
        </div>
      )}
    </>
  );
}

function ApplyDialog({ status: st }: { status: EdgeStatus }) {
  const models = st.models;
  const close = () => {
    if (!submitting.value) applyOpen.value = false;
  };
  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 p-4" onClick={close}>
      <section
        role="dialog"
        aria-modal="true"
        aria-labelledby="edge-apply-title"
        class="w-full max-w-xl overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <form onSubmit={(e) => void submit(e)}>
          <div class="border-b border-gray-100 px-6 py-4">
            <h2 id="edge-apply-title" class="text-lg font-semibold text-gray-900">{t("edge.form.title")}</h2>
            <p class="mt-1 text-xs text-gray-500">{t("edge.form.desc")}</p>
          </div>
          {models.length === 0 ? (
            <p class="px-6 py-8 text-sm text-gray-500">{t("edge.form.noModels")}</p>
          ) : (
            <div class="grid gap-4 px-6 py-5 sm:grid-cols-2">
              <label class="block text-sm sm:col-span-2">
                <span class="text-gray-700">{t("edge.form.model")}</span>
                <select
                  class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm"
                  value={formModel.value}
                  onChange={(e) => {
                    formModel.value = (e.target as HTMLSelectElement).value;
                    const picked = models.find((m) => m.id === formModel.value);
                    if (!formName.value) formName.value = suggestEdgeModelName(picked?.name || formModel.value);
                  }}
                >
                  <option value="">{t("edge.form.pickModel")}</option>
                  {models.map((m) => (
                    <option key={m.id} value={m.id} disabled={!m.ready}>
                      {m.name && m.name !== m.id ? `${m.name} (${m.id})` : m.id}
                      {m.ready ? "" : ` (${t("edge.form.notRunnable")})`}
                    </option>
                  ))}
                </select>
              </label>
              <label class="block text-sm sm:col-span-2">
                <span class="text-gray-700">{t("edge.form.name")}</span>
                <input
                  class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 font-mono text-sm"
                  value={formName.value}
                  onInput={(e) => (formName.value = (e.target as HTMLInputElement).value)}
                  placeholder="qwen3-8b"
                />
                <span class="mt-1 block text-xs text-gray-400">{t("edge.form.nameHint")}</span>
              </label>
              <label class="block text-sm">
                <span class="text-gray-700">{t("edge.form.promptPrice")}</span>
                <input
                  class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm"
                  inputMode="decimal"
                  value={formPromptPrice.value}
                  onInput={(e) => (formPromptPrice.value = (e.target as HTMLInputElement).value)}
                  placeholder="1.00"
                />
              </label>
              <label class="block text-sm">
                <span class="text-gray-700">{t("edge.form.completionPrice")}</span>
                <input
                  class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm"
                  inputMode="decimal"
                  value={formCompletionPrice.value}
                  onInput={(e) => (formCompletionPrice.value = (e.target as HTMLInputElement).value)}
                  placeholder="4.00"
                />
              </label>
              <label class="block text-sm">
                <span class="text-gray-700">{t("edge.form.concurrency")}</span>
                <input
                  class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm"
                  type="number"
                  min={1}
                  max={256}
                  value={formConcurrency.value}
                  onInput={(e) => (formConcurrency.value = (e.target as HTMLInputElement).value)}
                />
              </label>
              <label class="block text-sm sm:col-span-2">
                <span class="text-gray-700">{t("edge.form.description")}</span>
                <textarea
                  class="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm"
                  rows={2}
                  value={formDescription.value}
                  onInput={(e) => (formDescription.value = (e.target as HTMLTextAreaElement).value)}
                />
              </label>
            </div>
          )}
          {submitError.value && <p class="px-6 pb-3 text-sm text-red-600 break-all">{submitError.value}</p>}
          <div class="flex items-center justify-between gap-3 border-t border-gray-100 bg-gray-50 px-6 py-4">
            <span class="text-xs text-gray-500">{t("edge.form.reviewHint")}</span>
            <div class="flex gap-3">
              <button type="button" class="rounded-lg border border-gray-300 bg-white px-4 py-2 text-sm text-gray-700 hover:bg-gray-50" onClick={close}>
                {t("edge.form.cancel")}
              </button>
              <button
                type="submit"
                class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50"
                disabled={submitting.value || models.length === 0}
              >
                {submitting.value ? t("edge.form.submitting") : t("edge.form.submit")}
              </button>
            </div>
          </div>
        </form>
      </section>
    </div>
  );
}

// ---- earnings --------------------------------------------------------------

function EarningsTab() {
  const e = earnings.value;
  const b = balance.value;
  const rows = e ? mergeEarningsByModel(e.rows) : [];
  return (
    <div class="space-y-6">
      {earningsError.value && <p class="text-sm text-red-600 break-all">{earningsError.value}</p>}
      {settlementError.value && <p class="text-sm text-red-600 break-all">{settlementError.value}</p>}
      <section class="rounded-xl border border-gray-200 bg-white p-5">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 class="text-base font-semibold text-gray-900">{t("edge.balance.title")}</h2>
            <p class="mt-0.5 text-xs text-gray-500">{t("edge.balance.desc")}</p>
          </div>
          {b?.statements_url && (
            <a
              href={b.statements_url}
              target="_blank"
              rel="noreferrer"
              class="rounded-lg border border-indigo-200 bg-indigo-50 px-3 py-1.5 text-sm font-medium text-indigo-700 hover:bg-indigo-100"
            >
              {t("edge.balance.manage")}
            </a>
          )}
        </div>
        <div class="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Stat label={t("edge.balance.total")} value={formatYuan(b?.balance.total ?? 0)} strong />
          <Stat label={t("edge.balance.settled")} value={formatYuan(b?.balance.settled ?? 0)} />
          <Stat label={t("edge.balance.unsettled")} value={formatYuan(b?.balance.unsettled ?? 0)} hint={t("edge.balance.unsettledHint")} />
          <Stat label={t("edge.balance.currentMonth")} value={formatYuan(b?.balance.current_month ?? 0)} hint={t("edge.balance.currentMonthHint")} />
        </div>
      </section>
      <section class="rounded-xl border border-gray-200 bg-white">
        <div class="border-b border-gray-100 px-6 py-4">
          <h2 class="text-base font-semibold text-gray-900">{t("edge.statements.title")}</h2>
          <p class="mt-0.5 text-xs text-gray-500">{t("edge.statements.desc")}</p>
        </div>
        {statements.value.length === 0 ? (
          <p class="px-6 py-10 text-center text-sm text-gray-400">{t("edge.statements.empty")}</p>
        ) : (
          <div class="overflow-x-auto">
            <table class="min-w-full text-sm">
              <thead class="bg-gray-50 text-left text-xs text-gray-500">
                <tr>
                  <th class="px-6 py-2.5 font-medium">{t("edge.statements.month")}</th>
                  <th class="px-3 py-2.5 text-right font-medium">{t("edge.earn.requests")}</th>
                  <th class="px-3 py-2.5 text-right font-medium">{t("edge.earn.amount")}</th>
                  <th class="px-3 py-2.5 text-right font-medium">{t("edge.statements.platform")}</th>
                  <th class="px-3 py-2.5 text-right font-medium">{t("edge.earn.mine")}</th>
                  <th class="px-6 py-2.5 font-medium">{t("edge.statements.status")}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100">
                {statements.value.map((st) => (
                  <tr
                    key={st.month}
                    class={`cursor-pointer hover:bg-gray-50 ${st.month === month.value ? "bg-indigo-50/60" : ""}`}
                    onClick={() => chooseMonth(st.month)}
                  >
                    <td class="px-6 py-2.5 tabular-nums">{formatMonth(st.month, locale.value)}</td>
                    <td class="px-3 py-2.5 text-right tabular-nums">{st.requests}</td>
                    <td class="px-3 py-2.5 text-right tabular-nums">{formatYuan(st.amount)}</td>
                    <td class="px-3 py-2.5 text-right tabular-nums">{formatYuan(st.platform_amount)}</td>
                    <td class="px-3 py-2.5 text-right font-semibold tabular-nums">{formatYuan(st.owner_amount)}</td>
                    <td class="px-6 py-2.5">
                      <span class={`inline-flex rounded-full px-2 py-0.5 text-xs ring-1 ring-inset ${statementTone(st.status)}`}>
                        {t(`edge.statements.${st.status}`)}
                      </span>
                      {st.settlement && (
                        <div class="mt-0.5 text-xs text-gray-400">
                          {t("edge.statements.reference", st.settlement.reference)}
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
      <div class="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 class="text-base font-semibold text-gray-900">{t("edge.earn.monthTitle")}</h2>
          <p class="mt-0.5 text-xs text-gray-500">{t("edge.earn.desc")}</p>
        </div>
        <MonthPicker
          value={month.value}
          max={currentMonth()}
          locale={locale.value}
          label={t("edge.earn.monthTitle")}
          prevYearLabel={t("edge.earn.prevYear")}
          nextYearLabel={t("edge.earn.nextYear")}
          onChange={chooseMonth}
        />
      </div>
      <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label={t("edge.earn.requests")} value={String(e?.requests ?? 0)} />
        <Stat label={t("edge.earn.tokens")} value={String((e?.prompt_tokens ?? 0) + (e?.completion_tokens ?? 0))} />
        <Stat label={t("edge.earn.amount")} value={formatYuan(e?.amount ?? 0)} />
        <Stat label={t("edge.earn.mine")} value={formatYuan(e?.owner_earning ?? 0)} strong />
      </div>
      <section class="rounded-xl border border-gray-200 bg-white">
        <h2 class="border-b border-gray-100 px-6 py-4 text-base font-semibold text-gray-900">{t("edge.earn.byModel")}</h2>
        {rows.length === 0 ? (
          <p class="px-6 py-10 text-center text-sm text-gray-400">{t("edge.earn.empty")}</p>
        ) : (
          <div class="overflow-x-auto">
            <table class="min-w-full text-sm">
              <thead class="bg-gray-50 text-left text-xs text-gray-500">
                <tr>
                  <th class="px-6 py-2.5 font-medium">{t("edge.apps.model")}</th>
                  <th class="px-3 py-2.5 text-right font-medium">{t("edge.earn.requests")}</th>
                  <th class="px-3 py-2.5 text-right font-medium">{t("edge.earn.tokens")}</th>
                  <th class="px-3 py-2.5 text-right font-medium">{t("edge.earn.amount")}</th>
                  <th class="px-6 py-2.5 text-right font-medium">{t("edge.earn.mine")}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100">
                {rows.map((row) => (
                  <tr key={row.public_model_id}>
                    <td class="px-6 py-2.5 font-mono">{row.public_model_id}</td>
                    <td class="px-3 py-2.5 text-right tabular-nums">{row.requests}</td>
                    <td class="px-3 py-2.5 text-right tabular-nums">{row.prompt_tokens + row.completion_tokens}</td>
                    <td class="px-3 py-2.5 text-right tabular-nums">{formatYuan(row.amount)}</td>
                    <td class="px-6 py-2.5 text-right font-semibold tabular-nums">{formatYuan(row.owner_earning)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}

function chooseMonth(value: string) {
  if (value === month.value) return;
  month.value = value;
  void loadEarnings();
}

function Stat({ label, value, hint, strong }: { label: string; value: string; hint?: string; strong?: boolean }) {
  return (
    <div class="rounded-xl border border-gray-200 bg-white p-4">
      <div class="text-xs text-gray-500">{label}</div>
      <div class={`mt-1 text-2xl tabular-nums ${strong ? "font-bold text-indigo-700" : "font-semibold text-gray-900"}`}>{value}</div>
      {hint && <div class="mt-0.5 text-xs text-gray-400">{hint}</div>}
    </div>
  );
}
