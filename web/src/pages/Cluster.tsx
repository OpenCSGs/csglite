import { useEffect } from "preact/hooks";
import { useLocation } from "preact-iso";
import { signal } from "@preact/signals";
import {
  createCluster,
  explainClusterScheduling,
  getCluster,
  getClusterCode,
  getClusterModels,
  getClusterRecommendations,
  getClusterToken,
  getDiscoveredClusterNodes,
  inviteClusterNode,
  isClusterDisabledError,
  isFeatureNotLicensedError,
  joinCluster,
  leaveCluster,
  removeClusterNode,
  rotateClusterToken,
  setClusterNodeState,
  syncClusterModel,
  updateClusterNode,
  updateClusterSettings, enableCluster } from "../api/client";
import type {
  ClusterCodeResponse,
  ClusterExplainResponse,
  ClusterModelDistribution,
  ClusterNodeState,
  ClusterNodeView,
  ClusterRecommendation,
  ClusterRoutingMode,
  ClusterSettings,
  ClusterSyncResponse,
  ClusterTokenResponse,
  ClusterView,
  DiscoveredClusterNode,
} from "../api/client";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { t, locale } from "../i18n";
import {
  fmtGB,
  fmtGBPair,
  fmtSeconds,
  healthDotClass,
  healthKey,
  healthTone,
  isValidAdmissionCode,
  nodeLimitLabel,
  normalizeAdmissionCode,
  percentOf,
  secondsUntil,
  stateKey,
} from "../cluster";

type ClusterTab = "overview" | "discovered" | "models" | "settings";

const CLUSTER_DISABLED_ENV = "CSGHUB_LITE_CLUSTER_DISABLED";
const JOIN_TOKEN_ENV = "CSGHUB_LITE_CLUSTER_JOIN_TOKEN";

// ---- page state ------------------------------------------------------------
const activeTab = signal<ClusterTab>("overview");
const view = signal<ClusterView | null>(null);
const viewLoaded = signal(false);
const viewError = signal("");
const featureDisabled = signal(false);

/** Set when join/invite hits the Community node cap (403 feature_not_licensed). */
const capError = signal<{ limit: number; current: number } | null>(null);
const copied = signal("");

// ---- not in a cluster ------------------------------------------------------
const createName = signal("");
const creating = signal(false);
const createError = signal("");
const createdToken = signal("");
const joinToken = signal("");
const joinAddress = signal("");
const joining = signal(false);
const joinError = signal("");
const admissionCode = signal<ClusterCodeResponse | null>(null);
const admissionCodeError = signal("");
const codeCountdown = signal<number | null>(null);

// ---- discovered ------------------------------------------------------------
const discovered = signal<DiscoveredClusterNode[]>([]);
const discoveredLoaded = signal(false);
const inviteTarget = signal<DiscoveredClusterNode | null>(null);
const inviteCode = signal("");
const inviteAddress = signal("");
const inviting = signal(false);
const inviteError = signal("");
const inviteDone = signal("");

// ---- overview --------------------------------------------------------------
const nameEditing = signal(false);
const nameDraft = signal("");
const nameSaving = signal(false);
const staticEditUUID = signal("");
const staticDraft = signal("");
const staticSaving = signal(false);
const removeTarget = signal<ClusterNodeView | null>(null);
const removing = signal(false);
const stateSaving = signal(false);
const overviewError = signal("");

// ---- models ----------------------------------------------------------------
const models = signal<ClusterModelDistribution[]>([]);
const modelsLoading = signal(false);
const modelsError = signal("");
const syncBusy = signal("");
const syncResults = signal<Record<string, ClusterSyncResponse | { error: string }>>({});
const recommendations = signal<ClusterRecommendation[]>([]);
const applyBusy = signal("");
const applyResults = signal<Record<string, string>>({});
const explainModel = signal("");
const explainPromptTokens = signal("");
const explainMaxTokens = signal("");
const explainBusy = signal(false);
const explainError = signal("");
const explainResult = signal<ClusterExplainResponse | null>(null);

// ---- settings --------------------------------------------------------------
const tokenInfo = signal<ClusterTokenResponse | null>(null);
const tokenError = signal("");
const rotating = signal(false);
const settingsForm = signal<ClusterSettings | null>(null);
const settingsSaving = signal(false);
const settingsError = signal("");
const settingsSaved = signal(false);
const leaveOpen = signal(false);
const leaving = signal(false);

function errorMessage(err: unknown): string {
  return err instanceof Error && err.message ? err.message : t("cluster.unknownError");
}

/** Records the node-cap error for the banner; returns true when it was one. */
function captureCapError(err: unknown): boolean {
  if (isFeatureNotLicensedError(err)) {
    capError.value = { limit: err.limit ?? 0, current: err.current ?? 0 };
    return true;
  }
  return false;
}

async function copyText(text: string, key: string) {
  try {
    await navigator.clipboard.writeText(text);
    copied.value = key;
    setTimeout(() => {
      if (copied.value === key) copied.value = "";
    }, 1500);
  } catch {
    /* clipboard unavailable; the value is still selectable */
  }
}

async function loadView() {
  if (featureDisabled.value) return;
  try {
    const next = await getCluster();
    view.value = next;
    viewError.value = "";
    if (!next.cluster) {
      // Leaving resets everything that only makes sense inside a cluster.
      activeTab.value = "overview";
    }
  } catch (err) {
    if (isClusterDisabledError(err)) {
      featureDisabled.value = true;
    } else {
      viewError.value = errorMessage(err);
    }
  } finally {
    viewLoaded.value = true;
  }
}

async function loadDiscovered() {
  if (featureDisabled.value) return;
  try {
    discovered.value = await getDiscoveredClusterNodes();
  } catch {
    /* transient; keep the last list */
  } finally {
    discoveredLoaded.value = true;
  }
}

async function loadAdmissionCode() {
  if (featureDisabled.value) return;
  if (view.value?.cluster) return;
  try {
    admissionCode.value = await getClusterCode();
    admissionCodeError.value = "";
  } catch (err) {
    admissionCodeError.value = errorMessage(err);
  }
}

async function loadModels() {
  modelsLoading.value = true;
  try {
    const [list, recs] = await Promise.all([getClusterModels(), getClusterRecommendations().catch(() => [])]);
    models.value = list;
    recommendations.value = recs;
    modelsError.value = "";
    if (!explainModel.value && list.length > 0) explainModel.value = list[0].id;
  } catch (err) {
    modelsError.value = errorMessage(err);
  } finally {
    modelsLoading.value = false;
  }
}

async function loadToken() {
  try {
    tokenInfo.value = await getClusterToken();
    tokenError.value = "";
  } catch (err) {
    tokenError.value = errorMessage(err);
  }
}

async function handleCreate() {
  const name = createName.value.trim();
  if (!name) {
    createError.value = t("cluster.createNameRequired");
    return;
  }
  creating.value = true;
  createError.value = "";
  try {
    const result = await createCluster(name);
    createdToken.value = result.join_token;
    await loadView();
  } catch (err) {
    createError.value = errorMessage(err);
  } finally {
    creating.value = false;
  }
}

async function handleJoin() {
  const token = joinToken.value.trim();
  if (!token) {
    joinError.value = t("cluster.joinTokenRequired");
    return;
  }
  joining.value = true;
  joinError.value = "";
  capError.value = null;
  try {
    view.value = await joinCluster(token, joinAddress.value);
    joinToken.value = "";
    joinAddress.value = "";
  } catch (err) {
    if (!captureCapError(err)) joinError.value = errorMessage(err);
  } finally {
    joining.value = false;
  }
}

function openInvite(node: DiscoveredClusterNode) {
  inviteTarget.value = node;
  inviteCode.value = "";
  inviteAddress.value = "";
  inviteError.value = "";
  inviteDone.value = "";
  capError.value = null;
}

function closeInvite() {
  if (inviting.value) return;
  inviteTarget.value = null;
}

async function handleInvite() {
  const target = inviteTarget.value;
  if (!target) return;
  if (!isValidAdmissionCode(inviteCode.value)) {
    inviteError.value = t("cluster.inviteCodeInvalid");
    return;
  }
  inviting.value = true;
  inviteError.value = "";
  try {
    const node = await inviteClusterNode(normalizeAdmissionCode(inviteCode.value), {
      uuid: target.uuid,
      address: inviteAddress.value || undefined,
    });
    inviteDone.value = node.name || target.name;
    inviteTarget.value = null;
    await Promise.all([loadView(), loadDiscovered()]);
  } catch (err) {
    if (captureCapError(err)) {
      inviteTarget.value = null;
    } else {
      inviteError.value = errorMessage(err);
    }
  } finally {
    inviting.value = false;
  }
}

function startNameEdit(current: string) {
  nameDraft.value = current;
  nameEditing.value = true;
  overviewError.value = "";
}

async function saveName(uuid: string) {
  const name = nameDraft.value.trim();
  if (!name) {
    overviewError.value = t("cluster.nodeNameRequired");
    return;
  }
  nameSaving.value = true;
  try {
    await updateClusterNode(uuid, { name });
    nameEditing.value = false;
    await loadView();
  } catch (err) {
    overviewError.value = errorMessage(err);
  } finally {
    nameSaving.value = false;
  }
}

function startStaticEdit(node: ClusterNodeView) {
  staticEditUUID.value = node.uuid;
  staticDraft.value = node.static_address || "";
  overviewError.value = "";
}

async function saveStaticAddress() {
  const uuid = staticEditUUID.value;
  if (!uuid) return;
  staticSaving.value = true;
  try {
    await updateClusterNode(uuid, { static_address: staticDraft.value.trim() });
    staticEditUUID.value = "";
    await loadView();
  } catch (err) {
    overviewError.value = errorMessage(err);
  } finally {
    staticSaving.value = false;
  }
}

async function confirmRemove() {
  const target = removeTarget.value;
  if (!target) return;
  removing.value = true;
  try {
    view.value = await removeClusterNode(target.uuid);
    removeTarget.value = null;
  } catch (err) {
    overviewError.value = errorMessage(err);
  } finally {
    removing.value = false;
  }
}

async function changeLocalState(uuid: string, state: ClusterNodeState) {
  stateSaving.value = true;
  overviewError.value = "";
  try {
    const settings = await setClusterNodeState(uuid, state);
    if (view.value) view.value = { ...view.value, settings };
    await loadView();
  } catch (err) {
    overviewError.value = errorMessage(err);
  } finally {
    stateSaving.value = false;
  }
}

async function handleSync(model: string, nodes: string[] | "all", resultKey: string) {
  syncBusy.value = resultKey;
  try {
    const result = await syncClusterModel(model, nodes);
    syncResults.value = { ...syncResults.value, [resultKey]: result };
  } catch (err) {
    syncResults.value = { ...syncResults.value, [resultKey]: { error: errorMessage(err) } };
  } finally {
    syncBusy.value = "";
  }
}

async function applyRecommendation(rec: ClusterRecommendation) {
  const key = `${rec.model}::${rec.node_uuid}`;
  applyBusy.value = key;
  try {
    const result = await syncClusterModel(rec.model, [rec.node_uuid]);
    const first = result.results[0];
    applyResults.value = {
      ...applyResults.value,
      [key]: first?.error
        ? t("cluster.syncError", first.error)
        : first?.skipped
          ? t("cluster.syncSkipped", first.skipped)
          : t("cluster.syncStarted"),
    };
  } catch (err) {
    applyResults.value = { ...applyResults.value, [key]: t("cluster.syncError", errorMessage(err)) };
  } finally {
    applyBusy.value = "";
  }
}

async function handleExplain() {
  const model = explainModel.value;
  if (!model) return;
  explainBusy.value = true;
  explainError.value = "";
  try {
    const promptTokens = explainPromptTokens.value.trim() ? Number(explainPromptTokens.value) : undefined;
    const maxTokens = explainMaxTokens.value.trim() ? Number(explainMaxTokens.value) : undefined;
    explainResult.value = await explainClusterScheduling(model, { promptTokens, maxTokens });
  } catch (err) {
    explainResult.value = null;
    explainError.value = errorMessage(err);
  } finally {
    explainBusy.value = false;
  }
}

async function handleRotate() {
  rotating.value = true;
  tokenError.value = "";
  try {
    tokenInfo.value = await rotateClusterToken();
    await loadView();
  } catch (err) {
    tokenError.value = errorMessage(err);
  } finally {
    rotating.value = false;
  }
}

function openSettingsTab(current: ClusterSettings | undefined) {
  activeTab.value = "settings";
  settingsSaved.value = false;
  settingsError.value = "";
  if (current) settingsForm.value = { ...current };
  void loadToken();
}

async function saveSettings() {
  const form = settingsForm.value;
  if (!form) return;
  const weight = Number(form.weight);
  if (!Number.isFinite(weight) || weight < 10 || weight > 200) {
    settingsError.value = t("cluster.weightRange");
    return;
  }
  settingsSaving.value = true;
  settingsError.value = "";
  settingsSaved.value = false;
  try {
    const saved = await updateClusterSettings({
      accept_work: form.accept_work,
      prefer_local: form.prefer_local,
      routing_mode: form.routing_mode,
      affinity_max_queue: Math.max(0, Math.round(Number(form.affinity_max_queue) || 0)),
      disk_reserve_gb: Math.max(0, Number(form.disk_reserve_gb) || 0),
      weight: Math.round(weight),
    });
    settingsForm.value = { ...saved };
    settingsSaved.value = true;
    if (view.value) view.value = { ...view.value, settings: saved };
  } catch (err) {
    settingsError.value = errorMessage(err);
  } finally {
    settingsSaving.value = false;
  }
}

async function confirmLeave() {
  leaving.value = true;
  try {
    view.value = await leaveCluster();
    leaveOpen.value = false;
    tokenInfo.value = null;
    createdToken.value = "";
    activeTab.value = "overview";
    await Promise.all([loadAdmissionCode(), loadDiscovered()]);
  } catch (err) {
    settingsError.value = errorMessage(err);
  } finally {
    leaving.value = false;
  }
}

// ---- page ------------------------------------------------------------------

export function Cluster() {
  void locale.value;

  useEffect(() => {
    void loadView().then(() => {
      void loadAdmissionCode();
      void loadDiscovered();
    });
    const viewTimer = setInterval(() => void loadView(), 3000);
    const discoveredTimer = setInterval(() => void loadDiscovered(), 5000);
    const codeTimer = setInterval(() => void loadAdmissionCode(), 30000);
    const countdown = setInterval(() => {
      codeCountdown.value = secondsUntil(admissionCode.value?.expires_at);
      if (codeCountdown.value === 0) void loadAdmissionCode();
    }, 1000);
    return () => {
      clearInterval(viewTimer);
      clearInterval(discoveredTimer);
      clearInterval(codeTimer);
      clearInterval(countdown);
    };
  }, []);

  const current = view.value;

  return (
    <div class="page-shell space-y-6">
      <div class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="text-xs font-semibold uppercase tracking-[0.28em] text-indigo-500">{t("cluster.eyebrow")}</p>
          <h1 class="mt-2 text-3xl font-bold tracking-tight text-gray-950">{t("cluster.title")}</h1>
          <p class="mt-2 max-w-2xl text-sm leading-6 text-gray-600">{t("cluster.subtitle")}</p>
        </div>
        {current?.cluster && <HeaderStats view={current} />}
      </div>

      {featureDisabled.value ? (
        <section class="rounded-xl border border-amber-200 bg-amber-50 p-6 text-sm text-amber-900">
          <p class="font-semibold">{t("cluster.disabledTitle")}</p>
          <p class="mt-1">{t("cluster.disabledDesc", CLUSTER_DISABLED_ENV)}</p>
        </section>
      ) : !viewLoaded.value ? (
        <section class="bg-white rounded-xl border border-gray-200 p-6 text-sm text-gray-400">{t("cluster.loading")}</section>
      ) : !current ? (
        <section class="rounded-xl border border-red-200 bg-red-50 p-6 text-sm text-red-700">
          <p>{t("cluster.loadFailed")}</p>
          {viewError.value && <p class="mt-1 font-mono text-xs">{viewError.value}</p>}
        </section>
      ) : !current.cluster ? (
        <NotInCluster view={current} />
      ) : (
        <InCluster view={current} />
      )}
    </div>
  );
}

function HeaderStats({ view }: { view: ClusterView }) {
  const limit = nodeLimitLabel(view.members.length, view.node_limit);
  const online = view.members.filter((m) => m.online).length;
  const vram = view.members.reduce(
    (acc, m) => {
      for (const gpu of m.status?.gpus ?? []) {
        acc.total += gpu.vram_total;
        acc.used += gpu.vram_used;
      }
      return acc;
    },
    { total: 0, used: 0 },
  );
  const inflight = view.members.reduce((sum, m) => sum + (m.status?.inflight ?? 0), 0);
  return (
    <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
      <Stat label={t("cluster.statOnline")} value={`${online} / ${view.members.length}`} />
      <Stat label={t("cluster.statNodes")} value={t(limit.key, ...limit.args)} tone={limit.atLimit ? "warn" : "normal"} />
      <Stat label={t("cluster.statVRAM")} value={fmtGBPair(vram.used, vram.total)} />
      <Stat label={t("cluster.statInflight")} value={String(inflight)} />
    </div>
  );
}

function Stat({ label, value, tone = "normal" }: { label: string; value: string; tone?: "normal" | "warn" }) {
  return (
    <div class={`rounded-xl border px-4 py-3 ${tone === "warn" ? "border-amber-200 bg-amber-50" : "border-gray-200 bg-white"}`}>
      <div class="text-[11px] font-medium uppercase tracking-wide text-gray-400">{label}</div>
      <div class={`mt-0.5 text-sm font-semibold ${tone === "warn" ? "text-amber-800" : "text-gray-900"}`}>{value}</div>
    </div>
  );
}

function CapBanner() {
  const cap = capError.value;
  const { route } = useLocation();
  if (!cap) return null;
  return (
    <div class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
      <span>{t("cluster.capReached", cap.limit, cap.current)}</span>
      <div class="flex items-center gap-3">
        <a
          href="/settings"
          onClick={(event) => {
            event.preventDefault();
            route("/settings");
          }}
          class="font-medium text-indigo-700 hover:underline"
        >
          {t("cluster.capReachedLink")}
        </a>
        <button type="button" class="text-amber-700 hover:text-amber-900" onClick={() => (capError.value = null)} aria-label={t("dash.close")}>
          ×
        </button>
      </div>
    </div>
  );
}

function CopyButton({ text, id, tone = "gray" }: { text: string; id: string; tone?: "gray" | "amber" }) {
  const cls = tone === "amber"
    ? "border-amber-200 bg-white text-amber-900 hover:bg-amber-100"
    : "border-gray-200 bg-white text-gray-700 hover:bg-gray-50";
  return (
    <button type="button" onClick={() => void copyText(text, id)} class={`shrink-0 rounded-lg border px-3 py-2 text-sm transition-colors ${cls}`}>
      {copied.value === id ? t("dash.copied") : t("dash.copy")}
    </button>
  );
}

// ---- not in a cluster ------------------------------------------------------

const enablingCluster = signal(false);

function DormantNotice({ current }: { current: ClusterView }) {
  if (current.active !== false) return null;
  const enable = async () => {
    enablingCluster.value = true;
    try {
      view.value = await enableCluster();
    } catch (err: any) {
      viewError.value = err?.message || t("cluster.unknownError");
    } finally {
      enablingCluster.value = false;
    }
  };
  return (
    <div class="rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 text-sm text-gray-700 flex flex-wrap items-center justify-between gap-3">
      <div>
        <div class="font-semibold text-gray-900">{t("cluster.dormantTitle")}</div>
        <p class="mt-1">{t("cluster.dormantDesc")}</p>
      </div>
      <button
        type="button"
        disabled={enablingCluster.value}
        onClick={enable}
        class="px-3 py-1.5 text-sm rounded-lg bg-indigo-600 text-white hover:bg-indigo-700 disabled:opacity-50"
      >
        {enablingCluster.value ? t("cluster.enabling") : t("cluster.enableAction")}
      </button>
    </div>
  );
}

function AutoFormNotice({ view }: { view: ClusterView }) {
  if (!view.auto_form) return null;
  return (
    <div class="rounded-xl border border-indigo-200 bg-indigo-50 px-4 py-3 text-sm text-indigo-900">
      <div class="font-semibold">{t("cluster.autoFormTitle")}</div>
      <p class="mt-1 text-indigo-800">{view.auto_form_paused ? t("cluster.autoFormPaused") : t("cluster.autoFormDesc")}</p>
    </div>
  );
}

function NotInCluster({ view }: { view: ClusterView }) {
  const code = admissionCode.value;
  const remaining = codeCountdown.value ?? secondsUntil(code?.expires_at);
  return (
    <>
      <CapBanner />
      <DormantNotice current={view} />
      <AutoFormNotice view={view} />
      <div class="grid gap-6 lg:grid-cols-2">
        {/* Create */}
        <section class="bg-white rounded-xl border border-gray-200 p-6">
          <h2 class="text-lg font-bold text-gray-900">{t("cluster.createTitle")}</h2>
          <p class="mt-1 text-sm text-gray-500">{t("cluster.createDesc")}</p>
          {createdToken.value ? (
            <div class="mt-4 rounded-xl border border-amber-200 bg-amber-50 p-4">
              <p class="text-sm font-semibold text-amber-900">{t("cluster.joinTokenCreated")}</p>
              <div class="mt-2 flex gap-2">
                <input
                  readOnly
                  class="min-w-0 flex-1 rounded-lg border border-amber-200 bg-white px-3 py-2 font-mono text-sm text-amber-900"
                  value={createdToken.value}
                  onFocus={(e) => (e.target as HTMLInputElement).select()}
                />
                <CopyButton text={createdToken.value} id="created-token" tone="amber" />
              </div>
              <p class="mt-2 text-xs text-amber-800">{t("cluster.joinTokenHint", "csghub-lite cluster join <token>", JOIN_TOKEN_ENV)}</p>
            </div>
          ) : (
            <div class="mt-4 space-y-3">
              <div>
                <label class="mb-1 block text-sm font-medium text-gray-700">{t("cluster.createName")}</label>
                <input
                  class="w-full rounded-lg border border-gray-200 px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  value={createName.value}
                  placeholder={t("cluster.createNamePlaceholder")}
                  disabled={creating.value}
                  onInput={(e) => (createName.value = (e.target as HTMLInputElement).value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") void handleCreate();
                  }}
                />
              </div>
              {createError.value && <p class="text-sm text-red-600">{createError.value}</p>}
              <button
                type="button"
                onClick={() => void handleCreate()}
                disabled={creating.value}
                class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
              >
                {creating.value ? t("cluster.creating") : t("cluster.createAction")}
              </button>
            </div>
          )}
        </section>

        {/* Join */}
        <section class="bg-white rounded-xl border border-gray-200 p-6">
          <h2 class="text-lg font-bold text-gray-900">{t("cluster.joinTitle")}</h2>
          <p class="mt-1 text-sm text-gray-500">{t("cluster.joinDesc")}</p>
          <div class="mt-4 space-y-3">
            <div>
              <label class="mb-1 block text-sm font-medium text-gray-700">{t("cluster.joinToken")}</label>
              <input
                class="w-full rounded-lg border border-gray-200 px-3 py-2.5 font-mono text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={joinToken.value}
                placeholder={t("cluster.joinTokenPlaceholder")}
                disabled={joining.value}
                spellcheck={false}
                onInput={(e) => (joinToken.value = (e.target as HTMLInputElement).value)}
              />
            </div>
            <div>
              <label class="mb-1 block text-sm font-medium text-gray-700">{t("cluster.joinAddress")}</label>
              <input
                class="w-full rounded-lg border border-gray-200 px-3 py-2.5 font-mono text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={joinAddress.value}
                placeholder={t("cluster.joinAddressPlaceholder")}
                disabled={joining.value}
                spellcheck={false}
                onInput={(e) => (joinAddress.value = (e.target as HTMLInputElement).value)}
              />
              <p class="mt-1 text-xs text-gray-400">{t("cluster.joinAddressHint")}</p>
            </div>
            {joinError.value && <p class="text-sm text-red-600">{joinError.value}</p>}
            <button
              type="button"
              onClick={() => void handleJoin()}
              disabled={joining.value}
              class="rounded-lg border border-indigo-200 bg-indigo-50 px-4 py-2 text-sm font-medium text-indigo-700 transition-colors hover:bg-indigo-100 disabled:opacity-60"
            >
              {joining.value ? t("cluster.joining") : t("cluster.joinAction")}
            </button>
          </div>
        </section>
      </div>

      {/* Admission code */}
      <section class="bg-white rounded-xl border border-gray-200 p-6">
        <div class="flex flex-wrap items-center justify-between gap-4">
          <div>
            <h2 class="text-lg font-bold text-gray-900">{t("cluster.admissionTitle")}</h2>
            <p class="mt-1 text-sm text-gray-500">{t("cluster.admissionDesc", view.node.name)}</p>
          </div>
          {code ? (
            <div class="flex items-center gap-3">
              <span class="rounded-xl border border-gray-200 bg-gray-50 px-4 py-2 font-mono text-2xl font-semibold tracking-[0.3em] text-gray-900">
                {code.code}
              </span>
              <div class="text-xs text-gray-400">
                {remaining !== null && remaining > 0 ? t("cluster.admissionExpiresIn", fmtSeconds(remaining)) : t("cluster.admissionRefreshing")}
              </div>
              <CopyButton text={code.code} id="admission-code" />
            </div>
          ) : (
            <span class="text-sm text-gray-400">{admissionCodeError.value || t("cluster.loading")}</span>
          )}
        </div>
      </section>

      <DiscoveredSection readOnly />
    </>
  );
}

// ---- in a cluster ----------------------------------------------------------

function InCluster({ view }: { view: ClusterView }) {
  return (
    <>
      {view.model_source_mixed && (
        <div class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
          <span class="font-semibold">{t("cluster.mixedSourceTitle")}</span> {t("cluster.mixedSourceDesc")}
        </div>
      )}
      <CapBanner />
      <div class="inline-flex rounded-2xl border border-gray-200 bg-white p-1 shadow-sm">
        <TabButton tab="overview" label={t("cluster.tabOverview")} onSelect={() => (activeTab.value = "overview")} />
        <TabButton
          tab="discovered"
          label={t("cluster.tabDiscovered")}
          badge={view.discovered_count > 0 ? String(view.discovered_count) : ""}
          onSelect={() => (activeTab.value = "discovered")}
        />
        <TabButton
          tab="models"
          label={t("cluster.tabModels")}
          onSelect={() => {
            activeTab.value = "models";
            void loadModels();
          }}
        />
        <TabButton tab="settings" label={t("cluster.tabSettings")} onSelect={() => openSettingsTab(view.settings)} />
      </div>

      {activeTab.value === "overview" && <OverviewSection view={view} />}
      {activeTab.value === "discovered" && <DiscoveredSection />}
      {activeTab.value === "models" && <ModelsSection view={view} />}
      {activeTab.value === "settings" && <SettingsSection view={view} />}

      <InviteDialog />
      <ConfirmDialog
        open={removeTarget.value !== null}
        title={t("cluster.removeTitle")}
        name={removeTarget.value?.name}
        description={t("cluster.removeDesc")}
        confirmLabel={t("cluster.removeConfirm")}
        busy={removing.value}
        onConfirm={() => void confirmRemove()}
        onCancel={() => {
          if (!removing.value) removeTarget.value = null;
        }}
      />
      <ConfirmDialog
        open={leaveOpen.value}
        title={t("cluster.leaveTitle")}
        name={view.cluster?.name}
        description={t("cluster.leaveDesc")}
        confirmLabel={t("cluster.leaveConfirm")}
        busy={leaving.value}
        onConfirm={() => void confirmLeave()}
        onCancel={() => {
          if (!leaving.value) leaveOpen.value = false;
        }}
      />
    </>
  );
}

function TabButton({ tab, label, badge, onSelect }: { tab: ClusterTab; label: string; badge?: string; onSelect: () => void }) {
  const active = activeTab.value === tab;
  return (
    <button
      type="button"
      onClick={onSelect}
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

// ---- overview --------------------------------------------------------------

function sortMembers(members: ClusterNodeView[]): ClusterNodeView[] {
  return [...members].sort((a, b) => {
    if (a.local !== b.local) return a.local ? -1 : 1;
    if (a.online !== b.online) return a.online ? -1 : 1;
    return a.name.localeCompare(b.name);
  });
}

function OverviewSection({ view }: { view: ClusterView }) {
  const members = sortMembers(view.members);
  return (
    <section class="bg-white rounded-xl border border-gray-200 p-6">
      <div class="flex flex-wrap items-center justify-between gap-3 mb-4">
        <div>
          <h2 class="text-lg font-bold text-gray-900">{view.cluster?.name}</h2>
          <p class="text-xs text-gray-400 font-mono">{view.cluster?.uuid}</p>
        </div>
        <span class="text-sm text-gray-400">{t("dash.updates")}</span>
      </div>
      {overviewError.value && (
        <p class="mb-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{overviewError.value}</p>
      )}
      <div class="overflow-x-auto">
        <table class="w-full min-w-[960px] text-sm">
          <thead>
            <tr class="border-b border-gray-100 text-left text-gray-500">
              <th class="pb-3 pr-3 font-medium">{t("cluster.colNode")}</th>
              <th class="pb-3 pr-3 font-medium">{t("cluster.colStatus")}</th>
              <th class="pb-3 pr-3 font-medium">{t("cluster.colGPU")}</th>
              <th class="pb-3 pr-3 font-medium">{t("cluster.colCPURAM")}</th>
              <th class="pb-3 pr-3 font-medium">{t("cluster.colDisk")}</th>
              <th class="pb-3 pr-3 font-medium">{t("cluster.colModels")}</th>
              <th class="pb-3 pr-3 font-medium text-right">{t("cluster.colInflight")}</th>
              <th class="pb-3 pr-3 font-medium">{t("cluster.colVersion")}</th>
              <th class="pb-3 font-medium text-right">{t("cluster.colActions")}</th>
            </tr>
          </thead>
          <tbody>
            {members.map((m) => (
              <MemberRow key={m.uuid} node={m} view={view} />
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function MemberRow({ node, view }: { node: ClusterNodeView; view: ClusterView }) {
  const st = node.status;
  const tone = healthTone(node.health, node.online);
  const gpus = st?.gpus ?? [];
  const vramTotal = gpus.reduce((s, g) => s + g.vram_total, 0);
  const vramUsed = gpus.reduce((s, g) => s + g.vram_used, 0);
  const vramPct = percentOf(vramUsed, vramTotal);
  const gpuName = gpus[0]?.name || (st ? t("dash.clusterNoGPU") : "—");
  const loaded = (st?.models ?? []).filter((m) => m.loaded);
  const stateName = st?.state ?? (node.local ? view.settings.state : undefined);
  const stateLabel = stateName && stateName !== "active" ? t(stateKey(stateName)) : "";
  const muted = node.online ? "" : "text-gray-400";
  const lastSeen = node.last_seen ? new Date(node.last_seen) : null;
  const addr = node.addr || node.static_address || "";
  const editingStatic = staticEditUUID.value === node.uuid;

  return (
    <tr class={`border-b border-gray-50 align-top ${node.online ? "" : "bg-gray-50/60"}`}>
      <td class="py-3 pr-3">
        {node.local && nameEditing.value ? (
          <div class="flex items-center gap-1.5">
            <input
              class="w-40 rounded-lg border border-gray-200 px-2 py-1 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
              value={nameDraft.value}
              disabled={nameSaving.value}
              onInput={(e) => (nameDraft.value = (e.target as HTMLInputElement).value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") void saveName(node.uuid);
                if (e.key === "Escape") nameEditing.value = false;
              }}
            />
            <button type="button" class="text-xs text-indigo-600 hover:underline" disabled={nameSaving.value} onClick={() => void saveName(node.uuid)}>
              {nameSaving.value ? "..." : t("cluster.save")}
            </button>
            <button type="button" class="text-xs text-gray-400 hover:text-gray-600" onClick={() => (nameEditing.value = false)}>
              {t("confirm.cancel")}
            </button>
          </div>
        ) : (
          <div class="flex items-center gap-2 min-w-0">
            <span class={`font-medium ${node.online ? "text-gray-900" : "text-gray-500"}`}>{node.name}</span>
            {node.local && (
              <>
                <span class="shrink-0 rounded bg-indigo-100 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-indigo-700">
                  {t("cluster.thisNode")}
                </span>
                <button type="button" class="text-gray-400 hover:text-indigo-600" title={t("cluster.rename")} onClick={() => startNameEdit(node.name)}>
                  <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                  </svg>
                </button>
              </>
            )}
          </div>
        )}
        {st?.hostname && <div class="text-xs text-gray-400">{st.hostname}</div>}
        {editingStatic ? (
          <div class="mt-1 flex items-center gap-1.5">
            <input
              class="w-44 rounded-lg border border-gray-200 px-2 py-1 font-mono text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500"
              value={staticDraft.value}
              placeholder={t("cluster.staticAddressPlaceholder")}
              disabled={staticSaving.value}
              onInput={(e) => (staticDraft.value = (e.target as HTMLInputElement).value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") void saveStaticAddress();
                if (e.key === "Escape") staticEditUUID.value = "";
              }}
            />
            <button type="button" class="text-xs text-indigo-600 hover:underline" disabled={staticSaving.value} onClick={() => void saveStaticAddress()}>
              {staticSaving.value ? "..." : t("cluster.save")}
            </button>
            <button type="button" class="text-xs text-gray-400 hover:text-gray-600" onClick={() => (staticEditUUID.value = "")}>
              {t("confirm.cancel")}
            </button>
          </div>
        ) : (
          addr && (
            <div class="mt-0.5 font-mono text-[11px] text-gray-400" title={node.static_address ? t("cluster.staticAddressSet", node.static_address) : addr}>
              {addr}
              {node.static_address && <span class="ml-1 rounded bg-gray-100 px-1 text-[10px] text-gray-500">{t("cluster.staticBadge")}</span>}
            </div>
          )
        )}
      </td>
      <td class="py-3 pr-3">
        <div class="flex items-center gap-1.5 whitespace-nowrap" title={node.last_error || undefined}>
          <span class={`h-2 w-2 rounded-full ${healthDotClass[tone]}`} />
          <span class={node.online ? "text-gray-700" : "text-gray-400"}>{t(healthKey(node.health, node.online))}</span>
          {stateLabel && <span class="rounded bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium text-amber-700">{stateLabel}</span>}
          {node.last_error && (
            <span class="cursor-help text-red-500" title={node.last_error}>
              <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" />
              </svg>
            </span>
          )}
        </div>
        {!node.online && lastSeen && !Number.isNaN(lastSeen.getTime()) && (
          <div class="text-[11px] text-gray-400">{t("cluster.lastSeen", lastSeen.toLocaleString())}</div>
        )}
        {st && !st.licensed && view.node_limit > 0 && (
          <div class="text-[11px] text-gray-400">{t("cluster.communityNode")}</div>
        )}
      </td>
      <td class={`py-3 pr-3 ${muted}`}>
        <div class="truncate max-w-[12rem]" title={gpuName}>
          {gpuName}
          {gpus.length > 1 ? ` ×${gpus.length}` : ""}
        </div>
        {vramTotal > 0 ? (
          <>
            <div class="text-xs text-gray-500">{fmtGBPair(vramUsed, vramTotal)}</div>
            <div class="mt-1 h-1.5 w-32 rounded-full bg-gray-100 overflow-hidden">
              <div class={`h-full rounded-full ${vramPct > 80 ? "bg-red-500" : vramPct > 50 ? "bg-amber-400" : "bg-indigo-500"}`} style={{ width: `${vramPct}%` }} />
            </div>
          </>
        ) : (
          st && <div class="text-xs text-gray-400">{t("dash.na")}</div>
        )}
      </td>
      <td class={`py-3 pr-3 whitespace-nowrap ${muted}`}>
        {st ? (
          <>
            <div>
              {st.cpu.util !== undefined && st.cpu.util !== null
                ? t("dash.clusterCPUWithUtil", st.cpu.cores, Math.round(st.cpu.util))
                : t("dash.clusterCPUCores", st.cpu.cores)}
            </div>
            <div class="text-xs text-gray-500">
              {fmtGBPair(st.ram.used, st.ram.total)}
              {st.ram.unified ? ` · ${t("dash.unifiedMemory")}` : ""}
            </div>
          </>
        ) : (
          "—"
        )}
      </td>
      <td class={`py-3 pr-3 whitespace-nowrap ${muted}`}>
        {st ? t("dash.clusterDiskFree", fmtGB(st.disk.free), fmtGB(st.disk.total)) : "—"}
        {st?.disk.io_busy && <div class="text-[11px] text-amber-600">{t("cluster.diskBusy")}</div>}
        {st && (st.jobs.pulling.length > 0 || st.jobs.converting.length > 0) && (
          <div class="text-[11px] text-indigo-600" title={[...st.jobs.pulling, ...st.jobs.converting].join("\n")}>
            {t("cluster.jobsRunning", st.jobs.pulling.length + st.jobs.converting.length)}
          </div>
        )}
      </td>
      <td class={`py-3 pr-3 ${muted}`}>
        {st ? (
          <span class={loaded.length > 0 ? "cursor-help underline decoration-dotted" : ""} title={loaded.map((m) => m.id).join("\n")}>
            {t("cluster.modelsLoadedOf", loaded.length, st.models.length)}
          </span>
        ) : (
          "—"
        )}
      </td>
      <td class={`py-3 pr-3 text-right ${muted}`}>{st ? st.inflight : "—"}</td>
      <td class={`py-3 pr-3 whitespace-nowrap ${muted}`}>{st?.version || "—"}</td>
      <td class="py-3 text-right whitespace-nowrap">
        {node.local ? (
          <select
            class="rounded-lg border border-gray-200 px-2 py-1 text-xs text-gray-700 focus:outline-none focus:ring-2 focus:ring-indigo-500"
            value={view.settings.state}
            disabled={stateSaving.value}
            onChange={(e) => void changeLocalState(node.uuid, (e.target as HTMLSelectElement).value as ClusterNodeState)}
            title={t("cluster.stateSelectorHint")}
          >
            <option value="active">{t("cluster.stateActive")}</option>
            <option value="drain">{t("cluster.stateDrain")}</option>
            <option value="maintenance">{t("cluster.stateMaintenance")}</option>
          </select>
        ) : (
          <div class="flex justify-end gap-2">
            <button type="button" class="text-xs text-indigo-600 hover:underline" onClick={() => startStaticEdit(node)}>
              {t("cluster.setStaticAddress")}
            </button>
            <button
              type="button"
              onClick={() => (removeTarget.value = node)}
              class="rounded border border-red-300 px-2.5 py-1 text-xs text-red-600 transition-colors hover:bg-red-50"
            >
              {t("cluster.remove")}
            </button>
          </div>
        )}
      </td>
    </tr>
  );
}

// ---- discovered ------------------------------------------------------------

function DiscoveredSection({ readOnly = false }: { readOnly?: boolean }) {
  const list = discovered.value;
  return (
    <section class="bg-white rounded-xl border border-gray-200 p-6">
      <div class="flex flex-wrap items-center justify-between gap-3 mb-4">
        <div>
          <h2 class="text-lg font-bold text-gray-900">{t("cluster.discoveredTitle")}</h2>
          <p class="mt-1 text-sm text-gray-500">{readOnly ? t("cluster.discoveredDescReadOnly") : t("cluster.discoveredDesc")}</p>
        </div>
        <span class="text-sm text-gray-400">{t("cluster.updatesEvery5s")}</span>
      </div>
      {inviteDone.value && (
        <p class="mb-3 rounded-lg bg-green-50 px-3 py-2 text-sm text-green-700">{t("cluster.inviteSuccess", inviteDone.value)}</p>
      )}
      {!discoveredLoaded.value ? (
        <p class="text-sm text-gray-400 py-2">{t("cluster.loading")}</p>
      ) : list.length === 0 ? (
        <p class="text-sm text-gray-400 py-2">{view.value?.active === false ? t("cluster.discoveredDormant") : t("cluster.discoveredEmpty")}</p>
      ) : (
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-gray-100 text-left text-gray-500">
              <th class="pb-3 pr-3 font-medium">{t("cluster.colNode")}</th>
              <th class="pb-3 pr-3 font-medium">{t("cluster.colAddress")}</th>
              <th class="pb-3 pr-3 font-medium">{t("cluster.colVersion")}</th>
              <th class="pb-3 pr-3 font-medium">{t("cluster.colSeen")}</th>
              <th class="pb-3 font-medium text-right">{t("cluster.colActions")}</th>
            </tr>
          </thead>
          <tbody>
            {list.map((n) => {
              const seen = new Date(n.seen);
              return (
                <tr key={n.uuid} class="border-b border-gray-50">
                  <td class="py-3 pr-3">
                    <div class="font-medium text-gray-900">{n.name}</div>
                    <div class="font-mono text-[11px] text-gray-400">{n.uuid}</div>
                  </td>
                  <td class="py-3 pr-3 font-mono text-xs text-gray-600">
                    {n.addr}
                    {n.api_port ? <span class="text-gray-400"> · api {n.api_port}</span> : null}
                  </td>
                  <td class="py-3 pr-3 text-gray-600">{n.version || "—"}</td>
                  <td class="py-3 pr-3 text-gray-600" title={n.source}>
                    {Number.isNaN(seen.getTime()) ? n.seen : seen.toLocaleTimeString()}
                    <span class="ml-1 text-xs text-gray-400">({n.source})</span>
                  </td>
                  <td class="py-3 text-right">
                    {n.cluster_uuid ? (
                      <span class="text-xs text-gray-400" title={n.cluster_uuid}>{t("cluster.inAnotherCluster")}</span>
                    ) : readOnly ? (
                      <span class="text-xs text-gray-400">{t("cluster.notPaired")}</span>
                    ) : (
                      <button
                        type="button"
                        onClick={() => openInvite(n)}
                        class="rounded-lg bg-indigo-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-indigo-700"
                      >
                        {t("cluster.invite")}
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </section>
  );
}

function InviteDialog() {
  const target = inviteTarget.value;
  useEffect(() => {
    if (!target) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") closeInvite();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [target]);
  if (!target) return null;
  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 px-4" onClick={closeInvite}>
      <div class="w-full max-w-md rounded-2xl bg-white shadow-2xl" onClick={(e) => e.stopPropagation()}>
        <div class="border-b border-gray-100 px-6 py-5">
          <h2 class="text-lg font-semibold text-gray-900">{t("cluster.inviteTitle", target.name)}</h2>
          <p class="mt-1 text-sm text-gray-500">{t("cluster.inviteDesc")}</p>
        </div>
        <div class="space-y-4 px-6 py-5">
          <div>
            <label class="mb-1 block text-sm font-medium text-gray-700">{t("cluster.admissionCode")}</label>
            <input
              class="w-full rounded-lg border border-gray-200 px-3 py-2.5 font-mono text-lg tracking-[0.2em] focus:outline-none focus:ring-2 focus:ring-indigo-500"
              value={inviteCode.value}
              inputMode="numeric"
              maxLength={9}
              placeholder="12345678"
              disabled={inviting.value}
              onInput={(e) => (inviteCode.value = (e.target as HTMLInputElement).value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") void handleInvite();
              }}
            />
          </div>
          <div>
            <label class="mb-1 block text-sm font-medium text-gray-700">{t("cluster.inviteAddress")}</label>
            <input
              class="w-full rounded-lg border border-gray-200 px-3 py-2.5 font-mono text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
              value={inviteAddress.value}
              placeholder={target.addr}
              disabled={inviting.value}
              onInput={(e) => (inviteAddress.value = (e.target as HTMLInputElement).value)}
            />
          </div>
          {inviteError.value && <p class="text-sm text-red-600">{inviteError.value}</p>}
        </div>
        <div class="flex justify-end gap-3 border-t border-gray-100 px-6 py-4">
          <button
            type="button"
            onClick={closeInvite}
            disabled={inviting.value}
            class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-60"
          >
            {t("confirm.cancel")}
          </button>
          <button
            type="button"
            onClick={() => void handleInvite()}
            disabled={inviting.value}
            class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
          >
            {inviting.value ? t("cluster.inviting") : t("cluster.invite")}
          </button>
        </div>
      </div>
    </div>
  );
}

// ---- models ----------------------------------------------------------------

function ModelsSection({ view }: { view: ClusterView }) {
  const columns = sortMembers(view.members);
  const list = models.value;
  return (
    <>
      <section class="bg-white rounded-xl border border-gray-200 p-6">
        <div class="flex flex-wrap items-center justify-between gap-3 mb-4">
          <div>
            <h2 class="text-lg font-bold text-gray-900">{t("cluster.modelsTitle")}</h2>
            <p class="mt-1 text-sm text-gray-500">{t("cluster.modelsDesc")}</p>
          </div>
          <div class="flex items-center gap-4 text-xs text-gray-500">
            <span class="flex items-center gap-1.5"><span class="inline-block h-3 w-3 rounded bg-indigo-600" />{t("cluster.cellLoaded")}</span>
            <span class="flex items-center gap-1.5"><span class="inline-block h-3 w-3 rounded border-2 border-indigo-400" />{t("cluster.cellPresent")}</span>
            <span class="flex items-center gap-1.5"><span class="inline-block w-3 text-center text-gray-300">—</span>{t("cluster.cellAbsent")}</span>
            <button type="button" class="text-indigo-600 hover:underline" disabled={modelsLoading.value} onClick={() => void loadModels()}>
              {modelsLoading.value ? t("cluster.refreshing") : t("cluster.refresh")}
            </button>
          </div>
        </div>
        {modelsError.value && <p class="mb-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{modelsError.value}</p>}
        {list.length === 0 ? (
          <p class="text-sm text-gray-400 py-2">{modelsLoading.value ? t("cluster.loading") : t("cluster.modelsEmpty")}</p>
        ) : (
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-gray-100 text-left text-gray-500">
                  <th class="pb-3 pr-3 font-medium">{t("cluster.colModel")}</th>
                  {columns.map((c) => (
                    <th key={c.uuid} class={`pb-3 px-3 text-center font-medium ${c.online ? "" : "text-gray-300"}`} title={c.uuid}>
                      <div class="truncate max-w-[9rem]">{c.name}</div>
                      {c.local && <div class="text-[10px] uppercase tracking-wide text-indigo-500">{t("cluster.thisNode")}</div>}
                    </th>
                  ))}
                  <th class="pb-3 font-medium text-right">{t("cluster.colActions")}</th>
                </tr>
              </thead>
              <tbody>
                {list.map((m) => {
                  const key = `${m.id}::all`;
                  const result = syncResults.value[key];
                  return (
                    <>
                      <tr key={m.id} class="border-b border-gray-50">
                        <td class="py-3 pr-3">
                          <div class="font-medium text-gray-900 break-all">{m.id}</div>
                          <div class="text-xs text-gray-400">
                            {fmtGB(m.size)} GB{m.format ? ` · ${m.format}` : ""}{m.pipeline_tag ? ` · ${m.pipeline_tag}` : ""}
                          </div>
                        </td>
                        {columns.map((c) => {
                          const cell = m.nodes.find((n) => n.uuid === c.uuid);
                          return (
                            <td key={c.uuid} class="py-3 px-3 text-center">
                              {cell?.loaded ? (
                                <span class="inline-block rounded bg-indigo-600 px-2 py-0.5 text-[11px] font-medium text-white">{t("cluster.cellLoaded")}</span>
                              ) : cell ? (
                                <span class="inline-block rounded border-2 border-indigo-400 px-2 py-0.5 text-[11px] font-medium text-indigo-700">{t("cluster.cellPresent")}</span>
                              ) : (
                                <span class="text-gray-300">—</span>
                              )}
                            </td>
                          );
                        })}
                        <td class="py-3 text-right whitespace-nowrap">
                          <button
                            type="button"
                            disabled={syncBusy.value !== ""}
                            onClick={() => void handleSync(m.id, "all", key)}
                            class="rounded-lg border border-indigo-200 bg-indigo-50 px-3 py-1.5 text-xs font-medium text-indigo-700 transition-colors hover:bg-indigo-100 disabled:opacity-60"
                          >
                            {syncBusy.value === key ? t("cluster.syncing") : t("cluster.syncAll")}
                          </button>
                        </td>
                      </tr>
                      {result && (
                        <tr key={`${m.id}-result`} class="border-b border-gray-50 bg-gray-50/60">
                          <td colSpan={columns.length + 2} class="px-3 py-2 text-xs">
                            <SyncResultList result={result} onDismiss={() => {
                              const next = { ...syncResults.value };
                              delete next[key];
                              syncResults.value = next;
                            }} />
                          </td>
                        </tr>
                      )}
                    </>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <div class="grid gap-6 lg:grid-cols-2">
        <RecommendationsCard />
        <ExplainCard models={list} />
      </div>
    </>
  );
}

function SyncResultList({ result, onDismiss }: { result: ClusterSyncResponse | { error: string }; onDismiss: () => void }) {
  return (
    <div class="flex items-start justify-between gap-3">
      {"error" in result ? (
        <span class="text-red-700">{t("cluster.syncError", result.error)}</span>
      ) : (
        <ul class="space-y-0.5">
          {result.results.length === 0 && <li class="text-gray-500">{t("cluster.syncNothing")}</li>}
          {result.results.map((r) => (
            <li key={r.node_uuid} class="flex flex-wrap items-center gap-1.5">
              <span class="font-medium text-gray-800">{r.node_name}:</span>
              {r.error ? (
                <span class="text-red-700">{t("cluster.syncError", r.error)}</span>
              ) : r.skipped ? (
                <span class="text-gray-500">{t("cluster.syncSkipped", r.skipped)}</span>
              ) : (
                <span class="text-green-700">
                  {t("cluster.syncStarted")}
                  {r.job?.id ? <span class="ml-1 font-mono text-[11px] text-gray-400">{r.job.id}</span> : null}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
      <button type="button" class="text-gray-400 hover:text-gray-600" onClick={onDismiss} aria-label={t("dash.close")}>
        ×
      </button>
    </div>
  );
}

function RecommendationsCard() {
  const recs = recommendations.value;
  return (
    <section class="bg-white rounded-xl border border-gray-200 p-6">
      <h2 class="text-lg font-bold text-gray-900">{t("cluster.recommendationsTitle")}</h2>
      <p class="mt-1 text-sm text-gray-500">{t("cluster.recommendationsDesc")}</p>
      {recs.length === 0 ? (
        <p class="mt-4 text-sm text-gray-400">{t("cluster.recommendationsEmpty")}</p>
      ) : (
        <ul class="mt-4 divide-y divide-gray-100">
          {recs.map((rec) => {
            const key = `${rec.model}::${rec.node_uuid}`;
            const outcome = applyResults.value[key];
            return (
              <li key={key} class="flex items-start justify-between gap-3 py-3">
                <div class="min-w-0">
                  <div class="text-sm font-medium text-gray-900 break-all">{rec.model}</div>
                  <div class="text-xs text-gray-500">
                    → <span class="font-medium text-gray-700">{rec.node_name}</span> · {rec.reason}
                  </div>
                  {outcome && <div class="mt-1 text-xs text-indigo-700">{outcome}</div>}
                </div>
                <button
                  type="button"
                  disabled={applyBusy.value !== ""}
                  onClick={() => void applyRecommendation(rec)}
                  class="shrink-0 rounded-lg bg-indigo-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
                >
                  {applyBusy.value === key ? t("cluster.applying") : t("cluster.apply")}
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

function ExplainCard({ models: list }: { models: ClusterModelDistribution[] }) {
  const result = explainResult.value;
  const ordered = result ? [...result.candidates].sort((a, b) => a.rank - b.rank) : [];
  return (
    <section class="bg-white rounded-xl border border-gray-200 p-6">
      <h2 class="text-lg font-bold text-gray-900">{t("cluster.explainTitle")}</h2>
      <p class="mt-1 text-sm text-gray-500">{t("cluster.explainDesc")}</p>
      <div class="mt-4 grid gap-3 sm:grid-cols-[1fr_auto_auto_auto] sm:items-end">
        <div>
          <label class="mb-1 block text-xs font-medium text-gray-600">{t("cluster.colModel")}</label>
          <select
            class="w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            value={explainModel.value}
            onChange={(e) => (explainModel.value = (e.target as HTMLSelectElement).value)}
          >
            {list.length === 0 && <option value="">{t("cluster.modelsEmpty")}</option>}
            {list.map((m) => (
              <option key={m.id} value={m.id}>{m.id}</option>
            ))}
          </select>
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-gray-600">{t("cluster.explainPromptTokens")}</label>
          <input
            type="number"
            min={0}
            class="w-28 rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            value={explainPromptTokens.value}
            onInput={(e) => (explainPromptTokens.value = (e.target as HTMLInputElement).value)}
          />
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-gray-600">{t("cluster.explainMaxTokens")}</label>
          <input
            type="number"
            min={0}
            class="w-28 rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            value={explainMaxTokens.value}
            onInput={(e) => (explainMaxTokens.value = (e.target as HTMLInputElement).value)}
          />
        </div>
        <button
          type="button"
          disabled={explainBusy.value || !explainModel.value}
          onClick={() => void handleExplain()}
          class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
        >
          {explainBusy.value ? t("cluster.explaining") : t("cluster.explainAction")}
        </button>
      </div>
      {explainError.value && <p class="mt-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{explainError.value}</p>}
      {result && (
        <ol class="mt-4 space-y-2">
          {ordered.length === 0 && <li class="text-sm text-gray-400">{t("cluster.explainNoCandidates")}</li>}
          {ordered.map((c) => (
            <li key={c.uuid} class={`flex items-start gap-3 rounded-lg border px-3 py-2 ${c.eligible ? "border-gray-200" : "border-gray-100 bg-gray-50 text-gray-400"}`}>
              <span class={`mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold ${c.eligible ? "bg-indigo-600 text-white" : "bg-gray-200 text-gray-500"}`}>
                {c.eligible ? c.rank : "×"}
              </span>
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2 text-sm">
                  <span class={`font-medium ${c.eligible ? "text-gray-900" : "text-gray-500"}`}>{c.name}</span>
                  {c.local && <span class="rounded bg-indigo-100 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-indigo-700">{t("cluster.thisNode")}</span>}
                  {c.warm && <span class="rounded bg-green-50 px-1.5 py-0.5 text-[10px] font-medium text-green-700">{t("cluster.warm")}</span>}
                  {c.affinity && <span class="rounded bg-sky-50 px-1.5 py-0.5 text-[10px] font-medium text-sky-700">{t("cluster.affinity")}</span>}
                  {c.eligible && <span class="ml-auto text-xs text-gray-500">{t("cluster.estimated", fmtSeconds(c.estimated_seconds))}</span>}
                </div>
                {c.eligible ? (
                  c.factors && c.factors.length > 0 && <div class="mt-0.5 text-xs text-gray-500">{c.factors.join(" · ")}</div>
                ) : (
                  <div class="mt-0.5 text-xs">{t("cluster.excluded", c.excluded || t("cluster.unknownError"))}</div>
                )}
              </div>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

// ---- settings --------------------------------------------------------------

function Toggle({ checked, onChange, disabled }: { checked: boolean; onChange: (next: boolean) => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      class={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors disabled:opacity-60 ${checked ? "bg-indigo-600" : "bg-gray-300"}`}
    >
      <span class={`inline-block h-5 w-5 transform rounded-full bg-white shadow transition-transform ${checked ? "translate-x-5" : "translate-x-0.5"}`} />
    </button>
  );
}

function SettingsSection({ view }: { view: ClusterView }) {
  useEffect(() => {
    // The tab is remembered across navigations; refresh what it shows on mount.
    settingsForm.value = { ...view.settings };
    settingsSaved.value = false;
    void loadToken();
  }, []);
  const form = settingsForm.value ?? view.settings;
  const update = (patch: Partial<ClusterSettings>) => {
    settingsForm.value = { ...form, ...patch };
    settingsSaved.value = false;
  };
  const token = tokenInfo.value;
  return (
    <div class="space-y-6">
      {/* Join token */}
      <section class="bg-white rounded-xl border border-gray-200 p-6">
        <h2 class="text-lg font-bold text-gray-900">{t("cluster.tokenTitle")}</h2>
        <p class="mt-1 text-sm text-gray-500">{t("cluster.tokenDesc")}</p>
        <div class="mt-4">
          {tokenError.value && <p class="mb-3 text-sm text-red-600">{tokenError.value}</p>}
          {token?.available && token.token ? (
            <div class="flex gap-2">
              <input
                readOnly
                class="min-w-0 flex-1 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 font-mono text-sm text-gray-800"
                value={token.token}
                onFocus={(e) => (e.target as HTMLInputElement).select()}
              />
              <CopyButton text={token.token} id="join-token" />
              <button
                type="button"
                disabled={rotating.value}
                onClick={() => void handleRotate()}
                class="shrink-0 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-60"
              >
                {rotating.value ? t("cluster.rotating") : t("cluster.rotate")}
              </button>
            </div>
          ) : (
            <div class="flex flex-wrap items-center gap-3">
              <p class="text-sm text-gray-600">{token ? t("cluster.tokenUnavailable") : t("cluster.loading")}</p>
              <button
                type="button"
                disabled={rotating.value}
                onClick={() => void handleRotate()}
                class="rounded-lg bg-indigo-600 px-3.5 py-1.5 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
              >
                {rotating.value ? t("cluster.rotating") : t("cluster.rotateToGet")}
              </button>
            </div>
          )}
          <p class="mt-2 text-xs text-gray-400">{t("cluster.rotateHint")}</p>
        </div>
      </section>

      {/* Routing & node */}
      <section class="bg-white rounded-xl border border-gray-200 p-6">
        <h2 class="text-lg font-bold text-gray-900">{t("cluster.routingTitle")}</h2>
        <p class="mt-1 text-sm text-gray-500">{t("cluster.routingDesc")}</p>

        <div class="mt-5 space-y-5">
          <div>
            <div class="text-sm font-medium text-gray-700">{t("cluster.routingMode")}</div>
            <div class="mt-2 grid gap-2 sm:grid-cols-2">
              {(["local_first", "balanced"] as ClusterRoutingMode[]).map((mode) => (
                <label
                  key={mode}
                  class={`flex cursor-pointer items-start gap-3 rounded-xl border p-3 transition-colors ${
                    form.routing_mode === mode ? "border-indigo-300 bg-indigo-50" : "border-gray-200 hover:bg-gray-50"
                  }`}
                >
                  <input
                    type="radio"
                    name="routing_mode"
                    class="mt-1"
                    checked={form.routing_mode === mode}
                    onChange={() => update({ routing_mode: mode })}
                  />
                  <span>
                    <span class="block text-sm font-medium text-gray-900">
                      {mode === "local_first" ? t("cluster.routingLocalFirst") : t("cluster.routingBalanced")}
                    </span>
                    <span class="block text-xs text-gray-500">
                      {mode === "local_first" ? t("cluster.routingLocalFirstDesc") : t("cluster.routingBalancedDesc")}
                    </span>
                  </span>
                </label>
              ))}
            </div>
          </div>

          <div class="flex items-start justify-between gap-4 border-t border-gray-100 pt-4">
            <div>
              <div class="text-sm font-medium text-gray-700">{t("cluster.preferLocal")}</div>
              <div class="text-xs text-gray-500">{t("cluster.preferLocalDesc")}</div>
            </div>
            <Toggle checked={form.prefer_local} onChange={(v) => update({ prefer_local: v })} />
          </div>

          <div class="flex items-start justify-between gap-4 border-t border-gray-100 pt-4">
            <div>
              <div class="text-sm font-medium text-gray-700">{t("cluster.acceptWork")}</div>
              <div class="text-xs text-gray-500">{t("cluster.acceptWorkDesc")}</div>
            </div>
            <Toggle checked={form.accept_work} onChange={(v) => update({ accept_work: v })} />
          </div>

          <div class="grid gap-4 border-t border-gray-100 pt-4 sm:grid-cols-3">
            <div>
              <label class="block text-sm font-medium text-gray-700">{t("cluster.weight")}</label>
              <input
                type="number"
                min={10}
                max={200}
                class="mt-1 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={form.weight}
                onInput={(e) => update({ weight: Number((e.target as HTMLInputElement).value) })}
              />
              <p class="mt-1 text-xs text-gray-400">{t("cluster.weightDesc")}</p>
            </div>
            <div>
              <label class="block text-sm font-medium text-gray-700">{t("cluster.affinityMaxQueue")}</label>
              <input
                type="number"
                min={0}
                class="mt-1 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={form.affinity_max_queue}
                onInput={(e) => update({ affinity_max_queue: Number((e.target as HTMLInputElement).value) })}
              />
              <p class="mt-1 text-xs text-gray-400">{t("cluster.affinityMaxQueueDesc")}</p>
            </div>
            <div>
              <label class="block text-sm font-medium text-gray-700">{t("cluster.diskReserve")}</label>
              <input
                type="number"
                min={0}
                step={1}
                class="mt-1 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={form.disk_reserve_gb}
                onInput={(e) => update({ disk_reserve_gb: Number((e.target as HTMLInputElement).value) })}
              />
              <p class="mt-1 text-xs text-gray-400">{t("cluster.diskReserveDesc")}</p>
            </div>
          </div>

          <div class="flex flex-wrap items-center gap-3 border-t border-gray-100 pt-4">
            <button
              type="button"
              disabled={settingsSaving.value}
              onClick={() => void saveSettings()}
              class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
            >
              {settingsSaving.value ? t("cluster.saving") : t("cluster.saveSettings")}
            </button>
            {settingsSaved.value && <span class="text-sm text-green-700">{t("cluster.settingsSaved")}</span>}
            {settingsError.value && <span class="text-sm text-red-600">{settingsError.value}</span>}
          </div>
        </div>
      </section>

      {/* Leave */}
      <section class="rounded-xl border border-red-200 bg-white p-6">
        <div class="flex flex-wrap items-center justify-between gap-4">
          <div>
            <h2 class="text-lg font-bold text-red-700">{t("cluster.leaveTitle")}</h2>
            <p class="mt-1 text-sm text-gray-500">{t("cluster.leaveDesc")}</p>
          </div>
          <button
            type="button"
            onClick={() => (leaveOpen.value = true)}
            class="rounded-lg border border-red-300 bg-white px-4 py-2 text-sm font-medium text-red-600 transition-colors hover:bg-red-50"
          >
            {t("cluster.leaveAction")}
          </button>
        </div>
      </section>
    </div>
  );
}
