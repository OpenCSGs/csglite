import { useEffect, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { signal } from "@preact/signals";
import {
  createCluster,
  explainClusterScheduling,
  getCluster,
  getClusterCode,
  getClusterModels,
  getClusterDeployments,
  getProviderPools,
  getClusterRecommendations,
  getClusterToken,
  getDiscoveredClusterNodes,
  inviteClusterNode,
  isClusterDisabledError,
  isFeatureNotLicensedError,
  joinCluster,
  leaveCluster,
  rotateClusterToken,
  setClusterNodeState,
  syncClusterModel,
  updateClusterNode,
  updateClusterSettings, enableCluster } from "../api/client";
import type {
  ClusterCodeResponse,
  ClusterDeployment,
  ClusterExplainResponse,
  ClusterModelDistribution,
  ClusterModelSync,
  ClusterNodeState,
  ClusterNodeView,
  ClusterRecommendation,
  ClusterRoutingMode,
  ClusterSettings,
  ClusterTokenResponse,
  ClusterView,
  DiscoveredClusterNode,
  ProviderPool,
} from "../api/client";
import { clusterMode, navigationMode } from "../clusterAccess";
import { filterModelInstances, isManagedModelInstance, mergeClusterDeployments, modelInstanceCategory, modelInstanceRows, modelRowState, modelNodeState, measuredRate, modelPublication } from "../clusterModels";
import { ClusterModelActions } from "../components/ClusterModelActions";
import { DeploymentMessage } from "../components/DeploymentMessage";
import { InfoTooltip } from "../components/InfoTooltip";
import { ClusterWorkerHeader } from "../components/ClusterWorkerHeader";
import { ClusterWorkerOverview } from "../components/ClusterWorkerOverview";
import { ClusterDeploymentControls } from "../components/ClusterDeployments";
import { nodeMemory, clusterAcceleratorMemory, formatMemory, formatMemoryPair } from "../clusterMemory";
import { deploymentProgress } from "../deploymentModels";
import type { ModelInstanceFilter } from "../clusterModels";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { t, locale } from "../i18n";
import { clusterReasonText, canManageNodeState, workerNodeState, clusterNodeRole, fmtGB, fmtGBPairShort, fmtGBShort, fmtSeconds, healthDotClass, healthKey, healthTone, isNoiseHostname, isValidAdmissionCode, nodeLimitLabel, normalizeAdmissionCode, percentOf, secondsUntil, stateKey } from "../cluster";

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
const stateSaving = signal(false);
const overviewError = signal("");

// ---- models ----------------------------------------------------------------
const models = signal<ClusterModelDistribution[]>([]);
const modelDeployments = signal<ClusterDeployment[]>([]);
const modelPublications = signal<ProviderPool[] | null>(null);
const publicationError = signal("");
const modelsLoading = signal(false);
const modelsError = signal("");
let modelsRefreshPending: Promise<void> | undefined;
const syncBusy = signal("");
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
    if (next.mode === "worker" && activeTab.value !== "overview" && activeTab.value !== "settings") activeTab.value = "overview";
    if (view.value?.node.uuid !== next.node.uuid || view.value?.cluster?.uuid !== next.cluster?.uuid || view.value?.mode !== next.mode) {
      models.value = []; modelDeployments.value = []; recommendations.value = []; modelsError.value = "";
      modelPublications.value = null; publicationError.value = "";
    }
    view.value = next;
    clusterMode.value = navigationMode(next);
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
  if (featureDisabled.value || !view.value || (view.value.mode && view.value.mode !== "lan")) return;
  try {
    discovered.value = await getDiscoveredClusterNodes();
  } catch {
    /* transient; keep the last list */
  } finally {
    discoveredLoaded.value = true;
  }
}

async function loadAdmissionCode() {
  if (featureDisabled.value || !view.value || (view.value.mode && view.value.mode !== "lan")) return;
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
    await refreshModels();
  } finally {
    modelsLoading.value = false;
  }
}

async function refreshModels() {
  const current = view.value;
  if (!current || current.mode === "worker") return;
  if (modelsRefreshPending) return modelsRefreshPending;
  const sameCluster = () => view.value?.node.uuid === current.node.uuid
    && view.value?.cluster?.uuid === current.cluster?.uuid && view.value?.mode === current.mode;
  modelsRefreshPending = (async () => {
    const publications = getProviderPools().then(pools => {
      if (sameCluster()) { modelPublications.value = pools; publicationError.value = ""; }
    }).catch(err => { if (sameCluster()) publicationError.value = errorMessage(err); });
    try {
      const [list, tasks, recs] = await Promise.all([
        getClusterModels(), getClusterDeployments(),
        (!current.mode || current.mode === "lan") ? getClusterRecommendations().catch(() => []) : Promise.resolve([]),
      ]);
      if (!sameCluster()) return;
      models.value = list;
      modelDeployments.value = mergeClusterDeployments(tasks.deployments, tasks.assignments);
      recommendations.value = recs;
      modelsError.value = "";
      if (!explainModel.value && list.length > 0) explainModel.value = list[0].id;
    } catch (err) {
      if (sameCluster()) modelsError.value = errorMessage(err);
    }
    await publications;
  })();
  try { await modelsRefreshPending; }
  finally { modelsRefreshPending = undefined; }
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

async function changeNodeState(uuid: string, state: ClusterNodeState) {
  stateSaving.value = true;
  overviewError.value = "";
  try {
    const settings = view.value?.mode === "worker"
      ? await updateClusterSettings({ state, accept_work: true })
      : await setClusterNodeState(uuid, state);
    if (view.value && uuid === view.value.node.uuid) view.value = { ...view.value, settings };
    await loadView();
  } catch (err) {
    overviewError.value = errorMessage(err);
  } finally {
    stateSaving.value = false;
  }
}

async function handleSync(model: string, nodes: string[] | "all", resultKey: string) {
  syncBusy.value = resultKey;
  modelsError.value = "";
  try {
    await syncClusterModel(model, nodes);
    void refreshModels();
  } catch (err) {
    modelsError.value = errorMessage(err);
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
  if (view.value?.mode !== "worker") void loadToken();
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
	  shared_models: form.shared_models?.map((id) => id.trim()).filter(Boolean),
      managed_by: view.value?.mode === "worker" ? undefined : form.managed_by,
      accept_work: form.accept_work,
      state: view.value?.mode === "worker" ? form.state : undefined,
      prefer_local: view.value?.mode === "worker" ? undefined : form.prefer_local,
      routing_mode: view.value?.mode === "worker" ? undefined : form.routing_mode,
      affinity_max_queue: view.value?.mode === "worker" ? undefined : Math.max(0, Math.round(Number(form.affinity_max_queue) || 0)),
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
	const publicMode = current?.mode === "gateway" || current?.mode === "worker";

  return (
    <div class="page-shell min-w-0 space-y-6 px-4 md:px-8">
      {current?.mode === "worker" && current.cluster ? <ClusterWorkerHeader view={current}
        settings={activeTab.value === "settings"}
        onSettings={() => openSettingsTab(current.settings)}
        onOverview={() => (activeTab.value = "overview")} /> : <div class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="text-xs font-semibold uppercase tracking-[0.28em] text-indigo-500">{t(publicMode ? "cluster.publicEyebrow" : "cluster.eyebrow")}</p>
          <h1 class="mt-2 text-3xl font-bold tracking-tight text-gray-950">{t(current?.mode === "worker" ? "cluster.workerConnectTitle" : "cluster.title")}</h1>
          <p class="mt-2 max-w-2xl text-sm leading-6 text-gray-600">{t(current?.mode === "worker" ? "cluster.workerConnectSubtitle" : publicMode ? "cluster.publicSubtitle" : "cluster.subtitle")}</p>
        </div>
        {current?.cluster && current.mode !== "worker" && <HeaderStats view={current} />}
      </div>}

      {current && viewError.value && <p role="alert" class="rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900">{t("cluster.staleView")} {viewError.value}</p>}

      {current?.mode === "gateway" && clusterNodeRole(current.node.uuid, current.cluster) === "master" && (
        <section class="rounded-xl border border-indigo-200 bg-indigo-50 p-4 text-sm text-indigo-900">
          <p class="font-semibold">{t("cluster.publicGateway")}</p>
          <p class="mt-1">{t("cluster.publicGatewayDesc")}</p>
          <p class="mt-2 font-mono">{current.public_listen_addr}</p>
        </section>
      )}

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
  const memory = clusterAcceleratorMemory(view.members);
  const inflight = view.members.filter(m => m.online).reduce((sum, m) => sum + (m.status?.inflight ?? 0), 0);
  const nodesValue = view.node_limit > 0 ? `${view.members.length} / ${view.node_limit}` : String(view.members.length);
  const nodesHint = view.node_limit > 0 ? (limit.atLimit ? t("cluster.capHintShort") : "") : t("cluster.nodesUnlimitedHint");
  return (
    <div class="grid w-full min-w-0 max-w-full grid-cols-2 gap-3 sm:w-auto sm:grid-cols-3 xl:grid-cols-5" data-cluster-summary>
      <Stat label={t("cluster.statOnline")} value={`${online} / ${view.members.length}`} />
      <Stat label={t("cluster.statNodes")} value={nodesValue} hint={nodesHint} tone={limit.atLimit ? "warn" : "normal"} />
      <Stat label={t("cluster.statVRAM")} value={memory.total > 0 ? formatMemoryPair(memory.free, memory.total) : "—"} hint={t("cluster.memorySummaryHint")} />
      <Stat label={t("cluster.statInflight")} value={String(inflight)} />
      <Stat label={t("cluster.statRole")} value={t(`cluster.role.${clusterNodeRole(view.node.uuid, view.cluster)}`)} />
    </div>
  );
}

function Stat({ label, value, hint, tone = "normal" }: { label: string; value: string; hint?: string; tone?: "normal" | "warn" }) {
  return (
    <div class={`min-w-0 break-words rounded-xl border px-4 py-3 ${tone === "warn" ? "border-amber-200 bg-amber-50" : "border-gray-200 bg-white"}`}>
      <div class="text-[11px] font-medium uppercase tracking-wide text-gray-400">{label}</div>
      <div class={`mt-0.5 text-lg font-semibold leading-tight ${tone === "warn" ? "text-amber-800" : "text-gray-900"}`}>{value}</div>
      {hint && <div class={`mt-0.5 text-[11px] ${tone === "warn" ? "text-amber-700" : "text-gray-400"}`}>{hint}</div>}
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
    <div class="flex flex-col items-start justify-between gap-4 border-b border-gray-200 bg-gray-50/70 p-5 text-sm text-gray-700 sm:flex-row sm:items-center sm:p-6" data-cluster-network>
      <div>
        <div class="font-semibold text-gray-900">{t("cluster.dormantTitle")}</div>
        <p class="mt-1 text-xs leading-5 text-gray-500">{t(current.mode === "worker" ? "cluster.workerDormantDesc" : "cluster.dormantDesc")}</p>
      </div>
      <button
        type="button"
        disabled={enablingCluster.value}
        onClick={enable}
        class="shrink-0 rounded-lg border border-gray-200 bg-white px-4 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-100 disabled:opacity-50"
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
    <div class={`space-y-6 ${view.mode === "worker" || view.mode === "gateway" ? "max-w-3xl" : ""}`} data-cluster-connect>
      <CapBanner />
      <AutoFormNotice view={view} />
      <section class="overflow-hidden rounded-xl border border-gray-200 bg-white" data-cluster-setup>
      <DormantNotice current={view} />
      <div class={`grid ${!view.mode || view.mode === "lan" ? "divide-y divide-gray-200 lg:grid-cols-2 lg:divide-x lg:divide-y-0" : ""}`} data-cluster-forms>
        {/* Create */}
        {view.mode !== "worker" && <section class="min-w-0 p-5 sm:p-6">
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
        </section>}

        {/* Join */}
        {view.mode !== "gateway" && <section class="min-w-0 p-5 sm:p-6">
          <h2 class="text-lg font-bold text-gray-900">{t("cluster.joinTitle")}</h2>
          <p class="mt-1 text-sm text-gray-500">{t(view.mode === "worker" ? "cluster.workerJoinDesc" : "cluster.joinDesc")}</p>
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
              <label class="mb-1 block text-sm font-medium text-gray-700">{t(view.mode === "worker" ? "cluster.masterAddress" : "cluster.joinAddress")}</label>
              <input
                class="w-full rounded-lg border border-gray-200 px-3 py-2.5 font-mono text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={joinAddress.value}
                placeholder={view.mode === "worker" ? view.gateway_url : t("cluster.joinAddressPlaceholder")}
                disabled={joining.value}
                spellcheck={false}
                onInput={(e) => (joinAddress.value = (e.target as HTMLInputElement).value)}
              />
              <p class="mt-1 text-xs text-gray-400">{t(view.mode === "worker" ? "cluster.masterAddressHint" : "cluster.joinAddressHint")}</p>
            </div>
            {joinError.value && <p class="text-sm text-red-600">{joinError.value}</p>}
            <button
              type="button"
              onClick={() => void handleJoin()}
              disabled={joining.value}
              class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
            >
              {joining.value ? t("cluster.joining") : t("cluster.joinAction")}
            </button>
          </div>
        </section>}
      </div>
      </section>

      {(!view.mode || view.mode === "lan") && <>
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
      </>}
    </div>
  );
}

// ---- in a cluster ----------------------------------------------------------

function InCluster({ view }: { view: ClusterView }) {
  return (
    <div class="space-y-6">
      {view.model_source_mixed && (!view.mode || view.mode === "lan") && (
        <div class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
          <span class="font-semibold">{t("cluster.mixedSourceTitle")}</span> {t("cluster.mixedSourceDesc")}
        </div>
      )}
      {view.mode !== "worker" && <CapBanner />}
      {view.mode !== "worker" && <div class="inline-flex max-w-full flex-wrap rounded-2xl border border-gray-200 bg-white p-1 shadow-sm">
        <TabButton tab="overview" label={t("cluster.tabOverview")} onSelect={() => (activeTab.value = "overview")} />
        {(!view.mode || view.mode === "lan") && <TabButton
          tab="discovered"
          label={t("cluster.tabDiscovered")}
          badge={view.discovered_count > 0 ? String(view.discovered_count) : ""}
          onSelect={() => (activeTab.value = "discovered")}
        />}
        <TabButton
          tab="models"
          label={t("cluster.modelManagement")}
          onSelect={() => {
            activeTab.value = "models";
            void loadModels();
          }}
        />
        <TabButton tab="settings" label={t("cluster.tabSettings")} onSelect={() => openSettingsTab(view.settings)} />
      </div>}

      {activeTab.value === "overview" && (view.mode === "worker" ? <ClusterWorkerOverview view={view} /> : <OverviewSection view={view} />)}
      {activeTab.value === "discovered" && (!view.mode || view.mode === "lan") && <DiscoveredSection />}
      {activeTab.value === "models" && view.mode !== "worker" && <ModelsSection view={view} />}
      {activeTab.value === "settings" && <SettingsSection view={view} />}

      <InviteDialog />
      <ConfirmDialog
        open={leaveOpen.value}
        title={t(view.mode === "gateway" ? "cluster.masterLeaveTitle" : "cluster.leaveTitle")}
        name={view.cluster?.name}
        description={t(view.mode === "worker" ? "cluster.workerLeaveDesc" : view.mode === "gateway" ? "cluster.masterLeaveDesc" : "cluster.leaveDesc")}
        confirmLabel={t(view.mode === "gateway" ? "cluster.masterLeaveTitle" : "cluster.leaveConfirm")}
        busy={leaving.value}
        onConfirm={() => void confirmLeave()}
        onCancel={() => {
          if (!leaving.value) leaveOpen.value = false;
        }}
      />
    </div>
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
    <section class="bg-white rounded-xl border border-gray-200 p-4 sm:p-6">
      <div class="flex flex-wrap items-center justify-between gap-3 mb-4">
        <div>
          <h2 class="text-lg font-bold text-gray-900">{view.cluster?.name}</h2>
        </div>
        <span class="text-sm text-gray-400">{t("dash.updates")}</span>
      </div>
      {overviewError.value && (
        <p class="mb-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{overviewError.value}</p>
      )}
      <div class="cluster-members" data-cluster-overview>
        <table class="cluster-members-table w-full table-fixed text-sm">
          <colgroup>
            <col class="w-[29%]" />
            <col class="w-[15%]" />
            <col class="w-[30%]" />
            <col class="w-[11%]" />
            <col class="w-[15%]" />
          </colgroup>
          <thead>
            <tr class="border-b border-gray-100 text-left text-gray-500">
              <th class="pb-3 font-medium">{t("cluster.colNode")}</th>
              <th class="pb-3 font-medium">{t("cluster.colStatus")}</th>
              <th class="pb-3 font-medium">{t("cluster.colGPU")}</th>
              <th class="pb-3 font-medium">{t("cluster.modelsShort")}</th>
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
  const memory = nodeMemory(node);
  const vramPct = memory.free === null ? 0 : percentOf(memory.total - memory.free, memory.total);
  const gpuNames = [...new Set(gpus.map(gpu => gpu.name))].map(name => {
    const count = gpus.filter(gpu => gpu.name === name).length;
    return count > 1 ? `${name} ×${count}` : name;
  });
  const utilization = gpus.length && gpus.every(gpu => gpu.util != null)
    ? Math.round(gpus.reduce((sum, gpu) => sum + gpu.util!, 0) / gpus.length) : null;
  const loaded = (st?.models ?? []).filter((m) => m.loaded);
  const stateName = st?.state ?? (node.local ? view.settings.state : undefined);
  const stateLabel = stateName && stateName !== "active" ? t(stateKey(stateName)) : "";
  const muted = node.online ? "" : "text-gray-400";
  const lastSeen = node.last_seen ? new Date(node.last_seen) : null;
  const editingStatic = staticEditUUID.value === node.uuid;

  return (
    <tr class={`border-b border-gray-100 align-middle last:border-0 ${node.online ? "" : "bg-gray-50/60"}`}>
      <td data-label={t("cluster.colNode")} class="py-4">
        {node.local && nameEditing.value ? <div class="flex min-w-0 flex-wrap items-center gap-1.5">
          <input aria-label={t("cluster.workerNodeName")} class="min-w-0 flex-1 rounded-lg border border-gray-200 px-2 py-1 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            value={nameDraft.value} disabled={nameSaving.value} onInput={e => (nameDraft.value = e.currentTarget.value)}
            onKeyDown={e => { if (e.key === "Enter") void saveName(node.uuid); if (e.key === "Escape") nameEditing.value = false; }} />
          <button type="button" class="text-xs text-indigo-600" disabled={nameSaving.value} onClick={() => void saveName(node.uuid)}>{t("cluster.save")}</button>
          <button type="button" class="text-xs text-gray-400" onClick={() => (nameEditing.value = false)}>{t("confirm.cancel")}</button>
        </div> : <InfoTooltip label={t("cluster.nodeDetailsFor", node.name)} class={`min-w-0 font-medium ${node.online ? "text-gray-900 hover:text-indigo-600" : "text-gray-500"}`} content={<NodeMetadataDetails node={node} />}>
          <span class="flex min-w-0 items-center gap-1.5"><span class="truncate">{node.name}</span><svg aria-hidden="true" class="h-3.5 w-3.5 shrink-0 text-gray-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="12" cy="12" r="9" /><path d="M12 11v6M12 7v1" /></svg></span>
        </InfoTooltip>}
        <div class="mt-1.5 flex flex-wrap items-center gap-2 text-xs">
          <span class={clusterNodeRole(node.uuid, view.cluster) === "master" ? "font-medium text-indigo-600" : "text-gray-500"}>{t(`cluster.role.${clusterNodeRole(node.uuid, view.cluster)}`)}</span>
          {node.local && <>
            <span class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-500">{t("cluster.thisNode")}</span>
            <button type="button" class="rounded text-gray-400 hover:text-indigo-600" aria-label={t("cluster.rename")} title={t("cluster.rename")} onClick={() => startNameEdit(node.name)}>
              <svg aria-hidden="true" class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" /></svg>
            </button>
          </>}
        </div>
        {editingStatic && <div class="mt-2 flex min-w-0 flex-wrap items-center gap-1.5">
          <input aria-label={t("cluster.colAddress")} class="min-w-0 flex-1 rounded-lg border border-gray-200 px-2 py-1 font-mono text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500"
            value={staticDraft.value} placeholder={t("cluster.staticAddressPlaceholder")} disabled={staticSaving.value} onInput={e => (staticDraft.value = e.currentTarget.value)}
            onKeyDown={e => { if (e.key === "Enter") void saveStaticAddress(); if (e.key === "Escape") staticEditUUID.value = ""; }} />
          <button type="button" class="text-xs text-indigo-600" disabled={staticSaving.value} onClick={() => void saveStaticAddress()}>{t("cluster.save")}</button>
          <button type="button" class="text-xs text-gray-400" onClick={() => (staticEditUUID.value = "")}>{t("confirm.cancel")}</button>
        </div>}
      </td>
      <td data-label={t("cluster.colStatus")} class="py-4">
        <InfoTooltip label={t("cluster.nodeStatusFor", node.name)} content={<>
          <p class="font-semibold text-gray-900">{t(healthKey(node.health, node.online))}</p>
          {lastSeen && !Number.isNaN(lastSeen.getTime()) && <p class="mt-2">{t("cluster.lastSeen", lastSeen.toLocaleString())}</p>}
          {node.last_error && <p class="mt-2 whitespace-pre-wrap break-words text-red-600">{node.last_error}</p>}
          {st && !st.licensed && <p class="mt-2 text-amber-700">{t("cluster.nodeQuotaExceeded")}</p>}
        </>}>
          <span class="inline-flex items-center gap-1.5 whitespace-nowrap"><span class={`h-1.5 w-1.5 shrink-0 rounded-full ${healthDotClass[tone]}`} /><span class={node.online ? "text-gray-700" : "text-gray-400"}>{t(healthKey(node.health, node.online))}</span>{node.last_error && <span class="text-red-500" aria-hidden="true">!</span>}</span>
        </InfoTooltip>
        {stateLabel && <div class="mt-1 text-xs text-amber-700">{stateLabel}</div>}
        {st && !st.licensed && <div class="mt-1 text-[11px] text-amber-700">{t("cluster.nodeQuotaExceeded")}</div>}
        {st && st.inflight > 0 && <div class="mt-1 text-[11px] text-indigo-600">{t("cluster.inflightCount", st.inflight)}</div>}
      </td>
      <td data-label={t("cluster.colGPU")} class={`py-4 ${muted}`}>
        {gpus.length > 0 ? <>
          <InfoTooltip label={t("cluster.gpuDetailsFor", node.name)} class="w-full" content={<>
            <p class="mb-2 font-semibold text-gray-900">{t("cluster.colGPU")}</p>
            <div class="space-y-3">{gpus.map(gpu => <div key={gpu.index}>
              <p class="break-words font-medium">#{gpu.index} · {gpu.name}</p>
              <p>{t("cluster.memoryFreeShort", formatMemoryPair(memory.kind === "unified" ? memory.free : gpu.usage_known ? Math.max(0, gpu.vram_total - gpu.vram_used) : null, memory.kind === "unified" ? memory.total : gpu.vram_total))}</p>
              {gpu.util != null && <p>{t("cluster.gpuUtil", Math.round(gpu.util))}</p>}
              {gpu.temperature != null && <p>{t("cluster.gpuTemperature", gpu.temperature)}</p>}
              {gpu.power_draw != null && <p>{t("cluster.gpuPower", gpu.power_draw)}</p>}
              {gpu.throttled && <p class="text-amber-700">{t("cluster.gpuThrottled")}</p>}
            </div>)}</div>
            {memory.kind === "unified" && <p class="mt-3 text-gray-500">{t("cluster.unifiedMemoryHint")}</p>}
          </>}>
            <span class="block break-words font-medium" data-node-gpu-name>{gpuNames.join(" · ")}</span>
          </InfoTooltip>
          <div class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-gray-500" data-node-gpu-memory>
            <span>{t("cluster.memoryFreeShort", formatMemoryPair(memory.free, memory.total))}</span>
            {utilization !== null && <span>{t("cluster.gpuUtil", utilization)}</span>}
            {memory.kind === "unified" && <span>{t("deployments.memory.unified")}</span>}
          </div>
          {memory.free !== null && memory.total > 0 && <div class="mt-2 h-1 w-full max-w-48 overflow-hidden rounded-full bg-gray-100" role="meter" aria-label={t("cluster.memoryUsedPercent")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={vramPct}>
            <div class={`h-full rounded-full ${!node.online ? "bg-gray-300" : vramPct > 80 ? "bg-red-400" : vramPct > 50 ? "bg-amber-400" : "bg-indigo-400"}`} style={{ width: `${vramPct}%` }} />
          </div>}
          {!node.online && <p class="mt-1 text-[11px] text-gray-400">{t("cluster.lastReportedResources")}</p>}
        </> : <span class="text-gray-400">{st ? t("dash.clusterNoGPU") : "—"}</span>}
      </td>
      <td data-label={t("cluster.colModels")} class={`py-4 ${muted}`}>
        {st ? <InfoTooltip label={t("cluster.nodeModelsFor", node.name)} class="text-gray-700 underline decoration-gray-300 decoration-dotted underline-offset-4 hover:text-indigo-600" content={<>
          <p class="font-semibold text-gray-900">{t("cluster.modelsSummary", loaded.length, st.models.length)}</p>
          {loaded.length ? <ul class="mt-2 space-y-2">{loaded.map(model => <li key={model.id} class="break-words"><p>{model.repo || model.id}</p>{model.repo && <p class="text-[11px] text-gray-400">{model.id}</p>}</li>)}</ul> : <p class="mt-2 text-gray-500">{t("cluster.noLoadedModels")}</p>}
        </>}><span class="font-medium tabular-nums">{loaded.length}</span><span class="ml-1 text-xs text-gray-400">/ {st.models.length}</span></InfoTooltip> : "—"}
      </td>
      <td data-label={t("cluster.colActions")} class="py-3 text-right">
        {canManageNodeState(view, node) ? (
          <select
            class="max-w-full rounded-lg border border-gray-200 px-2 py-1 text-xs text-gray-700 focus:outline-none focus:ring-2 focus:ring-indigo-500"
            value={node.local ? (view.mode === "worker" ? workerNodeState(view.settings) : view.settings.state) : st?.state ?? "active"}
            disabled={stateSaving.value || !node.online}
            onChange={(e) => void changeNodeState(node.uuid, (e.target as HTMLSelectElement).value as ClusterNodeState)}
            aria-label={t("cluster.nodeStateFor", node.name)}
            title={t(view.mode === "worker" ? "cluster.workerStateOverviewHint" : "cluster.stateSelectorHint")}
          >
            <option value="active">{t("cluster.stateActive")}</option>
            <option value="drain">{t("cluster.stateDrain")}</option>
            <option value="maintenance">{t("cluster.stateMaintenance")}</option>
          </select>
        ) : view.mode && view.mode !== "lan" ? (
          <span class="text-xs text-gray-500">{t("cluster.publicTunnel")}</span>
        ) : (
          <button type="button" class="text-xs text-indigo-600 hover:underline" onClick={() => startStaticEdit(node)}>
            {t("cluster.setStaticAddress")}
          </button>
        )}
      </td>
    </tr>
  );
}

function NodeMetadataDetails({ node }: { node: ClusterNodeView }) {
  const st = node.status, ram = nodeMemory(node, true);
  return <>
    <p class="mb-3 break-words font-semibold text-gray-900">{node.name}</p>
    <dl class="space-y-2">
      <NodeDetail label={t("cluster.nodeID")} value={node.uuid} />
      {st?.hostname && !isNoiseHostname(st.hostname, node.name) && <NodeDetail label={t("cluster.hostname")} value={st.hostname} />}
      {(node.addr || node.static_address) && <NodeDetail label={t("cluster.colAddress")} value={node.addr || node.static_address || ""} />}
      {node.static_address && <NodeDetail label={t("cluster.staticBadge")} value={node.static_address} />}
      <NodeDetail label={t("cluster.colVersion")} value={st?.version || "—"} />
      {st && <>
        <NodeDetail label={t("cluster.system")} value={[st.os, st.arch].filter(Boolean).join(" · ")} />
        <NodeDetail label={t("cluster.cpuLabel")} value={st.cpu.util != null ? t("dash.clusterCPUWithUtil", st.cpu.cores, Math.round(st.cpu.util)) : t("dash.clusterCPUCores", st.cpu.cores)} />
        <NodeDetail label={t("cluster.ramLabel")} value={formatMemoryPair(Math.min(st.ram.used, st.ram.total), st.ram.total)} />
        <NodeDetail label={t("cluster.ramFree")} value={ram.free === null ? t("deployments.memoryFreeUnknown") : formatMemory(ram.free)} />
        <NodeDetail label={t("cluster.colDisk")} value={fmtGBPairShort(st.disk.total - st.disk.free, st.disk.total)} />
        <NodeDetail label={t("cluster.diskAvailable")} value={`${fmtGBShort(st.disk.free)} GB`} />
        {st.disk.io_busy && <NodeDetail label={t("cluster.colStatus")} value={t("cluster.diskBusy")} />}
        {(st.jobs.pulling.length > 0 || st.jobs.converting.length > 0) && <NodeDetail label={t("cluster.backgroundJobs")} value={[...st.jobs.pulling, ...st.jobs.converting].join("\n")} />}
      </>}
    </dl>
  </>;
}

function NodeDetail({ label, value }: { label: string; value: string }) {
  return <div class="grid grid-cols-[5rem_minmax(0,1fr)] gap-2"><dt class="text-gray-400">{label}</dt><dd class="whitespace-pre-wrap break-words">{value}</dd></div>;
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
                      <span class="text-xs text-gray-400" title={t("cluster.inAnotherCluster")}>{t("cluster.inAnotherCluster")}</span>
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
  const [query, setQuery] = useState("");
  const [machine, setMachine] = useState("");
  const [stateFilter, setStateFilter] = useState<ModelInstanceFilter>("all");
  const canSync = !view.mode || view.mode === "lan";
  const list = models.value;
  const allRows = modelInstanceRows(view.members, list, modelDeployments.value);
  const rows = filterModelInstances(allRows, query, machine, stateFilter);
  const machines = new Map(view.members.map(node => [node.uuid, node]));
  for (const row of allRows) if (!machines.has(row.node.uuid)) machines.set(row.node.uuid, row.node);
  useEffect(() => {
    const timer = window.setInterval(() => void refreshModels(), 2000);
    return () => window.clearInterval(timer);
  }, []);
  const changed = async () => { await loadView(); await refreshModels(); };
  const secondaryButton = "shrink-0 rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-600 hover:bg-gray-50 disabled:opacity-40";
  const publications = publicationError.value ? null : modelPublications.value;
  const stale = !!modelsError.value || !!viewError.value;
  return <ClusterDeploymentControls view={view} deployments={modelDeployments.value} publications={publications} stale={stale} onChanged={changed}>
    {controls => <>
      <section class="overflow-hidden rounded-xl border border-gray-200 bg-white" data-cluster-model-management>
        <div class="flex flex-wrap items-start justify-between gap-4 border-b border-gray-100 p-5">
          <div><h2 class="text-lg font-semibold text-gray-900">{t("cluster.modelManagement")}</h2><p class="mt-1 max-w-2xl text-sm leading-6 text-gray-500">{t("cluster.modelManagementHint")}</p></div>
          <div class="flex shrink-0 gap-2">
            <button type="button" class={secondaryButton} disabled={modelsLoading.value} onClick={() => void loadModels()}>{t(modelsLoading.value ? "cluster.refreshing" : "cluster.refresh")}</button>
            <button type="button" class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-40" disabled={stale || controls.busy || !controls.hasTargets} onClick={controls.create}>{t("deployments.create")}</button>
          </div>
        </div>
        {!controls.hasTargets && <p class="px-5 pt-4 text-sm text-amber-700">{t("deployments.noTargets")}</p>}
        {modelsError.value && <p role="alert" class="mx-5 mt-4 rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-800">{t("cluster.modelRefreshFailed")} {modelsError.value}</p>}
        {publicationError.value && <p role="alert" class="mx-5 mt-4 rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-800">{t("cluster.publicationRefreshFailed")}</p>}
        <div class="flex flex-wrap items-center gap-3 px-5 py-4">
          <input type="search" class="min-w-48 flex-1 rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" value={query} aria-label={t("cluster.modelSearch")} placeholder={t("cluster.modelSearch")} onInput={e => setQuery(e.currentTarget.value)} />
          <select class="max-w-56 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-600" value={machine} aria-label={t("cluster.instanceMachine")} onChange={e => setMachine(e.currentTarget.value)}>
            <option value="">{t("cluster.modelAllMachines")}</option>{[...machines.values()].map(node => <option key={node.uuid} value={node.uuid}>{node.name || t("deployments.unknownNode")}</option>)}
          </select>
          <select class="rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-600" value={stateFilter} aria-label={t("cluster.colStatus")} onChange={e => setStateFilter(e.currentTarget.value as ModelInstanceFilter)}>
            {(["all", "running", "preparing", "inactive", "failed", "offline"] as const).map(state => <option value={state} key={state}>{t(`cluster.modelFilter.${state}`)}</option>)}
          </select>
          <span class="text-xs text-gray-400">{t("cluster.modelRowCount", rows.length, allRows.length)}</span>
        </div>
        {!rows.length ? <p class="px-5 py-12 text-center text-sm text-gray-400">{t(modelsLoading.value ? "cluster.loading" : modelsError.value && !allRows.length ? "cluster.modelRefreshFailed" : allRows.length ? "cluster.modelNoMatches" : "cluster.modelManagementEmpty")}</p> : <div class="overflow-x-auto px-5">
          <table class="w-full min-w-[64rem] table-fixed text-left text-sm">
            <colgroup>
              <col class="w-[22%]" />
              <col class="w-[14%]" />
              <col />
              <col class="w-28" />
              <col class="w-24" />
              <col class="w-24" />
              <col class="w-44" />
            </colgroup>
            <thead><tr class="border-b border-gray-200 text-xs text-gray-500">
              {["cluster.instanceModelID", "cluster.instanceMachine", "cluster.runtimeStatus", "cluster.publicationStatus", "cluster.instanceSpeed", "cluster.instanceSize", "cluster.colActions"].map(key => <th key={key} class="px-3 py-3 first:pl-0 font-medium whitespace-nowrap">{t(key)}</th>)}
            </tr></thead>
            <tbody>{rows.map(row => {
              const { model, node, sync, deployment } = row;
              const label = model.repo || model.id;
              const syncKey = `${label}::all`;
              const publication = modelPublication(row, view, publications);
              const state = modelRowState(row);
              const kind = modelInstanceCategory(row);
              const progress = deployment ? deploymentProgress(deployment.status) : null;
              return <tr key={row.key} data-model-instance={row.key} data-deployment-id={deployment?.id} class="border-b border-gray-100 align-top last:border-0 hover:bg-gray-50/60">
                <td class="max-w-xs py-4 pr-3">
                  <p class="break-all font-mono text-xs font-medium text-gray-900">{label}</p>
                  <div class="mt-2 flex flex-wrap gap-1.5 text-[11px]"><span class={`rounded px-1.5 py-0.5 ${isManagedModelInstance(row) ? "bg-indigo-50 text-indigo-700" : "bg-gray-100 text-gray-500"}`}>{t(isManagedModelInstance(row) ? "cluster.modelManaged" : "cluster.modelExisting")}</span>{!row.synthetic && kind !== "unsupported" && <span class="rounded bg-gray-50 px-1.5 py-0.5 text-gray-500">{t(`services.${kind}`)}</span>}</div>
                  {deployment && <details class="mt-2 text-xs text-gray-400"><summary class="cursor-pointer hover:text-gray-600">{deployment.name}</summary><p class="mt-2 break-all">{deployment.artifact_source} · {deployment.revision}{deployment.quant ? ` · ${deployment.quant}` : ""}</p><p class="mt-1 break-all font-mono">{model.id}</p></details>}
                  {!deployment && model.id !== label && <p class="mt-1 break-all text-xs text-gray-400">{model.id}</p>}
                </td>
                <td class="max-w-[12rem] px-3 py-4"><p class="break-words font-medium text-gray-800">{node.name || t("deployments.unknownNode")}</p><p class="mt-1 text-xs text-gray-400">{t(`cluster.role.${clusterNodeRole(node.uuid, view.cluster)}`)}{node.local ? ` · ${t("cluster.thisNode")}` : ""}</p></td>
                <td class="min-w-36 max-w-64 px-3 py-4">
                  <span class={`inline-block whitespace-nowrap rounded-full px-2 py-1 text-xs ${!node.online ? "bg-gray-100 text-gray-500" : state === "deployments.phase.failed" ? "bg-red-50 text-red-700" : modelNodeState(row) ? "bg-amber-50 text-amber-700" : state === "cluster.instanceRunning" ? "bg-emerald-50 text-emerald-700" : model.loading || sync || state === "deployments.phase.downloading" ? "bg-indigo-50 text-indigo-700" : "bg-gray-100 text-gray-600"}`}>{t(state)}</span>
                  {modelNodeState(row) && <p class="mt-2 text-xs text-amber-700" title={t("cluster.nodePausedHint")}>{t(modelNodeState(row))}</p>}
                  {deployment?.status.phase === "downloading" && progress && <div class="mt-2 min-w-0 space-y-2 text-xs text-indigo-600">
                    <div class="flex items-center gap-2">
                      <progress class="block h-1.5 min-w-0 flex-1" value={progress.percent ?? undefined} max={100} aria-label={t("deployments.phase.downloading")} />
                      <span class="w-12 shrink-0 text-right tabular-nums">{progress.percent === null ? "—" : `${progress.percent}%`}</span>
                    </div>
                    <p class="h-4 truncate text-gray-400" title={deployment.status.detail}>{deployment.status.detail}</p>
                  </div>}
                  {deployment?.status.error && <DeploymentMessage message={deployment.status.error} class="mt-2 text-xs text-red-600" />}
                  {deployment?.status.detail && !deployment.status.error && deployment.status.phase !== "downloading" && <DeploymentMessage message={deployment.status.detail} class="mt-2 text-xs text-gray-400" />}
                  {node.online && sync && <div class="mt-2"><SyncProgress sync={sync} /></div>}
                </td>
                <td class="px-3 py-4" title={publication.services.map(service => service.model).join("\n")}><span class={`inline-block whitespace-nowrap rounded-full px-2 py-1 text-xs ${publication.state === "published" ? "bg-indigo-50 text-indigo-700" : publication.state === "disabled" ? "bg-amber-50 text-amber-700" : "bg-gray-100 text-gray-500"}`}>{t(`cluster.publication.${publication.state}`)}</span>{publication.services.length > 1 && <p class="mt-1 text-xs text-gray-400">{t("cluster.publicationServices", publication.services.length)}</p>}</td>
                <td class="whitespace-nowrap px-3 py-4 tabular-nums" title={t("cluster.instanceSpeedHint")}><span class="font-medium text-gray-900">{measuredRate(row)}</span>{measuredRate(row) !== "—" && <span class="ml-1 text-xs text-gray-400">tok/s</span>}</td>
                <td class="whitespace-nowrap px-3 py-4 text-gray-600">{model.size > 0 ? `${fmtGB(model.size)} GB` : "—"}{model.format && <p class="mt-1 text-xs text-gray-400">{model.format}</p>}</td>
                <td class="min-w-44 px-3 py-4"><ClusterModelActions row={row} rows={allRows} view={view} publication={publication} stale={stale} busy={controls.busy} onPublish={() => controls.publish(row)} onChanged={changed}
                  onSync={canSync ? () => void handleSync(label, "all", syncKey) : undefined} syncBusy={syncBusy.value !== ""} /></td>
              </tr>;
            })}</tbody>
          </table>
        </div>}
        <div class="border-t border-gray-100 px-5 py-4"><p class="text-xs leading-5 text-gray-400">{t("cluster.modelStateLegend")}</p></div>
      </section>
      <details class="rounded-xl border border-gray-200 bg-white p-5"><summary class="cursor-pointer text-sm font-medium text-gray-600">{t("cluster.instanceDiagnostics")}</summary><div class="mt-5 grid gap-6 lg:grid-cols-2">{canSync && <RecommendationsCard />}<ExplainCard models={list} /></div></details>
    </>}
  </ClusterDeploymentControls>;
}

function SyncProgress({ sync }: { sync: ClusterModelSync }) {
  const known = (sync.total_bytes ?? 0) > 0;
  const pct = known ? percentOf(sync.completed_bytes ?? 0, sync.total_bytes ?? 0) : 0;
  const label = sync.status === "queued" && !known
    ? t("cluster.syncQueued")
    : known
      ? `${pct}%`
      : t("cluster.cellSyncing");
  return (
    <div class="mx-auto w-16" title={sync.detail || label}>
      <div class="h-1.5 overflow-hidden rounded-full bg-indigo-100">
        <div
          class={`h-full rounded-full bg-indigo-600 ${known ? "" : "w-1/2 animate-pulse"}`}
          style={known ? { width: `${pct}%` } : undefined}
        />
      </div>
      <div class="mt-1 text-[10px] font-medium leading-none text-indigo-700">{label}</div>
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
                    → <span class="font-medium text-gray-700">{rec.node_name}</span> · {clusterReasonText(rec.reason_detail, rec.reason, t)}
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
  const ordered = result ? [...result.candidates].sort((a, b) => Number(b.eligible) - Number(a.eligible) || (a.rank ?? 0) - (b.rank ?? 0)) : [];
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
              <option key={m.id} value={m.id}>{m.repository || m.id}</option>
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
                  c.factors && c.factors.length > 0 && <div class="mt-0.5 text-xs text-gray-500">{(c.factor_details?.map((reason, index) => clusterReasonText(reason, c.factors?.[index] ?? "", t)) ?? c.factors).join(" · ")}</div>
                ) : (
                  <div class="mt-0.5 text-xs">{t("cluster.excluded", clusterReasonText(c.excluded_reason, c.excluded || t("cluster.unknownError"), t))}</div>
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
    if (view.mode !== "worker") void loadToken();
  }, []);
  const form = settingsForm.value ?? view.settings;
  const update = (patch: Partial<ClusterSettings>) => {
    settingsForm.value = { ...form, ...patch };
    settingsSaved.value = false;
  };
  const token = tokenInfo.value;
  return (
    <div class="space-y-6">
      {(!view.mode || view.mode === "lan") && <section class="rounded-xl border border-gray-200 bg-white p-6">
        <h2 class="text-lg font-bold text-gray-900">{t("deployments.management")}</h2>
        <p class="mt-1 text-sm text-gray-500">{t("deployments.managementHint")}</p>
        <label class="mt-4 block text-sm text-gray-700">{t("deployments.controller")}
          <select class="mt-2 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm" value={form.managed_by || ""} disabled={settingsSaving.value} onChange={event => update({ managed_by: event.currentTarget.value })}>
            <option value="">{t("deployments.sharingOnly")}</option>
            {view.members.filter(n => !n.local).map(n => <option key={n.uuid} value={n.uuid}>{n.name}</option>)}
          </select>
        </label>
        <p class="mt-2 text-xs text-gray-500">{t("deployments.authorizationSaveHint")}</p>
      </section>}
      {/* Join token */}
      {view.mode !== "worker" && <section class="bg-white rounded-xl border border-gray-200 p-6">
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
      </section>}

      {/* Routing & node */}
      <section class="bg-white rounded-xl border border-gray-200 p-6">
        <h2 class="text-lg font-bold text-gray-900">{t(view.mode === "worker" ? "deployments.workerSettings" : "cluster.routingTitle")}</h2>
        <p class="mt-1 text-sm text-gray-500">{t(view.mode === "worker" ? "deployments.workerSettingsHint" : "cluster.routingDesc")}</p>

        <div class="mt-5 space-y-5">
          {view.mode === "worker" && <div class="space-y-2">
            <div class="text-sm font-medium text-gray-700">{t("cluster.workerNodeName")}</div>
            {nameEditing.value ? <div class="flex flex-wrap items-center gap-2">
              <input aria-label={t("cluster.workerNodeName")} class="min-w-0 flex-1 rounded-lg border border-gray-200 px-3 py-2 text-sm" value={nameDraft.value} disabled={nameSaving.value} onInput={e => (nameDraft.value = e.currentTarget.value)} />
              <button type="button" class="text-sm text-indigo-600" disabled={nameSaving.value} onClick={() => void saveName(view.node.uuid)}>{t("cluster.save")}</button>
              <button type="button" class="text-sm text-gray-500" disabled={nameSaving.value} onClick={() => (nameEditing.value = false)}>{t("confirm.cancel")}</button>
            </div> : <div class="flex items-center gap-3"><span class="text-sm text-gray-900">{view.node.name}</span><button type="button" class="text-xs text-indigo-600" onClick={() => startNameEdit(view.node.name)}>{t("cluster.rename")}</button></div>}
            {overviewError.value && <p role="alert" class="text-sm text-red-600">{overviewError.value}</p>}
          </div>}
          {view.mode === "worker" && <div>
            <label class="block text-sm font-medium text-gray-700" for="worker-state">{t("cluster.workerState")}</label>
            <select id="worker-state" class="mt-2 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm" value={workerNodeState(form)} disabled={settingsSaving.value}
              onChange={event => update({ state: event.currentTarget.value as ClusterNodeState, accept_work: true })}>
              <option value="active">{t("cluster.stateActive")}</option>
              <option value="drain">{t("cluster.stateDrain")}</option>
              <option value="maintenance">{t("cluster.stateMaintenance")}</option>
            </select>
            <p class="mt-2 text-xs text-gray-500">{t("cluster.workerStateHint")}</p>
          </div>}
          {view.mode !== "worker" && <>
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

          </>}
          {view.mode !== "worker" && <div class="flex items-start justify-between gap-4 border-t border-gray-100 pt-4">
            <div>
              <div class="text-sm font-medium text-gray-700">{t("cluster.acceptWork")}</div>
              <div class="text-xs text-gray-500">{t("cluster.acceptWorkDesc")}</div>
            </div>
            <Toggle checked={form.accept_work} onChange={(v) => update({ accept_work: v })} />
          </div>}

          <div class={`grid gap-4 border-t border-gray-100 pt-4 ${view.mode === "worker" ? "sm:grid-cols-2" : "sm:grid-cols-3"}`}>
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
              <p class="mt-1 text-xs text-gray-400">{t(view.mode === "worker" ? "cluster.workerWeightDesc" : "cluster.weightDesc")}</p>
            </div>
            {view.mode !== "worker" && <div>
              <label class="block text-sm font-medium text-gray-700">{t("cluster.affinityMaxQueue")}</label>
              <input
                type="number"
                min={0}
                class="mt-1 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={form.affinity_max_queue}
                onInput={(e) => update({ affinity_max_queue: Number((e.target as HTMLInputElement).value) })}
              />
              <p class="mt-1 text-xs text-gray-400">{t("cluster.affinityMaxQueueDesc")}</p>
            </div>}
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

          {view.mode === "worker" && (
            <details class="rounded-xl border border-gray-200 p-4">
              <summary class="cursor-pointer text-sm font-medium text-gray-700">{t("cluster.sharedModels")}</summary>
              <p class="mt-1 text-xs text-gray-500">{t("cluster.sharedModelsDesc")}</p>
              <textarea
                class="mt-2 min-h-28 w-full rounded-lg border border-gray-200 px-3 py-2 font-mono text-sm"
                value={(form.shared_models ?? []).join("\n")}
                placeholder={t("cluster.sharedModelsPlaceholder")}
                onInput={(e) => update({ shared_models: (e.target as HTMLTextAreaElement).value.split("\n") })}
              />
            </details>
          )}

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
            <h2 class="text-lg font-bold text-red-700">{t(view.mode === "gateway" ? "cluster.masterLeaveTitle" : "cluster.leaveTitle")}</h2>
            <p class="mt-1 text-sm text-gray-500">{t(view.mode === "worker" ? "cluster.workerLeaveDesc" : view.mode === "gateway" ? "cluster.masterLeaveDesc" : "cluster.leaveDesc")}</p>
          </div>
          <button
            type="button"
            onClick={() => (leaveOpen.value = true)}
            class="rounded-lg border border-red-300 bg-white px-4 py-2 text-sm font-medium text-red-600 transition-colors hover:bg-red-50"
          >
            {t(view.mode === "gateway" ? "cluster.masterLeaveTitle" : "cluster.leaveAction")}
          </button>
        </div>
      </section>
    </div>
  );
}
