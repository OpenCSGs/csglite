import { signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { t, locale } from "../i18n";
import { formatNumber, formatDateTime, formatChartDate, chartXAxisLabels } from "../utils/format";
import { useRuntimeAPIOrigin } from "../utils/runtimeAPIOrigin";
import {
  isValidatedCollapsedProfile,
  previewResponseIsCurrent,
  routerActivationReasonKey,
  routerEvaluationModeKey,
  routerJobCallCounts,
  routerProfileKind,
  routerUnknownPricingNeedsConsent,
  snapshotRouterEvaluationRequest,
} from "../utils/routerEvaluationPreview";
import { routerOverviewCounts } from "../utils/routerStatus";
import { ProviderModelModalityBadges, providerModelLabel, defaultProviderModelDisplayName } from "../components/ProviderModelBadges";
import { ApiInfoDialog } from "../components/ApiInfoDialog";
import {
  activateProviderPoolRouterProfile,
  cancelProviderPoolRouterEvaluation,
  clearCloudAPIKey,
  createProviderPoolRouterEvaluation,
  createProvider,
  createProviderPool,
  createLocalAPIKey,
  deleteProvider,
  deleteProviderPool,
  deleteLocalAPIKey,
  getCloudAuthStatus,
  getSettings,
  getTags,
  getLocalAPIKeys,
  getLocalAPIUsage,
  getProviderManageTags,
  getProviderPoolPolicies,
  getProviderPoolRouterProfile,
  getProviderPoolRouterStatus,
  getProviderPools,
  getProviderSelectedTags,
  getProviders,
  replaceProviderManageTags,
  previewProviderPoolRouterEvaluation,
  rollbackProviderPoolRouterProfile,
  saveCloudAPIKey,
  updateProvider,
  updateProviderPool,
  updateProviderManageTag,
  validateProvider,
  updateLocalAPIKeySettings,
} from "../api/client";
import type { CloudAuthStatus, LocalAPIKeysResponse, LocalAPIUsageResponse, LocalAPIUsageTotalSummary, ModelInfo, ProviderHeader, ProviderPool, ProviderPoolMember, ProviderPoolPolicy, ProviderPoolPolicyType, ProviderPoolRouterEvaluationJob, ProviderPoolRouterEvaluationPreview, ProviderPoolRouterEvaluationRequest, ProviderPoolRouterProfile, ProviderPoolRouterStatus, ProviderTagModelSelection, ThirdPartyProvider } from "../api/client";

type GatewayTab = "apiKeys" | "providers" | "pools" | "usage";
type UsagePeriod = "week" | "month" | "year";
type ManagedProvider = ThirdPartyProvider & { builtIn?: boolean; source?: "cloud" | "provider" };
type GatewayAPIInfoTarget = {
  targetID: string;
  model: ModelInfo;
};

const activeGatewayTab = signal<GatewayTab>("apiKeys");
const localAPIKeys = signal<LocalAPIKeysResponse | null>(null);
const localAPIKeysLoading = signal(false);
const localAPIKeysError = signal("");
const localAPIKeyName = signal("");
const localAPIKeyCreated = signal("");
const isLocalAPIKeyDialogOpen = signal(false);
const localAPIKeySaving = signal(false);
const localAPIKeyDeleting = signal("");
const localAPIUsage = signal<LocalAPIUsageResponse | null>(null);
const localAPIUsageLoading = signal(false);
const localAPIUsageError = signal("");
const localAPIUsagePeriod = signal<UsagePeriod>("week");
const localAPIUsageProvider = signal("");
const providers = signal<ThirdPartyProvider[]>([]);
const providerPools = signal<ProviderPool[]>([]);
const providerPoolModels = signal<ModelInfo[]>([]);
const cloudManagedProvider = signal<ManagedProvider | null>(null);
const providersLoading = signal(false);
const providersError = signal("");
const isProviderPoolDialogOpen = signal(false);
const editingProviderPool = signal<ProviderPool | null>(null);
const providerPoolFormName = signal("");
const providerPoolFormModel = signal("");
const providerPoolFormEnabled = signal(true);
const providerPoolFormPolicy = signal<ProviderPoolPolicyType | string>("priority_weight");
const providerPoolFormMembers = signal<ProviderPoolMember[]>([]);
const providerPoolFormError = signal("");
const providerPoolFormSaving = signal(false);
const providerPoolDialogStep = signal<"basics" | "members">("basics");
const providerPoolSourceFilter = signal("local");
const providerPoolModelSearch = signal("");
const providerPoolMemberConfigIndex = signal<number | null>(null);
const providerPoolMemberConfigDraft = signal<ProviderPoolMember | null>(null);
const providerPoolPolicies = signal<ProviderPoolPolicy[]>([]);
const providerPoolPoliciesLoading = signal(false);
const providerPoolPoliciesError = signal("");
const providerPoolRouterStatuses = signal<Record<string, ProviderPoolRouterStatus>>({});
const providerPoolRouterStatusLoading = signal<Record<string, boolean>>({});
const providerPoolRouterDialogPool = signal<ProviderPool | null>(null);
const providerPoolRouterDialogStep = signal<"overview" | "configure" | "progress" | "profile">("overview");
const providerPoolRouterPreview = signal<ProviderPoolRouterEvaluationPreview | null>(null);
const providerPoolRouterPreviewConfig = signal<ProviderPoolRouterEvaluationRequest | null>(null);
const providerPoolRouterMinHistory = 20;
let providerPoolRouterPreviewGeneration = 0;
const providerPoolRouterJob = signal<ProviderPoolRouterEvaluationJob | null>(null);
const providerPoolRouterProfile = signal<ProviderPoolRouterProfile | null>(null);
const providerPoolRouterError = signal("");
const providerPoolRouterBusy = signal(false);
const providerPoolRouterConfig = signal<ProviderPoolRouterEvaluationRequest>({
  evaluation_mode: "listwise_v2",
  judge_model: "",
  max_queries: 25,
  repeats: 1,
  max_output_tokens: 1024,
  request_timeout_seconds: 60,
  budget_currency: "￥",
  budget_amount: 10,
  allow_unknown_pricing: false,
});
const gatewayAPIInfoTarget = signal<GatewayAPIInfoTarget | null>(null);
const cloudAuth = signal<CloudAuthStatus | null>(null);
const cloudAPIKeyInput = signal("");
const cloudAPIKeyError = signal("");
const cloudProviderName = signal("");
const cloudGatewayURL = signal("");
const isClearingCloudAPIKey = signal(false);
const isSavingCloudAPIKey = signal(false);
const copiedSnippet = signal("");
const isProviderDialogOpen = signal(false);
const editingProvider = signal<ThirdPartyProvider | null>(null);
const providerFormName = signal("");
const providerFormBaseURL = signal("");
const providerFormAPIKey = signal("");
const providerFormHeaders = signal<ProviderHeader[]>([]);
const providerFormType = signal("openai");
const providerFormEnabled = signal(true);
const providerFormError = signal("");
const providerFormSaving = signal(false);
const providerFormTesting = signal(false);
const providerFormTestSuccess = signal("");
const providerDialogStep = signal<"details" | "models">("details");
const providerModelTarget = signal<ManagedProvider | null>(null);
const providerModelCatalog = signal<ModelInfo[]>([]);
const providerModelSelected = signal<Record<string, boolean>>({});
const providerModelDisplayNames = signal<Record<string, string>>({});
const providerModelsLoading = signal(false);
const providerModelsSaving = signal(false);
const providerModelsError = signal("");
const providerSelectedModels = signal<Record<string, ModelInfo[]>>({});
const isProviderModelEditOpen = signal(false);
const providerModelEditProvider = signal<ManagedProvider | null>(null);
const providerModelEditCurrentID = signal("");
const providerModelEditID = signal("");
const providerModelEditDisplayName = signal("");
const providerModelEditDescription = signal("");
const providerModelEditInitialDescription = signal("");
const providerModelEditPlaceholder = signal("");
const providerModelEditError = signal("");
const providerModelEditSaving = signal(false);
const providersChangedEvent = "csghub:providers-changed";
let localAPIUsageRequestID = 0;

const providerTypes = [
  { value: "openai", label: "OpenAI Compatible", name: "OpenAI", baseURL: "https://api.openai.com/v1" },
  { value: "deepseek", label: "DeepSeek", name: "DeepSeek", baseURL: "https://api.deepseek.com/v1" },
  { value: "minimax", label: "MiniMax", name: "MiniMax", baseURL: "https://api.minimaxi.com/v1" },
  { value: "mimo", label: "MiMo (Xiaomi)", name: "MiMo", baseURL: "https://api.xiaomimimo.com/v1" },
  { value: "kimi", label: "Kimi (Moonshot)", name: "Kimi", baseURL: "https://api.moonshot.cn/v1" },
  { value: "dashscope", label: "DashScope", name: "DashScope", baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1" },
  { value: "bigmodel", label: "BigModel (Zhipu)", name: "BigModel", baseURL: "https://open.bigmodel.cn/api/paas/v4" },
  { value: "qianfan", label: "Qianfan (Baidu)", name: "Qianfan", baseURL: "https://qianfan.baidubce.com/v2" },
  { value: "openrouter", label: "OpenRouter", name: "OpenRouter", baseURL: "https://openrouter.ai/api/v1" },
  { value: "other", label: "Other", name: "", baseURL: "" },
];

async function fetchLocalAPIKeys() {
  localAPIKeysLoading.value = true;
  localAPIKeysError.value = "";
  try {
    localAPIKeys.value = await getLocalAPIKeys();
  } catch (err: any) {
    localAPIKeysError.value = err?.message || t("settings.localAPIKeysLoadFailed");
  } finally {
    localAPIKeysLoading.value = false;
  }
}

async function fetchLocalAPIUsage(period: UsagePeriod = localAPIUsagePeriod.value, provider: string = localAPIUsageProvider.value) {
  const requestID = ++localAPIUsageRequestID;
  localAPIUsageLoading.value = true;
  localAPIUsageError.value = "";
  try {
    const usage = await getLocalAPIUsage(period, provider);
    if (requestID !== localAPIUsageRequestID) return;
    localAPIUsage.value = usage;
  } catch (err: any) {
    if (requestID !== localAPIUsageRequestID) return;
    localAPIUsageError.value = err?.message || t("settings.apiUsageLoadFailed");
  } finally {
    if (requestID === localAPIUsageRequestID) {
      localAPIUsageLoading.value = false;
    }
  }
}

function notifyProvidersChanged() {
  window.dispatchEvent(new Event(providersChangedEvent));
}

function openExternal(url?: string) {
  if (url) {
    window.open(url, "_blank", "noopener,noreferrer");
  }
}

function fetchCloudAuth() {
  getCloudAuthStatus()
    .then((status) => {
      cloudAuth.value = status;
      cloudAPIKeyError.value = "";
    })
    .catch((err: any) => {
      cloudAuth.value = null;
      cloudAPIKeyError.value = err?.message || "";
    });
}

function fetchCloudSettings() {
  getSettings()
    .then((settings) => {
      cloudProviderName.value = settings.cloud_provider_name || settings.default_cloud_provider_name || "";
      cloudGatewayURL.value = settings.ai_gateway_url || settings.default_ai_gateway_url || "";
			cloudManagedProvider.value = cloudProviderFromSettings(settings);
    })
    .catch(() => {
      cloudProviderName.value = "";
      cloudGatewayURL.value = "";
			cloudManagedProvider.value = null;
    });
}

async function fetchProviderOptions() {
  providersLoading.value = true;
  providersError.value = "";
  try {
		const [list, settings, pools, models] = await Promise.all([getProviders(), getSettings(), getProviderPools(), getTags()]);
		const cloudProvider = cloudProviderFromSettings(settings);
		cloudProviderName.value = settings.cloud_provider_name || settings.default_cloud_provider_name || "";
		cloudGatewayURL.value = settings.ai_gateway_url || settings.default_ai_gateway_url || "";
		cloudManagedProvider.value = cloudProvider;
    providers.value = list;
    providerPools.value = pools;
    providerPoolModels.value = models;
    fetchProviderPoolRouterStatuses(pools);
		const managedProviders = [cloudProvider, ...list];
    const entries = await Promise.all(
			managedProviders.map(async (provider) => {
        try {
					return [provider.id, await getProviderSelectedTags(provider.id)] as const;
        } catch {
          return [provider.id, []] as const;
        }
      })
    );
    providerSelectedModels.value = Object.fromEntries(entries);
  } catch (err: any) {
    providersError.value = err?.message || t("settings.providersLoadFailed");
  } finally {
    providersLoading.value = false;
  }
}

async function fetchProviderPoolPolicies() {
  providerPoolPoliciesLoading.value = true;
  providerPoolPoliciesError.value = "";
  try {
    providerPoolPolicies.value = await getProviderPoolPolicies();
  } catch (err: any) {
    providerPoolPoliciesError.value = err?.message || t("settings.providerPoolPoliciesLoadFailed");
  } finally {
    providerPoolPoliciesLoading.value = false;
  }
}

async function fetchProviderPoolRouterStatus(poolID: string) {
  providerPoolRouterStatusLoading.value = { ...providerPoolRouterStatusLoading.value, [poolID]: true };
  try {
    const status = await getProviderPoolRouterStatus(poolID);
    providerPoolRouterStatuses.value = { ...providerPoolRouterStatuses.value, [poolID]: status };
    if (providerPoolRouterDialogPool.value?.id === poolID) {
      if (status.running_job || status.latest_job) {
        providerPoolRouterJob.value = status.running_job || status.latest_job || null;
      }
      if (status.latest_candidate_profile && providerPoolRouterProfile.value?.id === status.latest_candidate_profile.id) {
        providerPoolRouterProfile.value = status.latest_candidate_profile;
      }
    }
  } catch (err: any) {
    if (providerPoolRouterDialogPool.value?.id === poolID) {
      providerPoolRouterError.value = err?.message || t("settings.routerStatusFailed");
    }
  } finally {
    providerPoolRouterStatusLoading.value = { ...providerPoolRouterStatusLoading.value, [poolID]: false };
  }
}

function fetchProviderPoolRouterStatuses(pools: ProviderPool[] = providerPools.value) {
  for (const pool of pools) {
    if (pool.policy === "semantic") void fetchProviderPoolRouterStatus(pool.id);
  }
}

function providerPoolRouterJudgeModels(): ModelInfo[] {
  return providerPoolModels.value.filter((model) =>
    model.source === "cloud" && model.pipeline_tag === "text-generation"
  );
}

async function openProviderPoolRouterDialog(pool: ProviderPool) {
  providerPoolRouterDialogPool.value = pool;
  providerPoolRouterDialogStep.value = "overview";
  providerPoolRouterPreview.value = null;
  providerPoolRouterProfile.value = null;
  providerPoolRouterError.value = "";
  const judges = providerPoolRouterJudgeModels();
  if (!providerPoolRouterConfig.value.judge_model && judges[0]) {
    providerPoolRouterConfig.value = { ...providerPoolRouterConfig.value, judge_model: judges[0].model };
  }
  await fetchProviderPoolRouterStatus(pool.id);
  const status = providerPoolRouterStatuses.value[pool.id];
  providerPoolRouterJob.value = status?.running_job || status?.latest_job || null;
  if (status?.running_job) providerPoolRouterDialogStep.value = "progress";
}

function closeProviderPoolRouterDialog() {
  providerPoolRouterDialogPool.value = null;
  providerPoolRouterError.value = "";
  providerPoolRouterBusy.value = false;
}

function updateProviderPoolRouterConfig<K extends keyof ProviderPoolRouterEvaluationRequest>(
  key: K,
  value: ProviderPoolRouterEvaluationRequest[K],
) {
  providerPoolRouterConfig.value = { ...providerPoolRouterConfig.value, [key]: value };
  providerPoolRouterPreview.value = null;
  providerPoolRouterPreviewConfig.value = null;
  providerPoolRouterPreviewGeneration += 1;
}

function updateProviderPoolRouterUnknownPricingConsent(value: boolean) {
  providerPoolRouterConfig.value = {
    ...providerPoolRouterConfig.value,
    allow_unknown_pricing: value,
  };
  if (providerPoolRouterPreviewConfig.value) {
    providerPoolRouterPreviewConfig.value = {
      ...providerPoolRouterPreviewConfig.value,
      allow_unknown_pricing: value,
    };
  }
}

async function previewProviderPoolEvaluation() {
  const pool = providerPoolRouterDialogPool.value;
  if (!pool) return;
  providerPoolRouterBusy.value = true;
  providerPoolRouterError.value = "";
  const generation = ++providerPoolRouterPreviewGeneration;
  const snapshot = snapshotRouterEvaluationRequest(providerPoolRouterConfig.value);
  try {
    const result = await previewProviderPoolRouterEvaluation(pool.id, snapshot);
    if (previewResponseIsCurrent(generation, providerPoolRouterPreviewGeneration)) {
      providerPoolRouterPreview.value = result;
      providerPoolRouterPreviewConfig.value = snapshot;
    }
  } catch (err: any) {
    if (previewResponseIsCurrent(generation, providerPoolRouterPreviewGeneration)) {
      providerPoolRouterError.value = err?.message || t("settings.routerPreviewFailed");
    }
  } finally {
    if (previewResponseIsCurrent(generation, providerPoolRouterPreviewGeneration)) {
      providerPoolRouterBusy.value = false;
    }
  }
}

async function startProviderPoolEvaluation() {
  const pool = providerPoolRouterDialogPool.value;
  if (!pool || !providerPoolRouterPreview.value) return;
  providerPoolRouterBusy.value = true;
  providerPoolRouterError.value = "";
  try {
    const previewed = providerPoolRouterPreviewConfig.value;
    if (!previewed) return;
    const job = await createProviderPoolRouterEvaluation(pool.id, previewed);
    providerPoolRouterJob.value = job;
    providerPoolRouterDialogStep.value = "progress";
    await fetchProviderPoolRouterStatus(pool.id);
  } catch (err: any) {
    providerPoolRouterError.value = err?.message || t("settings.routerStartFailed");
  } finally {
    providerPoolRouterBusy.value = false;
  }
}

async function cancelProviderPoolEvaluation() {
  const pool = providerPoolRouterDialogPool.value;
  const job = providerPoolRouterJob.value;
  if (!pool || !job || !confirm(t("settings.routerCancelConfirm"))) return;
  providerPoolRouterBusy.value = true;
  try {
    providerPoolRouterJob.value = await cancelProviderPoolRouterEvaluation(pool.id, job.id);
    await fetchProviderPoolRouterStatus(pool.id);
  } catch (err: any) {
    providerPoolRouterError.value = err?.message || t("settings.routerCancelFailed");
  } finally {
    providerPoolRouterBusy.value = false;
  }
}

async function reviewProviderPoolRouterProfile(profileID?: string) {
  const pool = providerPoolRouterDialogPool.value;
  const id = profileID || providerPoolRouterStatuses.value[pool?.id || ""]?.latest_candidate_profile?.id;
  if (!pool || !id) return;
  providerPoolRouterBusy.value = true;
  providerPoolRouterError.value = "";
  try {
    providerPoolRouterProfile.value = await getProviderPoolRouterProfile(pool.id, id);
    providerPoolRouterDialogStep.value = "profile";
  } catch (err: any) {
    providerPoolRouterError.value = err?.message || t("settings.routerProfileFailed");
  } finally {
    providerPoolRouterBusy.value = false;
  }
}

async function activateProviderPoolRouterCandidate() {
  const pool = providerPoolRouterDialogPool.value;
  const profile = providerPoolRouterProfile.value;
  if (!pool || !profile || !profile.activation_allowed || !confirm(t("settings.routerActivateConfirm", profile.version))) return;
  providerPoolRouterBusy.value = true;
  try {
    await activateProviderPoolRouterProfile(pool.id, profile.id, t("settings.routerActivationReason"));
    await fetchProviderPoolRouterStatus(pool.id);
    providerPoolRouterProfile.value = await getProviderPoolRouterProfile(pool.id, profile.id);
  } catch (err: any) {
    providerPoolRouterError.value = err?.message || t("settings.routerActivateFailed");
  } finally {
    providerPoolRouterBusy.value = false;
  }
}

async function rollbackProviderPoolRouter() {
  const pool = providerPoolRouterDialogPool.value;
  const status = providerPoolRouterStatuses.value[pool?.id || ""];
  const target = status?.rollback_target_profile;
  if (!pool || !status?.current_profile_id || !target ||
      !confirm(t("settings.routerRollbackConfirm", target.version))) return;
  providerPoolRouterBusy.value = true;
  try {
    await rollbackProviderPoolRouterProfile(pool.id, status.current_profile_id, t("settings.routerRollbackReason"));
    await fetchProviderPoolRouterStatus(pool.id);
    providerPoolRouterDialogStep.value = "overview";
  } catch (err: any) {
    providerPoolRouterError.value = err?.message || t("settings.routerRollbackFailed");
  } finally {
    providerPoolRouterBusy.value = false;
  }
}

function cloudProviderFromSettings(settings: Awaited<ReturnType<typeof getSettings>>): ManagedProvider {
	return {
		id: "csghub",
		name: settings.cloud_provider_name || settings.default_cloud_provider_name || "csghub",
		base_url: settings.ai_gateway_url || settings.default_ai_gateway_url || "",
		provider: "csghub",
		enabled: true,
		builtIn: true,
		source: "cloud",
	};
}

function providerCards(): ManagedProvider[] {
	const cloud = cloudManagedProvider.value;
	return cloud ? [cloud, ...providers.value] : [...providers.value];
}

function openGatewayAPIInfo(targetID: string, model: ModelInfo) {
  gatewayAPIInfoTarget.value = {
    targetID,
    model,
  };
}

function providerPoolModelInfo(pool: ProviderPool): ModelInfo {
  const primaryMember = sortProviderPoolMembers(pool.members)[0];
  const primaryModel = primaryMember && providerPoolModels.value.find((model) =>
    model.source === primaryMember.source && model.model === primaryMember.model
  );
  if (primaryModel) {
    return {
      ...primaryModel,
      name: pool.model,
      model: pool.model,
      source: `pool:${pool.id}`,
      provider: pool.name,
    };
  }
  return {
    name: pool.model,
    model: pool.model,
    size: 0,
    format: "api",
    modified_at: "",
    source: `pool:${pool.id}`,
    category: "chat",
    pipeline_tag: "text-generation",
  };
}

function isGatewayEmbeddingModel(model: ModelInfo): boolean {
  const tag = (model.pipeline_tag || "").toLowerCase();
  return (model.category || "").toLowerCase() === "embedding" ||
    tag === "feature-extraction" || tag === "sentence-similarity" || tag === "text-embedding" || tag === "embedding";
}

function isGatewayVisionModel(model: ModelInfo): boolean {
  return (model.pipeline_tag || "").toLowerCase() === "image-text-to-text" ||
    Boolean(model.input_modalities?.includes("image"));
}

function isGatewayASRModel(model: ModelInfo): boolean {
  return (model.pipeline_tag || "").toLowerCase() === "automatic-speech-recognition" ||
    Boolean(model.input_modalities?.includes("audio")) ||
    Boolean(model.output_modalities?.includes("transcription"));
}

async function saveCloudAPIKeyForm() {
  const apiKey = cloudAPIKeyInput.value.trim();
  if (!apiKey) {
    cloudAPIKeyError.value = t("chat.cloudApiKeyEmpty");
    return;
  }

  isSavingCloudAPIKey.value = true;
  cloudAPIKeyError.value = "";
  try {
    cloudAuth.value = await saveCloudAPIKey(apiKey);
    cloudAPIKeyInput.value = "";
  } catch (err: any) {
    cloudAPIKeyError.value = err?.message || t("chat.failedResp");
  } finally {
    isSavingCloudAPIKey.value = false;
  }
}

async function clearCloudAPIKeyForm() {
  if (isClearingCloudAPIKey.value) return;
  isClearingCloudAPIKey.value = true;
  cloudAPIKeyError.value = "";
  try {
    cloudAuth.value = await clearCloudAPIKey();
    cloudAPIKeyInput.value = "";
  } catch (err: any) {
    cloudAPIKeyError.value = err?.message || t("chat.failedResp");
  } finally {
    isClearingCloudAPIKey.value = false;
  }
}

async function toggleLocalAPIAuth(enabled: boolean) {
  localAPIKeySaving.value = true;
  localAPIKeysError.value = "";
  try {
    localAPIKeys.value = await updateLocalAPIKeySettings(enabled);
  } catch (err: any) {
    localAPIKeysError.value = err?.message || t("settings.localAPIAuthSaveFailed");
  } finally {
    localAPIKeySaving.value = false;
  }
}

function openLocalAPIKeyDialog() {
  localAPIKeyName.value = "";
  localAPIKeyCreated.value = "";
  localAPIKeysError.value = "";
  isLocalAPIKeyDialogOpen.value = true;
}

function closeLocalAPIKeyDialog() {
  if (localAPIKeySaving.value) return;
  isLocalAPIKeyDialogOpen.value = false;
  localAPIKeyName.value = "";
  localAPIKeyCreated.value = "";
}

async function createLocalKey() {
  localAPIKeySaving.value = true;
  localAPIKeysError.value = "";
  localAPIKeyCreated.value = "";
  try {
    const resp = await createLocalAPIKey(localAPIKeyName.value.trim());
    localAPIKeyCreated.value = resp.api_key;
    localAPIKeyName.value = "";
    await fetchLocalAPIKeys();
  } catch (err: any) {
    localAPIKeysError.value = err?.message || t("settings.localAPIKeyCreateFailed");
  } finally {
    localAPIKeySaving.value = false;
  }
}

async function removeLocalKey(id: string) {
  if (!confirm(t("settings.localAPIKeyDeleteConfirm"))) return;
  localAPIKeyDeleting.value = id;
  localAPIKeysError.value = "";
  try {
    await deleteLocalAPIKey(id);
    await fetchLocalAPIKeys();
  } catch (err: any) {
    localAPIKeysError.value = err?.message || t("settings.localAPIKeyDeleteFailed");
  } finally {
    localAPIKeyDeleting.value = "";
  }
}

function openProviderDialog(provider?: ThirdPartyProvider) {
  editingProvider.value = provider || null;
  providerModelTarget.value = provider || null;
  providerDialogStep.value = "details";
  providerModelCatalog.value = [];
  providerModelSelected.value = {};
  providerModelDisplayNames.value = {};
  providerModelsError.value = "";
  providerFormName.value = provider?.name || "";
  providerFormBaseURL.value = provider?.base_url || "";
  providerFormAPIKey.value = "";
  providerFormHeaders.value = (provider?.headers || []).map((header) => ({ ...header }));
  providerFormType.value = provider?.provider || "openai";
  providerFormEnabled.value = provider?.enabled ?? true;
  providerFormError.value = "";
  providerFormTestSuccess.value = "";
  isProviderDialogOpen.value = true;
}

function openProviderModelsDialog(provider: ManagedProvider) {
	editingProvider.value = null;
	providerModelTarget.value = provider;
	providerDialogStep.value = "models";
	providerModelCatalog.value = [];
	providerModelSelected.value = {};
	providerModelDisplayNames.value = {};
	providerModelsError.value = "";
	isProviderDialogOpen.value = true;
	void loadProviderDialogModels(provider);
}

function closeProviderDialog() {
  if (providerFormSaving.value || providerFormTesting.value || providerModelsSaving.value) return;
  isProviderDialogOpen.value = false;
  editingProvider.value = null;
  providerModelTarget.value = null;
  providerDialogStep.value = "details";
  providerModelCatalog.value = [];
  providerModelSelected.value = {};
  providerModelDisplayNames.value = {};
  providerModelsError.value = "";
  providerFormError.value = "";
  providerFormTestSuccess.value = "";
  providerFormHeaders.value = [];
}

function openProviderModelEditDialog(provider: ManagedProvider, model: ModelInfo) {
  providerModelEditProvider.value = provider;
  providerModelEditCurrentID.value = model.model;
  providerModelEditID.value = model.model;
  providerModelEditDisplayName.value = "";
  providerModelEditDescription.value = model.description || "";
  providerModelEditInitialDescription.value = model.description || "";
  providerModelEditPlaceholder.value = providerModelLabel(model);
  providerModelEditError.value = "";
  isProviderModelEditOpen.value = true;
}

function closeProviderModelEditDialog() {
  if (providerModelEditSaving.value) return;
  isProviderModelEditOpen.value = false;
  providerModelEditProvider.value = null;
  providerModelEditCurrentID.value = "";
  providerModelEditID.value = "";
  providerModelEditDisplayName.value = "";
  providerModelEditDescription.value = "";
  providerModelEditInitialDescription.value = "";
  providerModelEditPlaceholder.value = "";
  providerModelEditError.value = "";
}

async function saveProviderModelEdit() {
  const provider = providerModelEditProvider.value;
  const currentID = providerModelEditCurrentID.value.trim();
  const nextID = providerModelEditID.value.trim();
  if (!provider || !currentID) return;
  if (!nextID) {
    providerModelEditError.value = t("settings.providerModelIDRequired");
    return;
  }

  providerModelEditSaving.value = true;
  providerModelEditError.value = "";
  try {
    const payload: { model?: string; display_name?: string; description?: string } = {};
    if (nextID !== currentID) {
      payload.model = nextID;
    }
    const displayName = providerModelEditDisplayName.value.trim();
    const description = providerModelEditDescription.value.trim();
    if (displayName) {
      payload.display_name = displayName;
    }
    if (description !== providerModelEditInitialDescription.value) {
      payload.description = description;
    }
    if (Object.keys(payload).length === 0) {
      closeProviderModelEditDialog();
      return;
    }
    const updated = await updateProviderManageTag(provider.id, currentID, payload);
    const currentModels = providerSelectedModels.value[provider.id] || [];
    let replaced = false;
    const nextModels = currentModels.map((model) => {
      if (model.model !== currentID) {
        return model;
      }
      replaced = true;
      return updated;
    });
    providerSelectedModels.value = {
      ...providerSelectedModels.value,
      [provider.id]: replaced ? nextModels : [...nextModels, updated],
    };
    notifyProvidersChanged();
    providerModelEditSaving.value = false;
    closeProviderModelEditDialog();
  } catch (err: any) {
    providerModelEditError.value = err?.message || t("settings.providerModelUpdateFailed");
  } finally {
    if (providerModelEditSaving.value) {
      providerModelEditSaving.value = false;
    }
  }
}

function providerConfiguredHeaderModel(provider: ManagedProvider): string {
  return provider.headers?.find((header) => header.name.trim().toLowerCase() === "x-model")?.value.trim() || "";
}

async function loadProviderDialogModels(provider: ManagedProvider, defaultModelID = "") {
  providerModelsLoading.value = true;
  providerModelsError.value = "";
  try {
    const [catalog, selected] = await Promise.all([
      getProviderManageTags(provider.id),
			getProviderSelectedTags(provider.id),
    ]);
		const selectedIDs = new Set(selected.map((model) => model.origin || model.model));
    const defaultNames = Object.fromEntries(catalog.map((model) => [model.model, defaultProviderModelDisplayName(model)] as const));
    const selectedDisplayNames = Object.fromEntries(selected.flatMap((model) => {
      const displayName = defaultProviderModelDisplayName(model).trim();
      const defaultName = (defaultNames[model.model] || model.model).trim();
      return displayName && displayName !== defaultName ? [[model.model, displayName] as const] : [];
    }));
    providerModelCatalog.value = catalog;
    providerModelSelected.value = Object.fromEntries(
      catalog.map((model) => [model.model, selectedIDs.has(model.model) || model.model === defaultModelID]),
    );
    providerModelDisplayNames.value = selectedDisplayNames;
  } catch (err: any) {
    providerModelCatalog.value = [];
    providerModelSelected.value = {};
    providerModelDisplayNames.value = {};
    providerModelsError.value = err?.message || t("settings.providerModelsLoadFailed");
  } finally {
    providerModelsLoading.value = false;
  }
}

async function testProviderForm() {
  const name = providerFormName.value.trim();
  const baseURL = providerFormBaseURL.value.trim();
  const apiKey = providerFormAPIKey.value.trim();
  const headers = providerFormHeaders.value
    .map((header) => ({ name: header.name.trim(), value: header.value.trim() }))
    .filter((header) => header.name && header.value);
  if (!name || !baseURL) {
    providerFormError.value = t("settings.providerNameURLRequired");
    return;
  }

  providerFormTesting.value = true;
  providerFormError.value = "";
  providerFormTestSuccess.value = "";
  try {
    await validateProvider({
      id: editingProvider.value?.id,
      name,
      base_url: baseURL,
      api_key: apiKey || undefined,
      provider: providerFormType.value.trim() || "openai",
      enabled: providerFormEnabled.value,
      headers,
      probe: true,
    });
    providerFormTestSuccess.value = t("settings.providerTestSucceeded");
  } catch (err: any) {
    providerFormError.value = err?.message || t("settings.providerTestFailed");
  } finally {
    providerFormTesting.value = false;
  }
}

async function saveProviderForm() {
  const name = providerFormName.value.trim();
  const baseURL = providerFormBaseURL.value.trim();
  const apiKey = providerFormAPIKey.value.trim();
  const providerType = providerFormType.value.trim() || "openai";
  const enabled = providerFormEnabled.value;
  const headers = providerFormHeaders.value
    .map((header) => ({ name: header.name.trim(), value: header.value.trim() }))
    .filter((header) => header.name && header.value);

  if (!name || !baseURL) {
    providerFormError.value = t("settings.providerNameURLRequired");
    return;
  }

  providerFormSaving.value = true;
  providerFormError.value = "";
  providerFormTestSuccess.value = "";
  try {
    await validateProvider({
      id: editingProvider.value?.id,
      name,
      base_url: baseURL,
      api_key: apiKey || undefined,
      provider: providerType,
      enabled,
      headers,
    });
    let savedProvider: ThirdPartyProvider;
    if (editingProvider.value) {
      savedProvider = await updateProvider(editingProvider.value.id, {
        name,
        base_url: baseURL,
        api_key: apiKey || undefined,
        provider: providerType,
        enabled,
        headers,
      });
    } else {
      savedProvider = await createProvider({
        name,
        base_url: baseURL,
        api_key: apiKey,
        provider: providerType,
        enabled,
        headers,
      });
    }
    await fetchProviderOptions();
    notifyProvidersChanged();
    editingProvider.value = savedProvider;
    providerModelTarget.value = savedProvider;
    providerDialogStep.value = "models";
    await loadProviderDialogModels(savedProvider, providerConfiguredHeaderModel(savedProvider));
  } catch (err: any) {
    providerFormError.value = err?.message || t("settings.providerSaveFailed");
  } finally {
    providerFormSaving.value = false;
  }
}

async function saveProviderModels() {
  const provider = providerModelTarget.value;
  if (!provider) return;
  providerModelsSaving.value = true;
  providerModelsError.value = "";
  try {
    const selected: ProviderTagModelSelection[] = providerModelCatalog.value
      .filter((model) => providerModelSelected.value[model.model])
      .map((model) => ({
        model: model.model,
        display_name: (providerModelDisplayNames.value[model.model] || "").trim() || undefined,
      }));
    await replaceProviderManageTags(provider.id, selected);
    await fetchProviderOptions();
    notifyProvidersChanged();
    isProviderDialogOpen.value = false;
    editingProvider.value = null;
    providerModelTarget.value = null;
    providerDialogStep.value = "details";
  } catch (err: any) {
    providerModelsError.value = err?.message || t("settings.providerModelsSaveFailed");
  } finally {
    providerModelsSaving.value = false;
  }
}

function toggleProviderModel(modelID: string, checked: boolean) {
  providerModelSelected.value = {
    ...providerModelSelected.value,
    [modelID]: checked,
  };
}

function selectAllProviderModels() {
  providerModelSelected.value = {
    ...providerModelSelected.value,
    ...Object.fromEntries(providerModelCatalog.value.map((model) => [model.model, true])),
  };
}

function invertProviderModels() {
  providerModelSelected.value = {
    ...providerModelSelected.value,
    ...Object.fromEntries(providerModelCatalog.value.map((model) => [model.model, !providerModelSelected.value[model.model]])),
  };
}

function changeProviderModelDisplayName(modelID: string, value: string) {
  providerModelDisplayNames.value = {
    ...providerModelDisplayNames.value,
    [modelID]: value,
  };
}

async function toggleProviderEnabled(provider: ThirdPartyProvider) {
  providersError.value = "";
  try {
    await updateProvider(provider.id, { enabled: !provider.enabled });
    providers.value = providers.value.map((p) =>
      p.id === provider.id ? { ...p, enabled: !p.enabled } : p
    );
    notifyProvidersChanged();
  } catch (err: any) {
    providersError.value = err?.message || t("settings.providerSaveFailed");
  }
}

async function removeProvider(provider: ThirdPartyProvider) {
  if (!confirm(t("settings.providerDeleteConfirm", provider.name))) return;
  providersError.value = "";
  try {
    await deleteProvider(provider.id);
    providers.value = providers.value.filter((item) => item.id !== provider.id);
    notifyProvidersChanged();
  } catch (err: any) {
    providersError.value = err?.message || t("settings.providerDeleteFailed");
  }
}

// sortProviderPoolMembers orders members by priority ascending (lower priority
// is tried first, matching the backend's orderedMembers() call order), then by
// weight descending within the same priority. The weight tiebreaker is purely
// visual — within a priority, members are load-split by weight ratio, not call
// order — but listing higher-weight members first matches the intuition that
// they carry more of the traffic.
function sortProviderPoolMembers(members: ProviderPoolMember[]): ProviderPoolMember[] {
  return [...members].sort((a, b) => {
    const pa = a.priority ?? 0;
    const pb = b.priority ?? 0;
    if (pa !== pb) return pa - pb;
    return (b.weight ?? 100) - (a.weight ?? 100);
  });
}

function newProviderPoolMember(index = providerPoolFormMembers.value.length): ProviderPoolMember {
  return {
    id: `member-${Date.now()}-${index}`,
    source: "local",
    model: "",
    priority: 0,
    weight: 100,
    requests_per_minute: 0,
    tokens_per_minute: 0,
    max_concurrent: 0,
  };
}

function providerPoolPolicyLabel(policy: string): string {
  const capabilityLabel = providerPoolPolicies.value.find((item) => item.type === policy)?.label?.trim();
  if (capabilityLabel) return capabilityLabel;
  if (policy === "priority_weight") return t("settings.providerPoolPolicyPriority");
  if (policy === "semantic") return t("settings.providerPoolPolicySemantic");
  return policy;
}

function providerPoolPolicyReason(reason?: string): string {
  switch (reason) {
    case "opencsg_login_required":
      return t("settings.providerPoolPolicyLoginRequired");
    case "required_embedding_model_unavailable":
      return t("settings.providerPoolPolicyEmbeddingUnavailable");
    case "gateway_catalog_unavailable":
      return t("settings.providerPoolPolicyCatalogUnavailable");
    default:
      return reason || t("settings.providerPoolPolicyUnavailable");
  }
}

function providerPoolPolicyHardUnavailable(policy?: ProviderPoolPolicy): boolean {
  return policy?.available === false && policy.reason !== "opencsg_login_required";
}

function providerPoolFormPolicyUnavailable(): boolean {
  const policy = providerPoolFormPolicy.value;
  if (
    editingProviderPool.value?.policy === policy
    && editingProviderPool.value.policy_available === false
    && editingProviderPool.value.policy_unavailable_reason !== "opencsg_login_required"
  ) {
    return true;
  }
  return providerPoolPolicyHardUnavailable(providerPoolPolicies.value.find((item) => item.type === policy));
}

function openProviderPoolDialog(pool?: ProviderPool) {
  editingProviderPool.value = pool || null;
  providerPoolFormName.value = pool?.name || "";
  providerPoolFormModel.value = pool?.model || "";
  providerPoolFormEnabled.value = pool?.enabled ?? true;
  providerPoolFormPolicy.value = pool?.policy || "priority_weight";
  providerPoolFormMembers.value = sortProviderPoolMembers(pool?.members.map((member) => ({ ...member })) || []);
  providerPoolFormError.value = "";
  providerPoolDialogStep.value = "basics";
  providerPoolSourceFilter.value = "local";
  providerPoolModelSearch.value = "";
  isProviderPoolDialogOpen.value = true;
}

function closeProviderPoolDialog() {
  if (providerPoolFormSaving.value) return;
  isProviderPoolDialogOpen.value = false;
  editingProviderPool.value = null;
  providerPoolFormError.value = "";
  providerPoolDialogStep.value = "basics";
  providerPoolModelSearch.value = "";
  closeProviderPoolMemberConfigDialog();
}

function continueProviderPoolDialog() {
  if (!providerPoolFormName.value.trim() || !providerPoolFormModel.value.trim()) {
    providerPoolFormError.value = t("settings.providerPoolNameModelRequired");
    return;
  }
  if (
    providerPoolFormPolicy.value === "semantic"
    && cloudAuth.value?.authenticated !== true
    && cloudAuth.value?.has_api_key !== true
  ) {
    providerPoolFormError.value = t("settings.providerPoolPolicyLoginRequired");
    return;
  }
  if (providerPoolFormPolicyUnavailable()) {
    providerPoolFormError.value = t("settings.providerPoolPolicyUnavailable");
    return;
  }
  providerPoolFormError.value = "";
  providerPoolDialogStep.value = "members";
}

function backProviderPoolDialog() {
  providerPoolFormError.value = "";
  providerPoolDialogStep.value = "basics";
}

function updateProviderPoolMember(index: number, patch: Partial<ProviderPoolMember>) {
  providerPoolFormMembers.value = providerPoolFormMembers.value.map((member, memberIndex) =>
    memberIndex === index ? { ...member, ...patch } : member
  );
}

function openProviderPoolMemberConfigDialog(index: number) {
  const member = providerPoolFormMembers.value[index];
  if (!member) return;
  providerPoolMemberConfigIndex.value = index;
  providerPoolMemberConfigDraft.value = {
    ...member,
    priority: member.priority ?? 0,
    weight: member.weight ?? 100,
    requests_per_minute: member.requests_per_minute ?? 0,
    tokens_per_minute: member.tokens_per_minute ?? 0,
    max_concurrent: member.max_concurrent ?? 0,
  };
}

function closeProviderPoolMemberConfigDialog() {
  providerPoolMemberConfigIndex.value = null;
  providerPoolMemberConfigDraft.value = null;
}

function saveProviderPoolMemberConfigDialog() {
  const index = providerPoolMemberConfigIndex.value;
  const member = providerPoolMemberConfigDraft.value;
  if (index === null || !member) return;
  updateProviderPoolMember(index, {
    priority: member.priority ?? 0,
    weight: member.weight ?? 100,
    requests_per_minute: member.requests_per_minute ?? 0,
    tokens_per_minute: member.tokens_per_minute ?? 0,
    max_concurrent: member.max_concurrent ?? 0,
  });
  // Re-sort so the list reflects the updated call priority (priority ascending,
  // matching the backend's orderedMembers() order).
  providerPoolFormMembers.value = sortProviderPoolMembers(providerPoolFormMembers.value);
  closeProviderPoolMemberConfigDialog();
}

function updateProviderPoolMemberConfigDraft(
  key: "priority" | "weight" | "requests_per_minute" | "tokens_per_minute" | "max_concurrent",
  value: string,
) {
  const member = providerPoolMemberConfigDraft.value;
  if (!member) return;
  const number = Number.parseInt(value, 10);
  providerPoolMemberConfigDraft.value = {
    ...member,
    [key]: Number.isFinite(number) ? number : undefined,
  };
}

function toggleProviderPoolSourceModel(source: string, model: string, checked: boolean) {
  const memberIndex = providerPoolFormMembers.value.findIndex(
    (member) => member.source === source && member.model === model
  );
  if (checked && memberIndex === -1) {
    providerPoolFormMembers.value = sortProviderPoolMembers([
      ...providerPoolFormMembers.value,
      { ...newProviderPoolMember(providerPoolFormMembers.value.length), source, model },
    ]);
  } else if (!checked && memberIndex !== -1) {
    providerPoolFormMembers.value = providerPoolFormMembers.value.filter((_, index) => index !== memberIndex);
  }
}

function providerPoolSourceLabel(source: string): string {
  const value = source.trim();
  if (value === "local") return t("settings.providerPoolSourceLocal");
  if (value === "cloud") return cloudManagedProvider.value?.name || t("settings.providerPoolSourceCloud");
  if (value.startsWith("provider:")) {
    const providerID = value.slice("provider:".length);
    return providers.value.find((provider) => provider.id === providerID)?.name || value;
  }
  return value;
}

async function saveProviderPoolForm() {
  const name = providerPoolFormName.value.trim();
  const model = providerPoolFormModel.value.trim();
  const members = providerPoolFormMembers.value.map((member) => ({
    ...member,
    id: member.id.trim(),
    source: member.source.trim(),
    model: member.model.trim(),
  }));
  if (!name || !model) {
    providerPoolFormError.value = t("settings.providerPoolNameModelRequired");
    return;
  }
  if (members.length === 0 || members.some((member) => !member.id || !member.source || !member.model)) {
    providerPoolFormError.value = t("settings.providerPoolMemberRequired");
    return;
  }
  if (
    providerPoolFormPolicy.value === "semantic"
    && cloudAuth.value?.authenticated !== true
    && cloudAuth.value?.has_api_key !== true
  ) {
    providerPoolFormError.value = t("settings.providerPoolPolicyLoginRequired");
    return;
  }
  if (providerPoolFormPolicyUnavailable()) {
    providerPoolFormError.value = t("settings.providerPoolPolicyUnavailable");
    return;
  }

  providerPoolFormSaving.value = true;
  providerPoolFormError.value = "";
  try {
    const payload = {
      name,
      model,
      enabled: providerPoolFormEnabled.value,
      policy: providerPoolFormPolicy.value,
      members,
    };
    if (editingProviderPool.value) {
      await updateProviderPool(editingProviderPool.value.id, payload);
    } else {
      await createProviderPool(payload);
    }
    await fetchProviderOptions();
    notifyProvidersChanged();
    providerPoolFormSaving.value = false;
    closeProviderPoolDialog();
  } catch (err: any) {
    providerPoolFormError.value = err?.message || t("settings.providerPoolSaveFailed");
  } finally {
    providerPoolFormSaving.value = false;
  }
}

async function removeProviderPool(pool: ProviderPool) {
  if (!confirm(t("settings.providerPoolDeleteConfirm", pool.name))) return;
  providersError.value = "";
  try {
    await deleteProviderPool(pool.id);
    providerPools.value = providerPools.value.filter((item) => item.id !== pool.id);
    notifyProvidersChanged();
  } catch (err: any) {
    providersError.value = err?.message || t("settings.providerPoolDeleteFailed");
  }
}

function selectLocalAPIUsagePeriod(period: UsagePeriod) {
  localAPIUsagePeriod.value = period;
  void fetchLocalAPIUsage(period);
}

function selectLocalAPIUsageProvider(provider: string) {
  localAPIUsageProvider.value = provider;
  void fetchLocalAPIUsage(localAPIUsagePeriod.value, provider);
}

function copySnippet(value: string) {
  void navigator.clipboard?.writeText(value);
  copiedSnippet.value = value;
  window.setTimeout(() => {
    if (copiedSnippet.value === value) {
      copiedSnippet.value = "";
    }
  }, 1500);
}

function hasManualCloudAPIKey(status: CloudAuthStatus | null | undefined): boolean {
  return status?.api_key_source === "manual";
}

function cloudAPIKeyStatus(status: CloudAuthStatus | null | undefined): string {
  if (!status) return "...";
  const suffix = status.api_key_prefix ? ` (${status.api_key_prefix})` : "";
  if (status.api_key_source === "manual") {
    return t("settings.cloudAPIKeyManualStatus", suffix);
  }
  if (status.api_key_source === "builtin") {
    return t("settings.cloudAPIKeyBuiltinStatus", suffix);
  }
  return t("settings.cloudAPIKeyMissingStatus");
}

function configuredCloudProviderName(): string {
  return cloudProviderName.value || t("chat.cloud");
}

export function AIGateway() {
  void locale.value;
  const runtimeAPIOrigin = useRuntimeAPIOrigin();

  useEffect(() => {
    void fetchLocalAPIKeys();
    void fetchLocalAPIUsage();
    fetchCloudAuth();
    fetchCloudSettings();
    void fetchProviderOptions();
    void fetchProviderPoolPolicies();
    const poll = window.setInterval(() => {
      const running = providerPools.value.filter((pool) => pool.policy === "semantic").map((pool) => pool.id);
      const openPool = providerPoolRouterDialogPool.value;
      const openJob = providerPoolRouterJob.value;
      if (openPool && openJob && (openJob.status === "queued" || openJob.status === "running") && !running.includes(openPool.id)) {
        running.push(openPool.id);
      }
      for (const poolID of running) {
        void getProviderPoolRouterStatus(poolID).then(async (status) => {
          providerPoolRouterStatuses.value = { ...providerPoolRouterStatuses.value, [poolID]: status };
          if (providerPoolRouterDialogPool.value?.id === poolID) {
            const nextJob = status.running_job || status.latest_job;
            if (nextJob) providerPoolRouterJob.value = nextJob;
          }
        }).catch(() => undefined);
      }
    }, 2000);
    return () => window.clearInterval(poll);
  }, []);

  return (
    <div class="page-shell">
      <div class="mb-6 overflow-hidden rounded-3xl border border-indigo-100 bg-gradient-to-br from-indigo-50 via-white to-sky-50 p-7">
        <div class="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <p class="text-xs font-semibold uppercase tracking-[0.28em] text-indigo-500">{t("gateway.eyebrow")}</p>
            <h1 class="mt-3 text-3xl font-bold tracking-tight text-gray-950">{t("gateway.title")}</h1>
            <p class="mt-2 max-w-2xl text-sm leading-6 text-gray-600">{t("gateway.subtitle")}</p>
          </div>
          <GatewaySnapshot />
        </div>
      </div>

      <div class="mb-6 inline-flex rounded-2xl border border-gray-200 bg-white p-1 shadow-sm">
        <GatewayTabButton tab="apiKeys" label={t("settings.tabAPIKeys")} />
        <GatewayTabButton tab="providers" label={t("gateway.tabProviders")} />
        <GatewayTabButton tab="pools" label={t("settings.providerPools")} />
        <GatewayTabButton tab="usage" label={t("settings.tabUsage")} />
      </div>

      {activeGatewayTab.value === "apiKeys" && <APIKeysSection />}
      {activeGatewayTab.value === "providers" && <ProvidersSection />}
      {activeGatewayTab.value === "pools" && <ProviderPoolsSection />}
      {activeGatewayTab.value === "usage" && <UsageStatisticsSection />}
      <LocalAPIKeyDialog
        open={isLocalAPIKeyDialogOpen.value}
        name={localAPIKeyName.value}
        createdKey={localAPIKeyCreated.value}
        error={localAPIKeysError.value}
        saving={localAPIKeySaving.value}
        onClose={closeLocalAPIKeyDialog}
        onSave={() => void createLocalKey()}
        onChangeName={(value) => (localAPIKeyName.value = value)}
      />
      <ProviderDialog
        open={isProviderDialogOpen.value}
        editing={!!editingProvider.value}
        step={providerDialogStep.value}
        name={providerFormName.value}
        baseURL={providerFormBaseURL.value}
        apiKey={providerFormAPIKey.value}
        headers={providerFormHeaders.value}
        providerType={providerFormType.value}
        enabled={providerFormEnabled.value}
        error={providerFormError.value}
        saving={providerFormSaving.value}
        testing={providerFormTesting.value}
        testSuccess={providerFormTestSuccess.value}
        modelTarget={providerModelTarget.value}
        modelCatalog={providerModelCatalog.value}
        modelSelected={providerModelSelected.value}
        modelDisplayNames={providerModelDisplayNames.value}
        modelsLoading={providerModelsLoading.value}
        modelsSaving={providerModelsSaving.value}
        modelsError={providerModelsError.value}
        onClose={closeProviderDialog}
        onTest={() => void testProviderForm()}
        onSave={() => void saveProviderForm()}
        onSaveModels={() => void saveProviderModels()}
        onToggleModel={toggleProviderModel}
        onSelectAllModels={selectAllProviderModels}
        onInvertModels={invertProviderModels}
        onChangeModelDisplayName={changeProviderModelDisplayName}
        onChangeName={(value) => (providerFormName.value = value)}
        onChangeBaseURL={(value) => (providerFormBaseURL.value = value)}
        onChangeAPIKey={(value) => (providerFormAPIKey.value = value)}
        onChangeHeaders={(headers) => (providerFormHeaders.value = headers)}
        onChangeProviderType={(value) => {
          providerFormType.value = value;
          const option = providerTypes.find((item) => item.value === value);
          if (option) {
            if (option.name) providerFormName.value = option.name;
            if (option.baseURL) providerFormBaseURL.value = option.baseURL;
          }
        }}
        onChangeEnabled={(value) => (providerFormEnabled.value = value)}
      />
      <ProviderModelEditDialog
        open={isProviderModelEditOpen.value}
        modelID={providerModelEditID.value}
        displayName={providerModelEditDisplayName.value}
        description={providerModelEditDescription.value}
        displayNamePlaceholder={providerModelEditPlaceholder.value}
        error={providerModelEditError.value}
        saving={providerModelEditSaving.value}
        onClose={closeProviderModelEditDialog}
        onSave={() => void saveProviderModelEdit()}
        onChangeModelID={(value) => (providerModelEditID.value = value)}
        onChangeDisplayName={(value) => (providerModelEditDisplayName.value = value)}
        onChangeDescription={(value) => (providerModelEditDescription.value = value)}
      />
      <ProviderPoolDialog
        open={isProviderPoolDialogOpen.value}
        editing={!!editingProviderPool.value}
        step={providerPoolDialogStep.value}
        name={providerPoolFormName.value}
        model={providerPoolFormModel.value}
        policy={providerPoolFormPolicy.value}
        enabled={providerPoolFormEnabled.value}
        members={providerPoolFormMembers.value}
        models={providerPoolModels.value}
        error={providerPoolFormError.value}
        saving={providerPoolFormSaving.value}
        policies={providerPoolPolicies.value}
        policiesLoading={providerPoolPoliciesLoading.value}
        policiesError={providerPoolPoliciesError.value}
        cloudCredentialAvailable={cloudAuth.value?.authenticated === true || cloudAuth.value?.has_api_key === true}
        onClose={closeProviderPoolDialog}
        onNext={continueProviderPoolDialog}
        onBack={backProviderPoolDialog}
        onSave={() => void saveProviderPoolForm()}
        onChangeName={(value) => (providerPoolFormName.value = value)}
        onChangeModel={(value) => (providerPoolFormModel.value = value)}
        onChangePolicy={(value) => (providerPoolFormPolicy.value = value)}
        onChangeEnabled={(value) => (providerPoolFormEnabled.value = value)}
        onToggleSourceModel={toggleProviderPoolSourceModel}
      />
      <ProviderPoolMemberConfigDialog
        open={providerPoolMemberConfigIndex.value !== null}
        member={providerPoolMemberConfigDraft.value}
        saving={providerPoolFormSaving.value}
        onClose={closeProviderPoolMemberConfigDialog}
        onSave={saveProviderPoolMemberConfigDialog}
        onChange={updateProviderPoolMemberConfigDraft}
      />
      <RouterProfileDialog />
      {gatewayAPIInfoTarget.value && (
        <ApiInfoDialog
          baseUrl={`${runtimeAPIOrigin.replace(/\/+$/, "")}/providers/${encodeURIComponent(gatewayAPIInfoTarget.value.targetID)}`}
          model={gatewayAPIInfoTarget.value.model.model}
          pipelineTag={gatewayAPIInfoTarget.value.model.pipeline_tag}
          isVision={isGatewayVisionModel(gatewayAPIInfoTarget.value.model)}
          isEmbedding={isGatewayEmbeddingModel(gatewayAPIInfoTarget.value.model)}
          isASR={isGatewayASRModel(gatewayAPIInfoTarget.value.model)}
          onClose={() => (gatewayAPIInfoTarget.value = null)}
        />
      )}
    </div>
  );
}

function GatewaySnapshot() {
  const authEnabled = localAPIKeys.value?.auth_enabled || false;
  const keyCount = localAPIKeys.value?.keys.length || 0;
  return (
    <div class="grid min-w-[18rem] grid-cols-3 gap-3 rounded-2xl border border-white/80 bg-white/80 p-3 shadow-sm backdrop-blur">
      <SnapshotItem label={t("gateway.snapshotAuth")} value={authEnabled ? t("settings.localAPIAuthOn") : t("settings.localAPIAuthOff")} />
      <SnapshotItem label={t("gateway.snapshotKeys")} value={formatNumber(keyCount)} />
      <SnapshotItem label={t("gateway.snapshotProtocols")} value="2" />
    </div>
  );
}

function SnapshotItem({ label, value }: { label: string; value: string }) {
  return (
    <div class="rounded-xl bg-gray-50 px-3 py-2">
      <p class="text-[11px] font-medium text-gray-400">{label}</p>
      <p class="mt-1 truncate text-sm font-semibold text-gray-900">{value}</p>
    </div>
  );
}

function GatewayTabButton({ tab, label }: { tab: GatewayTab; label: string }) {
  const active = activeGatewayTab.value === tab;
  return (
    <button
      type="button"
      onClick={() => (activeGatewayTab.value = tab)}
      class={`rounded-xl px-5 py-2 text-sm font-medium transition-colors ${
        active
          ? "bg-indigo-600 text-white shadow-sm"
          : "text-gray-600 hover:bg-gray-50 hover:text-gray-900"
      }`}
    >
      {label}
    </button>
  );
}

function APIKeysSection() {
  return (
    <div class="space-y-6">
      <LocalAPIKeysSection />
      <CloudAPIKeySection />
    </div>
  );
}

function CloudAPIKeySection() {
  return (
    <section class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm">
      <div class="flex flex-col gap-5 lg:flex-row lg:items-start lg:justify-between">
        <div class="max-w-2xl">
          <div class="inline-flex rounded-full bg-violet-50 px-3 py-1 text-xs font-medium text-violet-700">
            {t("gateway.cloudKeyBadge", configuredCloudProviderName())}
          </div>
          <h2 class="mt-3 text-base font-semibold text-gray-900">{t("settings.cloudAPIKey", configuredCloudProviderName())}</h2>
          <p class="mt-1 text-sm leading-6 text-gray-500">{t("settings.cloudAPIKeyDesc", configuredCloudProviderName())}</p>
        </div>
        <button
          type="button"
          onClick={() => openExternal("https://opencsg.com/settings/api-keys")}
          class="rounded-xl border border-gray-200 px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50"
        >
          {t("settings.openAPIKeyPage")}
        </button>
      </div>
      <div class="mt-5 grid gap-4 lg:grid-cols-[0.85fr_1.15fr]">
        <div class="rounded-xl border border-gray-100 bg-gray-50 p-4">
          <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{t("chat.cloudGatewayLabel")}</p>
          <p class="mt-1 text-sm font-semibold text-gray-900">{cloudGatewayURL.value || t("chat.cloudGatewayValue")}</p>
          <div class="mt-4 rounded-lg bg-white px-3 py-2">
            <p class="text-xs font-medium text-gray-400">{t("gateway.authStatus")}</p>
            <p class="mt-1 text-sm font-semibold text-gray-900">{cloudAPIKeyStatus(cloudAuth.value)}</p>
          </div>
          {hasManualCloudAPIKey(cloudAuth.value) && (
            <button
              type="button"
              onClick={() => void clearCloudAPIKeyForm()}
              disabled={isClearingCloudAPIKey.value}
              class="mt-4 rounded-lg border border-red-200 px-3 py-2 text-sm text-red-600 transition-colors hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-60"
            >
              {isClearingCloudAPIKey.value ? t("settings.clearingAPIKey") : t("settings.clearAPIKey")}
            </button>
          )}
        </div>
        <div class="rounded-xl border border-gray-100 p-4">
          <label class="mb-2 block text-sm font-medium text-gray-700">{t("chat.cloudApiKeyLabel")}</label>
          <p class="mb-3 text-sm text-gray-500">{t("settings.cloudAPIKeyInputHint")}</p>
          <div class="flex flex-col gap-3 sm:flex-row sm:items-end">
            <div class="flex-1">
              <input
                type="password"
                autoComplete="off"
                spellcheck={false}
                class="w-full rounded-xl border border-gray-200 px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder={t("chat.cloudApiKeyPlaceholder")}
                value={cloudAPIKeyInput.value}
                onInput={(e) => (cloudAPIKeyInput.value = (e.target as HTMLInputElement).value)}
              />
            </div>
            <button
              type="button"
              onClick={() => void saveCloudAPIKeyForm()}
              disabled={isSavingCloudAPIKey.value}
              class="rounded-xl bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white shadow-sm transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-60"
            >
              {isSavingCloudAPIKey.value ? t("chat.cloudApiKeySaving") : t("chat.cloudApiKeySave")}
            </button>
          </div>
          {cloudAuth.value?.api_key_error && (
            <p class="mt-3 text-sm text-amber-700">{cloudAuth.value.api_key_error}</p>
          )}
          {cloudAPIKeyError.value && (
            <p class="mt-3 text-sm text-red-600">{cloudAPIKeyError.value}</p>
          )}
        </div>
      </div>
    </section>
  );
}

function LocalAPIKeysSection() {
  const keys = localAPIKeys.value?.keys || [];
  const authEnabled = localAPIKeys.value?.auth_enabled || false;
  const origin = useRuntimeAPIOrigin();
  const openAIBaseURL = `${origin}/v1`;
  const anthropicBaseURL = `${origin}/anthropic`;
  const openAICurl = `curl ${openAIBaseURL}/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer <API_KEY>" \\
  -d '{"model":"<MODEL>","messages":[{"role":"user","content":"Hello"}]}'`;
  const anthropicCurl = `curl ${anthropicBaseURL}/v1/messages \\
  -H "Content-Type: application/json" \\
  -H "x-api-key: <API_KEY>" \\
  -d '{"model":"<MODEL>","max_tokens":1024,"messages":[{"role":"user","content":"Hello"}]}'`;

  return (
    <div class="space-y-6">
      <div>
        <section class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm">
          <div class="flex items-start justify-between gap-4">
            <div>
              <p class="text-sm font-semibold text-gray-900">{t("settings.localAPIAuth")}</p>
              <p class="mt-1 text-sm leading-6 text-gray-500">{t("settings.localAPIAuthDesc")}</p>
            </div>
            <label class="relative inline-flex cursor-pointer items-center">
              <input
                type="checkbox"
                checked={authEnabled}
                disabled={localAPIKeySaving.value}
                onChange={(e) => void toggleLocalAPIAuth((e.target as HTMLInputElement).checked)}
                class="peer sr-only"
              />
              <div class="h-6 w-11 rounded-full bg-gray-200 transition-all after:absolute after:left-[2px] after:top-[2px] after:h-5 after:w-5 after:rounded-full after:border after:border-gray-300 after:bg-white after:shadow-sm after:transition-all after:content-[''] peer-checked:bg-indigo-600 peer-checked:after:translate-x-full peer-checked:after:border-white peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-indigo-300 peer-disabled:cursor-not-allowed peer-disabled:opacity-60" />
            </label>
          </div>
          <div class="mt-5 rounded-xl border border-gray-100 bg-gray-50 px-4 py-3">
            <p class="text-xs font-medium uppercase tracking-wide text-gray-400">{t("gateway.authStatus")}</p>
            <p class={`mt-1 text-sm font-semibold ${authEnabled ? "text-indigo-700" : "text-gray-700"}`}>
              {authEnabled ? t("settings.localAPIAuthOn") : t("settings.localAPIAuthOff")}
            </p>
          </div>
        </section>
      </div>

      <section class="rounded-2xl border border-gray-200 bg-white shadow-sm">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4">
          <div>
            <h2 class="text-sm font-semibold text-gray-900">{t("gateway.keyListTitle")}</h2>
            <p class="mt-1 text-sm text-gray-500">{t("gateway.keyListDesc")}</p>
          </div>
          <div class="flex items-center gap-2">
            <span class="rounded-full bg-indigo-50 px-3 py-1 text-xs font-medium text-indigo-700">
              {t("gateway.keyCount", formatNumber(keys.length))}
            </span>
            <button
              type="button"
              onClick={openLocalAPIKeyDialog}
              class="rounded-lg bg-indigo-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-indigo-700"
            >
              {t("settings.localAPIKeyAdd")}
            </button>
          </div>
        </div>
        {localAPIKeysError.value && !isLocalAPIKeyDialogOpen.value && <p class="px-5 pt-4 text-sm text-red-600">{localAPIKeysError.value}</p>}
        {localAPIKeysLoading.value ? (
          <p class="p-5 text-sm text-gray-500">...</p>
        ) : keys.length === 0 ? (
          <p class="p-5 text-sm text-gray-400">{t("settings.localAPIKeysEmpty")}</p>
        ) : (
          <div class="divide-y divide-gray-100">
            {keys.map((key) => (
              <div key={key.id} class="flex flex-col gap-4 px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
                <div class="min-w-0">
                  <p class="truncate text-sm font-semibold text-gray-900">{key.name}</p>
                  <div class="mt-2 flex flex-wrap gap-2 text-xs">
                    <span class="rounded-full bg-gray-100 px-2.5 py-1 font-mono text-gray-600">{key.prefix}</span>
                    <span class="rounded-full bg-gray-50 px-2.5 py-1 text-gray-500">{t("settings.localAPIKeyLastUsed", formatDateTime(key.last_used_at))}</span>
                  </div>
                </div>
                <button
                  type="button"
                  onClick={() => void removeLocalKey(key.id)}
                  disabled={localAPIKeyDeleting.value === key.id}
                  class="self-start rounded-lg border border-red-200 px-3 py-1.5 text-xs font-medium text-red-600 transition-colors hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-60 sm:self-center"
                >
                  {localAPIKeyDeleting.value === key.id ? "..." : t("settings.localAPIKeyDelete")}
                </button>
              </div>
            ))}
          </div>
        )}
      </section>

      <section class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm">
        <div class="flex flex-col gap-1">
          <h2 class="text-sm font-semibold text-gray-900">{t("settings.localAPIBaseURL")}</h2>
          <p class="text-sm text-gray-500">{t("settings.localAPIBaseURLDesc")}</p>
        </div>
        <div class="mt-5 grid gap-4 lg:grid-cols-2">
          <EndpointCard label={t("settings.localAPIBaseURLOpenAI")} value={openAIBaseURL} example={openAICurl} />
          <EndpointCard label={t("settings.localAPIBaseURLAnthropic")} value={anthropicBaseURL} example={anthropicCurl} />
        </div>
      </section>
    </div>
  );
}

function LocalAPIKeyDialog({
  open,
  name,
  createdKey,
  error,
  saving,
  onClose,
  onSave,
  onChangeName,
}: {
  open: boolean;
  name: string;
  createdKey: string;
  error: string;
  saving: boolean;
  onClose: () => void;
  onSave: () => void;
  onChangeName: (value: string) => void;
}) {
  if (!open) return null;
  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 px-4" onClick={onClose}>
      <div class="w-full max-w-md rounded-2xl bg-white shadow-2xl" onClick={(e) => e.stopPropagation()}>
        <div class="border-b border-gray-100 px-6 py-5">
          <h2 class="text-lg font-semibold text-gray-900">{t("gateway.createKeyTitle")}</h2>
          <p class="mt-1 text-sm text-gray-500">{t("settings.localAPIKeysDesc")}</p>
        </div>
        <div class="space-y-4 px-6 py-5">
          <div>
            <label class="mb-1 block text-sm font-medium text-gray-700">{t("settings.localAPIKeyName")}</label>
            <input
              class="w-full rounded-lg border border-gray-200 px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
              value={name}
              onInput={(e) => onChangeName((e.target as HTMLInputElement).value)}
              placeholder={t("settings.localAPIKeyNamePlaceholder")}
              disabled={saving || !!createdKey}
            />
          </div>
          {createdKey && (
            <div class="rounded-xl border border-amber-200 bg-amber-50 p-4">
              <p class="text-sm font-semibold text-amber-900">{t("settings.localAPIKeyCreated")}</p>
              <div class="mt-2 flex gap-2">
                <input
                  readOnly
                  class="min-w-0 flex-1 rounded-lg border border-amber-200 bg-white px-3 py-2 text-sm font-mono text-amber-900"
                  value={createdKey}
                  onFocus={(e) => (e.target as HTMLInputElement).select()}
                />
                <button
                  type="button"
                  onClick={() => copySnippet(createdKey)}
                  class="rounded-lg border border-amber-200 bg-white px-3 py-2 text-sm text-amber-900 hover:bg-amber-100"
                >
                  {copiedSnippet.value === createdKey ? t("settings.copied") : t("settings.copy")}
                </button>
              </div>
              <p class="mt-2 text-xs text-amber-800">{t("settings.localAPIKeyCreatedHint")}</p>
            </div>
          )}
          {error && <p class="text-sm text-red-600">{error}</p>}
        </div>
        <div class="flex justify-end gap-3 border-t border-gray-100 px-6 py-4">
          <button
            type="button"
            onClick={onClose}
            disabled={saving}
            class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-60"
          >
            {createdKey ? t("dash.close") : t("upgrade.cancel")}
          </button>
          {!createdKey && (
            <button
              type="button"
              onClick={onSave}
              disabled={saving}
              class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
            >
              {saving ? "..." : t("settings.localAPIKeyCreate")}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

function EndpointCard({ label, value, example }: { label: string; value: string; example: string }) {
  return (
    <div class="rounded-xl border border-gray-100 bg-gray-50 p-4">
      <div class="mb-2 flex items-center justify-between gap-3">
        <span class="text-xs font-semibold uppercase tracking-wide text-gray-400">{label}</span>
        <button
          type="button"
          onClick={() => copySnippet(value)}
          class="rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-xs text-gray-700 transition-colors hover:bg-gray-50"
        >
          {copiedSnippet.value === value ? t("settings.copied") : t("settings.copy")}
        </button>
      </div>
      <input
        readOnly
        class="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm font-mono text-gray-800"
        value={value}
        onFocus={(e) => (e.target as HTMLInputElement).select()}
      />
      <div class="mt-4 rounded-lg border border-gray-200 bg-white p-3">
        <div class="mb-2 flex items-center justify-between gap-3">
          <span class="text-xs font-medium text-gray-400">{t("settings.localAPIAccessMethod")}</span>
          <button
            type="button"
            onClick={() => copySnippet(example)}
            class="rounded-lg border border-gray-200 px-3 py-1.5 text-xs text-gray-700 transition-colors hover:bg-gray-50"
          >
            {copiedSnippet.value === example ? t("settings.copied") : t("settings.copy")}
          </button>
        </div>
        <pre class="overflow-x-auto whitespace-pre-wrap break-words text-xs leading-5 text-gray-700"><code>{example}</code></pre>
      </div>
    </div>
  );
}

function ProvidersSection() {
	const cards = providerCards();
  return (
    <section class="rounded-2xl border border-gray-200 bg-white shadow-sm">
      <div class="flex flex-col gap-4 border-b border-gray-100 px-5 py-5 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h2 class="text-base font-semibold text-gray-900">{t("settings.providers")}</h2>
          <p class="mt-1 text-sm text-gray-500">{t("settings.providersDesc")}</p>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <span class="rounded-full bg-indigo-50 px-3 py-1 text-xs font-medium text-indigo-700">
            {t("settings.providersConfigured", providers.value.length)}
          </span>
          <button
            type="button"
            onClick={() => openProviderDialog()}
            class="rounded-xl bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white shadow-sm transition-colors hover:bg-indigo-700"
          >
            {t("settings.providerAdd")}
          </button>
        </div>
      </div>
      {providersError.value && <p class="px-5 pt-4 text-sm text-red-600">{providersError.value}</p>}
      <div class="grid gap-4 p-5 lg:grid-cols-2">
        {providersLoading.value ? (
          <p class="text-sm text-gray-500">...</p>
				) : cards.length === 0 ? (
          <div class="rounded-2xl border border-dashed border-gray-200 p-6 text-center lg:col-span-2">
            <p class="text-sm font-medium text-gray-700">{t("settings.providersEmpty")}</p>
            <p class="mt-1 text-sm text-gray-500">{t("settings.providersHint")}</p>
          </div>
        ) : (
					cards.map((provider) => {
            const selectedModels = providerSelectedModels.value[provider.id] || [];
            return (
              <div key={provider.id} class="rounded-2xl border border-gray-100 bg-gray-50 p-4">
                <div class="flex items-start justify-between gap-3">
                  <div class="min-w-0">
                    <div class="flex min-w-0 flex-wrap items-center gap-2">
                      <h3 class="truncate text-sm font-semibold text-gray-900">{provider.name}</h3>
                      <span class={`rounded-full px-2 py-0.5 text-[11px] font-medium ${provider.enabled ? "bg-emerald-50 text-emerald-700" : "bg-gray-100 text-gray-500"}`}>
                        {provider.enabled ? t("settings.providerEnabled") : t("settings.providerDisabled")}
                      </span>
										{provider.builtIn && (
											<span class="rounded-full bg-sky-50 px-2 py-0.5 text-[11px] font-medium text-sky-700">
												{t("settings.providerBuiltIn")}
											</span>
										)}
                    </div>
                    <p class="mt-1 truncate text-xs text-gray-500">{provider.base_url}</p>
                    <p class="mt-1 text-[11px] uppercase tracking-wide text-gray-400">{provider.provider || "openai"}</p>
                  </div>
								{!provider.builtIn && (
									<label class="relative inline-flex shrink-0 cursor-pointer items-center">
										<input
											type="checkbox"
											checked={provider.enabled}
											onChange={() => void toggleProviderEnabled(provider)}
											class="peer sr-only"
										/>
										<div class="h-5 w-9 rounded-full bg-gray-200 transition-all after:absolute after:left-[2px] after:top-[2px] after:h-4 after:w-4 after:rounded-full after:border after:border-gray-300 after:bg-white after:transition-all after:content-[''] peer-checked:bg-indigo-600 peer-checked:after:translate-x-full peer-checked:after:border-white peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-indigo-300" />
									</label>
								)}
                </div>
                <div class="mt-4 rounded-xl bg-white p-3">
                  <div class="mb-2 flex items-center justify-between gap-2">
                    <span class="text-xs font-medium text-gray-400">{t("gateway.providerModels")}</span>
                    <span class="text-xs text-gray-400">{selectedModels.length}</span>
                  </div>
                  {selectedModels.length === 0 ? (
                    <span class="text-xs text-gray-400">{t("settings.providerModelsNoneSelected")}</span>
                  ) : (
                    <div class="grid max-h-40 gap-1.5 overflow-y-auto pr-1">
                      {selectedModels.map((model) => (
                        <div key={model.model} class="min-w-0 rounded-lg bg-indigo-50 px-2 py-1.5">
                          <div class="flex min-w-0 items-start justify-between gap-2">
                            <div class="min-w-0">
                              <p class="truncate text-xs font-medium text-indigo-700">{providerModelLabel(model)}</p>
                              <p class="truncate text-[11px] text-indigo-500">{model.model}</p>
                            </div>
                            <div class="flex shrink-0 items-center gap-1">
                              <button
                                type="button"
                                onClick={() => openGatewayAPIInfo(provider.id, model)}
                                title={t("settings.providerCallMethod")}
                                class="rounded border border-indigo-100 bg-white/70 px-2 py-1 text-[11px] text-indigo-700 transition-colors hover:bg-white"
                              >
                                {t("settings.providerCallMethod")}
                              </button>
                              <button
                                type="button"
                                onClick={() => openProviderModelEditDialog(provider, model)}
                                title={t("settings.providerModelEdit")}
                                aria-label={t("settings.providerModelEdit")}
                                class="rounded border border-indigo-100 bg-white/70 p-1 text-indigo-700 transition-colors hover:bg-white"
                              >
                                <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                                  <path stroke-linecap="round" stroke-linejoin="round" d="M16.862 4.487l1.687-1.688a1.875 1.875 0 112.652 2.652L10.582 16.07a4.5 4.5 0 01-1.897 1.13L6 18l.8-2.685a4.5 4.5 0 011.13-1.897l8.932-8.931z" />
                                  <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 7.125L16.875 4.5" />
                                </svg>
                              </button>
                            </div>
                          </div>
                          <div class="mt-1">
                            <ProviderModelModalityBadges model={model} showOutputs compact />
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
                <div class="mt-4 flex justify-end gap-2">
                  <button
                    type="button"
									onClick={() => openProviderModelsDialog(provider)}
                    class="rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-xs text-gray-700 transition-colors hover:bg-gray-50"
                  >
									{t("settings.providerModelsManage")}
                  </button>
								{!provider.builtIn && (
									<>
										<button
											type="button"
											onClick={() => openProviderDialog(provider)}
											class="rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-xs text-gray-700 transition-colors hover:bg-gray-50"
										>
											{t("settings.providerEdit")}
										</button>
										<button
											type="button"
											onClick={() => void removeProvider(provider)}
											class="rounded-lg border border-red-200 bg-white px-3 py-1.5 text-xs text-red-600 transition-colors hover:bg-red-50"
										>
											{t("settings.providerDelete")}
										</button>
									</>
								)}
                </div>
              </div>
            );
          })
        )}
      </div>
    </section>
  );
}

function ProviderPoolsSection() {
  const pools = providerPools.value;
  return (
    <section class="rounded-2xl border border-gray-200 bg-white shadow-sm">
      <div class="flex flex-wrap items-center justify-between gap-4 border-b border-gray-100 px-5 py-5">
        <div>
          <h2 class="text-base font-semibold text-gray-900">{t("settings.providerPools")}</h2>
          <p class="mt-1 text-sm text-gray-500">{t("settings.providerPoolsDesc")}</p>
        </div>
        <button
          type="button"
          onClick={() => openProviderPoolDialog()}
          class="rounded-xl bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white shadow-sm transition-colors hover:bg-indigo-700"
        >
          {t("settings.providerPoolAdd")}
        </button>
      </div>
      {providersLoading.value ? (
        <p class="p-5 text-sm text-gray-500">...</p>
      ) : pools.length === 0 ? (
        <div class="p-5 text-center">
          <p class="text-sm font-medium text-gray-700">{t("settings.providerPoolsEmpty")}</p>
          <p class="mt-1 text-sm text-gray-500">{t("settings.providerPoolsHint")}</p>
        </div>
      ) : (
        <div class="grid gap-4 p-5 lg:grid-cols-2">
          {pools.map((pool) => (
            <div key={pool.id} class="rounded-2xl border border-gray-100 bg-gray-50 p-4">
              <div class="flex items-start justify-between gap-3">
                <div class="min-w-0">
                  <div class="flex flex-wrap items-center gap-2">
                    <h3 class="truncate text-sm font-semibold text-gray-900">{pool.name}</h3>
                    <span class={`rounded-full px-2 py-0.5 text-[11px] font-medium ${pool.enabled ? "bg-emerald-50 text-emerald-700" : "bg-gray-100 text-gray-500"}`}>
                      {pool.enabled ? t("settings.providerEnabled") : t("settings.providerDisabled")}
                    </span>
                    <span class="rounded-full bg-sky-50 px-2 py-0.5 text-[11px] font-medium text-sky-700">
                      {providerPoolPolicyLabel(pool.policy || "priority_weight")}
                    </span>
                    {pool.policy === "semantic" && (
                      <span class="rounded-full bg-violet-50 px-2 py-0.5 text-[11px] font-medium text-violet-700">
                        {t("settings.providerPoolPolicyExperimental")}
                      </span>
                    )}
                  </div>
                  <p class="mt-1 truncate font-mono text-xs text-gray-500">{pool.model}</p>
                  {pool.policy_available === false && (
                    <p class="mt-1 text-xs text-amber-700">
                      {providerPoolPolicyReason(pool.policy_unavailable_reason)}
                    </p>
                  )}
                  {pool.policy === "semantic" && providerPoolRouterStatusLoading.value[pool.id] && (
                    <p class="mt-1 text-xs text-gray-400">{t("settings.routerStatusLoading")}</p>
                  )}
                </div>
                <span class="rounded-full bg-indigo-50 px-2.5 py-1 text-xs font-medium text-indigo-700">
                  {t("settings.providerPoolMembers", pool.members.length)}
                </span>
              </div>
              {pool.policy === "semantic" && providerPoolRouterStatuses.value[pool.id] && (
                <div class="mt-3 flex flex-wrap gap-2">
                  {providerPoolRouterStatuses.value[pool.id].pending_suggestion && (
                    <span class="rounded-full bg-amber-50 px-2 py-1 text-[11px] font-medium text-amber-700">
                      {t("settings.routerSuggestionBadge", providerPoolRouterStatuses.value[pool.id].pending_suggestion!.new_query_count)}
                    </span>
                  )}
                  {providerPoolRouterStatuses.value[pool.id].running_job && (
                    <span class="rounded-full bg-blue-50 px-2 py-1 text-[11px] font-medium text-blue-700">
                      {t("settings.routerRunningBadge", providerPoolRouterStatuses.value[pool.id].running_job!.current, providerPoolRouterStatuses.value[pool.id].running_job!.total)}
                    </span>
                  )}
                  {providerPoolRouterStatuses.value[pool.id].active_profile && (
                    <span class="rounded-full bg-emerald-50 px-2 py-1 text-[11px] font-medium text-emerald-700">
                      {t("settings.routerActiveBadge", providerPoolRouterStatuses.value[pool.id].active_profile!.version)}
                    </span>
                  )}
                  {providerPoolRouterStatuses.value[pool.id].latest_candidate_profile?.schema_version === 1 && !providerPoolRouterStatuses.value[pool.id].semantic_differentiation && (
                    <span class="rounded-full bg-red-50 px-2 py-1 text-[11px] font-medium text-red-700">
                      {t("settings.routerNoDifferentiationBadge")}
                    </span>
                  )}
                </div>
              )}
              <div class="mt-4 space-y-2">
                {sortProviderPoolMembers(pool.members).map((member) => (
                  <div key={member.id} class="rounded-lg bg-white px-3 py-2 text-xs text-gray-600">
                    <div class="flex min-w-0 justify-between gap-2">
                      <span class="truncate font-medium text-gray-800">{member.model}</span>
                      <span class="shrink-0 text-gray-400">{providerPoolSourceLabel(member.source)}</span>
                    </div>
                    <div class="mt-1.5 flex flex-wrap items-center gap-1.5">
                      <span class="inline-flex items-center rounded-full bg-indigo-50 px-2 py-0.5 text-[11px] font-medium text-indigo-700" title={t("settings.providerPoolMemberPriority")}>
                        {t("settings.providerPoolMemberPriority")} {member.priority ?? 0}
                      </span>
                      <span class="inline-flex rounded-full bg-gray-100 px-2 py-0.5 text-[11px] text-gray-600" title={t("settings.providerPoolMemberWeight")}>
                        {t("settings.providerPoolMemberWeight")} {member.weight ?? 100}
                      </span>
                    </div>
                  </div>
                ))}
              </div>
              <div class="mt-4 flex justify-end gap-2">
                {pool.policy === "semantic" && (
                  <button type="button" onClick={() => void openProviderPoolRouterDialog(pool)} class="rounded-lg border border-violet-200 bg-white px-3 py-1.5 text-xs text-violet-700 transition-colors hover:bg-violet-50">
                    {t("settings.routerManage")}
                  </button>
                )}
                <button type="button" onClick={() => openGatewayAPIInfo(pool.id, providerPoolModelInfo(pool))} class="rounded-lg border border-indigo-200 bg-white px-3 py-1.5 text-xs text-indigo-700 transition-colors hover:bg-indigo-50">
                  {t("settings.providerCallMethod")}
                </button>
                <button type="button" onClick={() => openProviderPoolDialog(pool)} class="rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-xs text-gray-700 transition-colors hover:bg-gray-50">
                  {t("settings.providerEdit")}
                </button>
                <button type="button" onClick={() => void removeProviderPool(pool)} class="rounded-lg border border-red-200 bg-white px-3 py-1.5 text-xs text-red-600 transition-colors hover:bg-red-50">
                  {t("settings.providerDelete")}
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

function UsageStatisticsSection() {
  const usage = localAPIUsage.value;
  const rows = usage?.rows || [];
  const summary = usage?.total_summary;
  const providerOptions = [
    { value: "local", label: t("settings.apiUsageSourceLocal") },
    { value: "cloud", label: t("settings.apiUsageSourceCloud") },
    ...providers.value.map((provider) => ({
      value: provider.name.trim(),
      label: provider.name.trim(),
    })),
  ].filter((option, index, options) =>
    option.value
    && options.findIndex((candidate) => candidate.value.toLowerCase() === option.value.toLowerCase()) === index
  );

  return (
    <div>
      <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 class="font-semibold text-gray-900">{t("settings.apiUsage")}</h2>
          <p class="mt-1 text-sm text-gray-500">{t("settings.apiUsageDesc")}</p>
        </div>
        <div class="flex flex-wrap items-center justify-end gap-3">
          <label class="flex items-center gap-2 text-xs text-gray-500">
            <span>{t("settings.apiUsageProviderFilter")}</span>
            <select
              value={localAPIUsageProvider.value}
              onChange={(event) => selectLocalAPIUsageProvider((event.currentTarget as HTMLSelectElement).value)}
              disabled={localAPIUsageLoading.value}
              class="rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-700 outline-none transition-colors focus:border-indigo-300 focus:ring-2 focus:ring-indigo-100 disabled:opacity-60"
            >
              <option value="">{t("settings.apiUsageProviderAll")}</option>
              {providerOptions.map((option) => (
                <option key={option.value} value={option.value}>{option.label}</option>
              ))}
            </select>
          </label>
          <button
            type="button"
            onClick={() => void fetchLocalAPIUsage()}
            disabled={localAPIUsageLoading.value}
            class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-60"
          >
            {localAPIUsageLoading.value ? "..." : t("settings.apiUsageRefresh")}
          </button>
        </div>
      </div>
      <div class="mb-5 flex flex-wrap gap-2">
        <UsagePeriodButton period="week" label={t("settings.apiUsagePeriodWeek")} />
        <UsagePeriodButton period="month" label={t("settings.apiUsagePeriodMonth")} />
        <UsagePeriodButton period="year" label={t("settings.apiUsagePeriodYear")} />
      </div>
      <div class="grid gap-4 md:grid-cols-4">
        <UsageCard label={t("settings.apiUsageCumulative")} value={formatNumber(lastSummaryValue(summary, 0, usage?.totals.total_tokens || 0))} tone="orange" />
        <UsageCard label={t("settings.apiUsageLocalModels")} value={formatNumber(lastSummaryValue(summary, 1, usage?.totals.local_tokens || 0))} tone="green" />
        <UsageCard label={t("settings.apiUsageCloudModels")} value={formatNumber(lastSummaryValue(summary, 2, usage?.totals.cloud_tokens || 0))} tone="purple" />
        <UsageCard label={t("settings.apiUsageTotalHistory")} value={formatNumber(usage?.total_history || 0)} tone="blue" />
      </div>
      <UsageSummaryChart summary={summary} />
      {localAPIUsageError.value && <p class="mt-3 text-sm text-red-600">{localAPIUsageError.value}</p>}
      <div class="mb-3 mt-6 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 class="text-sm font-semibold text-gray-900">{t("settings.apiUsageBreakdown")}</h3>
          <span class="text-xs text-gray-400">
            {t("settings.apiUsageRequests")}: {formatNumber(usage?.totals.requests || 0)}
            {(usage?.totals.pool_requests || 0) > 0 && (
              <> · {t("settings.apiUsagePoolRequests")}: {formatNumber(usage?.totals.pool_requests || 0)}
                {" · "}{t("settings.apiUsageFallbacks")}: {formatNumber(usage?.totals.fallback_count || 0)}
                {" · "}{t("settings.apiUsageLimited")}: {formatNumber(usage?.totals.limited_count || 0)}</>
            )}
          </span>
        </div>
      </div>
      <div class="overflow-hidden rounded-xl border border-gray-200 bg-white">
        {rows.length === 0 ? (
          <p class="p-4 text-sm text-gray-400">{localAPIUsageLoading.value ? "..." : t("settings.apiUsageEmpty")}</p>
        ) : (
          <div>
            <table class="w-full table-fixed divide-y divide-gray-100 text-sm">
              <thead class="bg-gray-50 text-left text-xs uppercase tracking-wide text-gray-400">
                <tr>
                  <th class="w-[14%] whitespace-nowrap px-4 py-3">{t("settings.apiUsageSource")}</th>
                  <th class="w-[28%] whitespace-nowrap px-4 py-3">{t("settings.apiUsageModel")}</th>
                  <th class="w-[10%] whitespace-nowrap px-4 py-3">{t("settings.apiUsageRequests")}</th>
                  <th class="w-[13%] whitespace-nowrap px-4 py-3">{t("settings.apiUsageInput")}</th>
                  <th class="w-[13%] whitespace-nowrap px-4 py-3">{t("settings.apiUsageOutput")}</th>
                  <th class="w-[13%] whitespace-nowrap px-4 py-3">{t("settings.apiUsageTotal")}</th>
                  <th class="w-[15%] whitespace-nowrap px-4 py-3">{t("settings.apiUsageLastUsed")}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100">
                {rows.map((row) => (
                  <tr key={`${row.api_key_id}:${row.source}:${row.model}`}>
                    <td class="truncate whitespace-nowrap px-4 py-3 text-gray-600" title={apiUsageSourceRowLabel(row.source_type, row.source_name, row.pool_name)}>
                      {apiUsageSourceRowLabel(row.source_type, row.source_name, row.pool_name)}
                    </td>
                    <td class="truncate whitespace-nowrap px-4 py-3 text-gray-600" title={row.member_model ? `${row.model} → ${row.member_model}` : row.model}>
                      {row.member_model ? `${row.model} → ${row.member_model}` : row.model}
                    </td>
                    <td class="whitespace-nowrap px-4 py-3 tabular-nums text-gray-600">{formatNumber(row.requests)}</td>
                    <td class="whitespace-nowrap px-4 py-3 tabular-nums text-gray-600">{formatNumber(row.input_tokens)}</td>
                    <td class="whitespace-nowrap px-4 py-3 tabular-nums text-gray-600">{formatNumber(row.output_tokens)}</td>
                    <td class="whitespace-nowrap px-4 py-3 tabular-nums text-gray-600">{formatNumber(row.total_tokens)}</td>
                    <td class="truncate whitespace-nowrap px-4 py-3 text-gray-500" title={formatDateTime(row.last_used_at)}>
                      {formatDateTime(row.last_used_at)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

function UsagePeriodButton({ period, label }: { period: UsagePeriod; label: string }) {
  const active = localAPIUsagePeriod.value === period;
  return (
    <button
      type="button"
      onClick={() => selectLocalAPIUsagePeriod(period)}
      class={`rounded-lg border px-3 py-1.5 text-sm transition-colors ${
        active
          ? "border-indigo-200 bg-indigo-50 text-indigo-700"
          : "border-gray-200 text-gray-600 hover:bg-gray-50"
      }`}
    >
      {label}
    </button>
  );
}

function UsageCard({ label, value, tone }: { label: string; value: string; tone: "orange" | "green" | "purple" | "blue" }) {
  const toneClasses = {
    orange: "text-orange-500 bg-orange-50 border-orange-100",
    green: "text-emerald-500 bg-emerald-50 border-emerald-100",
    purple: "text-violet-500 bg-violet-50 border-violet-100",
    blue: "text-sky-500 bg-sky-50 border-sky-100",
  }[tone];
  return (
    <div class="rounded-2xl border border-gray-100 bg-white p-5 shadow-sm">
      <div class="flex items-start justify-between gap-3">
        <div>
          <p class="text-sm font-medium text-gray-500">{label}</p>
          <p class="mt-4 text-3xl font-semibold tracking-tight text-gray-900">{value}</p>
        </div>
        <span class={`inline-flex h-8 w-8 items-center justify-center rounded-lg border text-xs ${toneClasses}`}>
          {label.slice(0, 1)}
        </span>
      </div>
    </div>
  );
}

function UsageSummaryChart({ summary }: { summary?: LocalAPIUsageTotalSummary }) {
  const xAxis = summary?.xAxis || [];
  const labels = [t("settings.apiUsageCumulative"), t("settings.apiUsageLocalModels"), t("settings.apiUsageCloudModels")];
  const colors = ["#f97316", "#10b981", "#8b5cf6"];
  const fills = ["#ffedd5", "#d1fae5", "#ede9fe"];
  const series = (summary?.series || []).slice(0, 3).map((item, index) => ({
    ...item,
    label: labels[index] || item.name,
    color: colors[index] || "#6366f1",
    fill: fills[index] || "#e0e7ff",
  }));
  const values = series.flatMap((item) => item.data);
  const maxValue = Math.max(1, ...values);
  const width = 760;
  const height = 300;
  const padding = { top: 24, right: 24, bottom: 42, left: 52 };
  const innerWidth = width - padding.left - padding.right;
  const innerHeight = height - padding.top - padding.bottom;
  const xFor = (index: number) => padding.left + (xAxis.length <= 1 ? innerWidth : (index / (xAxis.length - 1)) * innerWidth);
  const yFor = (value: number) => padding.top + innerHeight - (value / maxValue) * innerHeight;
  const xLabels = chartXAxisLabels(xAxis);

  return (
    <div class="mt-5 rounded-2xl border border-gray-100 bg-white p-5 shadow-sm">
      <div class="mb-3 flex flex-wrap items-center justify-end gap-4">
        {series.map((item) => (
          <span key={item.label} class="inline-flex items-center gap-2 text-sm font-medium text-gray-600">
            <span class="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: item.color }} />
            {item.label}
          </span>
        ))}
      </div>
      {xAxis.length === 0 || series.length === 0 ? (
        <p class="flex h-64 items-center justify-center text-sm text-gray-400">{t("settings.apiUsageEmpty")}</p>
      ) : (
        <svg class="h-72 w-full overflow-visible" viewBox={`0 0 ${width} ${height}`} role="img" aria-label={t("settings.apiUsage")}>
          {[0, 0.25, 0.5, 0.75, 1].map((ratio) => {
            const y = padding.top + innerHeight - ratio * innerHeight;
            return (
              <g key={ratio}>
                <line x1={padding.left} x2={width - padding.right} y1={y} y2={y} stroke="#e5e7eb" strokeDasharray="4 4" />
                <text x={padding.left - 12} y={y + 4} textAnchor="end" class="fill-gray-400 text-xs">
                  {formatNumber(Math.round(maxValue * ratio))}
                </text>
              </g>
            );
          })}
          {series.map((item) => {
            const points = item.data.map((value, index) => `${xFor(index)},${yFor(value)}`);
            const areaPoints = [`${xFor(0)},${padding.top + innerHeight}`, ...points, `${xFor(item.data.length - 1)},${padding.top + innerHeight}`].join(" ");
            return (
              <g key={item.label}>
                <polygon points={areaPoints} fill={item.fill} opacity="0.55" />
                <polyline points={points.join(" ")} fill="none" stroke={item.color} stroke-width="3" stroke-linecap="round" stroke-linejoin="round" />
              </g>
            );
          })}
          {xLabels.map((item) => (
            <text key={`${item.index}:${item.label}`} x={xFor(item.index)} y={height - 10} textAnchor="middle" class="fill-gray-400 text-xs">
              {formatChartDate(item.label)}
            </text>
          ))}
        </svg>
      )}
    </div>
  );
}

function lastSummaryValue(summary: LocalAPIUsageTotalSummary | undefined, seriesIndex: number, fallback: number): number {
  const data = summary?.series[seriesIndex]?.data || [];
  return data.length > 0 ? data[data.length - 1] : fallback;
}

function apiUsageSourceSummaryLabel(sourceType: string, sourceName?: string): string {
  switch (sourceType) {
    case "local":
      return t("settings.apiUsageSourceLocal");
    case "cloud":
      return cloudManagedProvider.value?.name || t("settings.apiUsageSourceCloud");
    case "provider":
      return sourceName
        ? `${t("settings.apiUsageSourceProvider")} · ${sourceName}`
        : t("settings.apiUsageSourceProvider");
    default:
      return t("settings.apiUsageSourceUnknown");
  }
}

function apiUsageSourceRowLabel(sourceType: string, sourceName?: string, poolName?: string): string {
  if (poolName) {
    const member = sourceName || apiUsageSourceSummaryLabel(sourceType);
    return `${poolName} → ${member}`;
  }
  if (sourceType === "provider" && sourceName) {
    return sourceName;
  }
  return apiUsageSourceSummaryLabel(sourceType);
}

function ProviderDialog({
  open,
  editing,
  step,
  name,
  baseURL,
  apiKey,
  headers,
  providerType,
  enabled,
  error,
  saving,
  testing,
  testSuccess,
  modelTarget,
  modelCatalog,
  modelSelected,
  modelDisplayNames,
  modelsLoading,
  modelsSaving,
  modelsError,
  onClose,
  onTest,
  onSave,
  onSaveModels,
  onToggleModel,
  onSelectAllModels,
  onInvertModels,
  onChangeModelDisplayName,
  onChangeName,
  onChangeBaseURL,
  onChangeAPIKey,
  onChangeHeaders,
  onChangeProviderType,
  onChangeEnabled,
}: {
  open: boolean;
  editing: boolean;
  step: "details" | "models";
  name: string;
  baseURL: string;
  apiKey: string;
  headers: ProviderHeader[];
  providerType: string;
  enabled: boolean;
  error: string;
  saving: boolean;
  testing: boolean;
  testSuccess: string;
	modelTarget: ManagedProvider | null;
  modelCatalog: ModelInfo[];
  modelSelected: Record<string, boolean>;
  modelDisplayNames: Record<string, string>;
  modelsLoading: boolean;
  modelsSaving: boolean;
  modelsError: string;
  onClose: () => void;
  onTest: () => void;
  onSave: () => void;
  onSaveModels: () => void;
  onToggleModel: (modelID: string, checked: boolean) => void;
  onSelectAllModels: () => void;
  onInvertModels: () => void;
  onChangeModelDisplayName: (modelID: string, value: string) => void;
  onChangeName: (value: string) => void;
  onChangeBaseURL: (value: string) => void;
  onChangeAPIKey: (value: string) => void;
  onChangeHeaders: (headers: ProviderHeader[]) => void;
  onChangeProviderType: (value: string) => void;
  onChangeEnabled: (value: boolean) => void;
}) {
  if (!open) return null;
  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 px-4" onClick={onClose}>
      <div class="w-full max-w-lg rounded-2xl bg-white shadow-2xl" onClick={(e) => e.stopPropagation()}>
        <div class="border-b border-gray-100 px-6 py-5">
          <h2 class="text-lg font-semibold text-gray-900">
            {step === "models" ? t("settings.providerModelsTitle") : editing ? t("settings.providerEditTitle") : t("settings.providerAddTitle")}
          </h2>
          <p class="mt-1 text-sm text-gray-500">
            {step === "models" ? t("settings.providerModelsDesc", modelTarget?.name || name) : t("settings.providerDialogDesc")}
          </p>
        </div>
        {step === "details" ? (
          <div class="space-y-4 px-6 py-5">
            <div>
              <label class="mb-1 block text-sm font-medium text-gray-700">{t("settings.providerType")}</label>
              <select
                class="w-full rounded-lg border border-gray-200 px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={providerType}
                onChange={(e) => onChangeProviderType((e.target as HTMLSelectElement).value)}
              >
                {providerTypes.map((item) => (
                  <option key={item.value} value={item.value}>{item.label}</option>
                ))}
              </select>
            </div>
            <div>
              <label class="mb-1 block text-sm font-medium text-gray-700">{t("settings.providerName")}</label>
              <input
                class="w-full rounded-lg border border-gray-200 px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={name}
                onInput={(e) => onChangeName((e.target as HTMLInputElement).value)}
                placeholder="OpenAI"
              />
            </div>
            <div>
              <label class="mb-1 block text-sm font-medium text-gray-700">{t("settings.providerBaseURL")}</label>
              <input
                class="w-full rounded-lg border border-gray-200 px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={baseURL}
                onInput={(e) => onChangeBaseURL((e.target as HTMLInputElement).value)}
                placeholder="https://api.openai.com/v1"
              />
            </div>
            <div>
              <label class="mb-1 block text-sm font-medium text-gray-700">{t("settings.providerAPIKeyOptional")}</label>
              <input
                type="password"
                autoComplete="off"
                spellcheck={false}
                class="w-full rounded-lg border border-gray-200 px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={apiKey}
                onInput={(e) => onChangeAPIKey((e.target as HTMLInputElement).value)}
                placeholder={editing ? t("settings.providerAPIKeyUnchanged") : "sk-..."}
              />
            </div>
            <div>
              <div class="mb-1 flex items-center justify-between">
                <label class="block text-sm font-medium text-gray-700">{t("settings.providerHeaders")}</label>
                <button
                  type="button"
                  class="text-xs font-medium text-indigo-600 hover:text-indigo-700"
                  onClick={() => onChangeHeaders([...headers, { name: "", value: "" }])}
                >
                  {t("settings.providerHeaderAdd")}
                </button>
              </div>
              <p class="mb-2 text-xs text-gray-500">{t("settings.providerHeadersHint")}</p>
              <div class="space-y-2">
                {headers.map((header, index) => (
                  <div class="flex items-center gap-2" key={index}>
                    <input
                      class="min-w-0 flex-1 rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                      value={header.name}
                      placeholder={t("settings.providerHeaderName")}
                      onInput={(e) => {
                        const next = [...headers];
                        next[index] = { ...next[index], name: (e.target as HTMLInputElement).value };
                        onChangeHeaders(next);
                      }}
                    />
                    <input
                      autoComplete="off"
                      class="min-w-0 flex-1 rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                      value={header.value}
                      placeholder={t("settings.providerHeaderValue")}
                      onInput={(e) => {
                        const next = [...headers];
                        next[index] = { ...next[index], value: (e.target as HTMLInputElement).value };
                        onChangeHeaders(next);
                      }}
                    />
                    <button
                      type="button"
                      class="rounded-md px-2 py-1 text-sm text-gray-400 hover:bg-gray-100 hover:text-red-600"
                      aria-label={t("settings.providerHeaderRemove")}
                      onClick={() => onChangeHeaders(headers.filter((_, itemIndex) => itemIndex !== index))}
                    >
                      ×
                    </button>
                  </div>
                ))}
              </div>
            </div>
            <div class="flex items-center gap-3">
              <label class="relative inline-flex cursor-pointer items-center">
                <input
                  type="checkbox"
                  checked={enabled}
                  onChange={(e) => onChangeEnabled((e.target as HTMLInputElement).checked)}
                  class="peer sr-only"
                />
                <div class="h-6 w-11 rounded-full bg-gray-200 transition-all after:absolute after:left-[2px] after:top-[2px] after:h-5 after:w-5 after:rounded-full after:border after:border-gray-300 after:bg-white after:transition-all after:content-[''] peer-checked:bg-indigo-600 peer-checked:after:translate-x-full peer-checked:after:border-white peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-indigo-300" />
              </label>
              <span class="text-sm text-gray-700">{enabled ? t("settings.providerEnabled") : t("settings.providerDisabled")}</span>
            </div>
            {error && <p class="text-sm text-red-600">{error}</p>}
            {testSuccess && <p class="text-sm text-green-600">{testSuccess}</p>}
          </div>
        ) : (
          <div class="px-6 py-5">
            {modelsLoading ? (
              <p class="text-sm text-gray-500">...</p>
            ) : modelCatalog.length === 0 ? (
              <p class="text-sm text-gray-500">{modelsError || t("settings.providerModelsEmpty")}</p>
            ) : (
              <div class="space-y-3">
                <div class="flex flex-wrap items-center gap-2">
                  <button
                    type="button"
                    onClick={onSelectAllModels}
                    class="rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-xs text-gray-700 transition-colors hover:bg-gray-50"
                  >
                    {t("settings.providerModelsSelectAll")}
                  </button>
                  <button
                    type="button"
                    onClick={onInvertModels}
                    class="rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-xs text-gray-700 transition-colors hover:bg-gray-50"
                  >
                    {t("settings.providerModelsInvert")}
                  </button>
                </div>
                <div class="max-h-80 space-y-2 overflow-y-auto pr-1">
                  {modelCatalog.map((model) => (
                    <div key={model.model} class="flex items-start gap-3 rounded-lg border border-gray-100 px-3 py-2 hover:bg-gray-50">
                      <input
                        type="checkbox"
                        checked={!!modelSelected[model.model]}
                        onChange={(e) => onToggleModel(model.model, (e.target as HTMLInputElement).checked)}
                        class="mt-1 h-4 w-4 rounded border-gray-300 text-indigo-600 focus:ring-indigo-500"
                      />
                      <span class="min-w-0 flex-1">
                        <span class="flex min-w-0 flex-wrap items-center gap-1.5">
                          <span class="truncate text-sm font-medium text-gray-900">{providerModelLabel(model)}</span>
                          <ProviderModelModalityBadges model={model} showPipelineTag showInputs showOutputs />
                        </span>
                        <span class="block truncate text-xs text-gray-500">{model.model}</span>
                        <input
                          class="mt-2 w-full rounded-md border border-gray-200 px-2 py-1.5 text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500 disabled:bg-gray-50 disabled:text-gray-400"
                          value={modelDisplayNames[model.model] || ""}
                          disabled={!modelSelected[model.model]}
                          onInput={(e) => onChangeModelDisplayName(model.model, (e.target as HTMLInputElement).value)}
                          placeholder={t("settings.providerModelDisplayNamePlaceholder")}
                        />
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}
            {modelsError && modelCatalog.length > 0 && <p class="mt-3 text-sm text-red-600">{modelsError}</p>}
          </div>
        )}
        <div class="flex justify-end gap-3 border-t border-gray-100 px-6 py-4">
          <button
            type="button"
            onClick={onClose}
            disabled={saving || testing || modelsSaving}
            class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-60"
          >
            {t("upgrade.cancel")}
          </button>
          {step === "details" && (
            <button
              type="button"
              onClick={onTest}
              disabled={saving || testing}
              class="rounded-lg border border-indigo-200 px-4 py-2 text-sm text-indigo-700 transition-colors hover:bg-indigo-50 disabled:opacity-60"
            >
              {testing ? "..." : t("settings.providerTest")}
            </button>
          )}
          <button
            type="button"
            onClick={step === "models" ? onSaveModels : onSave}
            disabled={saving || testing || modelsSaving || (step === "models" && modelsLoading)}
            class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
          >
            {saving || modelsSaving ? "..." : step === "models" ? t("settings.providerModelsSave") : t("settings.providerSaveNext")}
          </button>
        </div>
      </div>
    </div>
  );
}

function RouterProfileDialog() {
  const pool = providerPoolRouterDialogPool.value;
  if (!pool) return null;
  const status = providerPoolRouterStatuses.value[pool.id];
  const overviewCounts = routerOverviewCounts(status);
  const step = providerPoolRouterDialogStep.value;
  const config = providerPoolRouterConfig.value;
  const preview = providerPoolRouterPreview.value;
  const job = providerPoolRouterJob.value;
  const profile = providerPoolRouterProfile.value;
  const judges = providerPoolRouterJudgeModels();
  const progress = job?.total ? Math.min(100, Math.round((job.current / job.total) * 100)) : 0;
  const terminal = job && ["succeeded", "failed", "cancelled"].includes(job.status);
  const startBlockedReason = !preview
    ? ""
    : preview.eligible_snapshot_count < providerPoolRouterMinHistory
      ? t("settings.routerStartBlockedNoQueries")
      : routerUnknownPricingNeedsConsent(preview, config)
        ? t("settings.routerStartBlockedUnknownPricing")
        : "";

  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 p-4" role="dialog" aria-modal="true">
      <div class="flex max-h-[92vh] w-full max-w-6xl flex-col overflow-hidden rounded-3xl bg-white shadow-2xl">
        <div class="flex items-start justify-between border-b border-gray-100 px-7 py-5">
          <div>
            <p class="text-xs font-semibold uppercase tracking-[0.2em] text-violet-500">{t("settings.routerEyebrow")}</p>
            <h2 class="mt-1 text-xl font-semibold text-gray-950">{t("settings.routerTitle", pool.name)}</h2>
            <p class="mt-1 font-mono text-xs text-gray-500">{pool.model}</p>
          </div>
          <button type="button" onClick={closeProviderPoolRouterDialog} class="rounded-lg p-2 text-gray-400 hover:bg-gray-100 hover:text-gray-700" aria-label={t("observability.close")}>✕</button>
        </div>
        <div class="flex flex-wrap gap-2 border-b border-gray-100 px-7 py-3 text-xs">
          {(["overview", "configure", "progress", "profile"] as const).map((item) => (
            <button
              type="button"
              disabled={(item === "progress" && !job) || (item === "profile" && !profile && !status?.latest_candidate_profile)}
              onClick={() => item === "profile" && !profile ? void reviewProviderPoolRouterProfile() : (providerPoolRouterDialogStep.value = item)}
              class={`rounded-full px-3 py-1.5 font-medium ${step === item ? "bg-violet-100 text-violet-800" : "bg-gray-50 text-gray-500 hover:bg-gray-100"} disabled:cursor-not-allowed disabled:opacity-40`}
            >
              {t(`settings.routerStep${item[0].toUpperCase()}${item.slice(1)}`)}
            </button>
          ))}
        </div>
        <div class="min-h-0 flex-1 overflow-y-auto p-7">
          {step === "overview" && (
            <div class="space-y-5">
              <div class="grid gap-4 md:grid-cols-4">
                <RouterSummaryCard label={t("settings.routerQualifiedTraces")} value={String(overviewCounts.qualifiedQueryCount)} />
                <RouterSummaryCard label={t("settings.routerNewTraces")} value={String(overviewCounts.newQueryCount)} />
                <RouterSummaryCard label={t("settings.routerActiveProfile")} value={status?.active_profile ? `v${status.active_profile.version}` : t("settings.routerNone")} />
                <RouterSummaryCard label={t("settings.routerLatestJob")} value={status?.latest_job ? t(`settings.routerJob.${status.latest_job.status}`) : t("settings.routerNone")} />
              </div>
              {status?.pending_suggestion ? (
                <div class="rounded-2xl border border-amber-100 bg-amber-50 p-5">
                  <h3 class="font-semibold text-amber-900">{t("settings.routerSuggestionTitle")}</h3>
                  <p class="mt-1 text-sm text-amber-800">{t(`settings.routerReason.${status.pending_suggestion.reason}`)}</p>
                  {!status.pending_suggestion.member_compatible && <p class="mt-2 text-sm font-medium text-red-700">{t("settings.routerStaleMembers")}</p>}
                </div>
              ) : (
                <p class="rounded-2xl bg-gray-50 p-5 text-sm text-gray-500">{t("settings.routerSuggestionEmpty")}</p>
              )}
              <div class="flex flex-wrap gap-3">
                <button type="button" onClick={() => (providerPoolRouterDialogStep.value = "configure")} class="rounded-xl bg-violet-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-violet-700">
                  {t("settings.routerConfigureEvaluation")}
                </button>
                {status?.latest_candidate_profile && (
                  <button type="button" onClick={() => void reviewProviderPoolRouterProfile(status.latest_candidate_profile!.id)} class="rounded-xl border border-gray-200 px-4 py-2.5 text-sm text-gray-700 hover:bg-gray-50">
                    {t("settings.routerReviewCandidate")}
                  </button>
                )}
                {status?.active_profile && (
                  <button type="button" onClick={() => void rollbackProviderPoolRouter()} disabled={providerPoolRouterBusy.value} class="rounded-xl border border-amber-200 px-4 py-2.5 text-sm text-amber-700 hover:bg-amber-50 disabled:opacity-50">
                    {t("settings.routerRollback")}
                  </button>
                )}
              </div>
            </div>
          )}
          {step === "configure" && (
            <div class="grid gap-6 lg:grid-cols-[1fr_0.9fr]">
              <div class="space-y-4">
                <h3 class="text-base font-semibold text-gray-900">{t("settings.routerEvaluationConfig")}</h3>
                <label class="block text-sm text-gray-700">
                  <span class="mb-1 block font-medium">{t("settings.routerJudge")}</span>
                  <select value={config.judge_model} onChange={(e) => updateProviderPoolRouterConfig("judge_model", e.currentTarget.value)} class="w-full rounded-xl border border-gray-200 px-3 py-2.5">
                    <option value="">{t("settings.routerJudgeSelect")}</option>
                    {judges.map((model) => <option key={model.model} value={model.model}>{providerModelLabel(model)}</option>)}
                  </select>
                </label>
                <div class="grid gap-4 sm:grid-cols-2">
                  <RouterNumberField label={t("settings.routerMaxQueries")} value={config.max_queries} min={1} max={100} onChange={(v) => updateProviderPoolRouterConfig("max_queries", v)} />
                  <RouterNumberField label={t("settings.routerRepeats")} value={config.repeats} min={1} max={3} onChange={(v) => updateProviderPoolRouterConfig("repeats", v)} />
                  <RouterNumberField label={t("settings.routerMaxOutputTokens")} value={config.max_output_tokens} min={1} max={4096} onChange={(v) => updateProviderPoolRouterConfig("max_output_tokens", v)} />
                  <RouterNumberField label={t("settings.routerTimeout")} value={config.request_timeout_seconds} min={1} max={600} onChange={(v) => updateProviderPoolRouterConfig("request_timeout_seconds", v)} />
                  <RouterNumberField label={t("settings.routerBudget")} value={config.budget_amount} min={0} step="0.01" onChange={(v) => updateProviderPoolRouterConfig("budget_amount", v)} />
                  <label class="block text-sm text-gray-700">
                    <span class="mb-1 block font-medium">{t("settings.routerCurrency")}</span>
                    <select value={config.budget_currency} onChange={(e) => updateProviderPoolRouterConfig("budget_currency", e.currentTarget.value)} class="w-full rounded-xl border border-gray-200 px-3 py-2.5">
                      <option value="￥">{t("settings.routerCurrencyCNY")}</option>
                      <option value="USD">{t("settings.routerCurrencyUSD")}</option>
                    </select>
                  </label>
                </div>
                <button type="button" onClick={() => void previewProviderPoolEvaluation()} disabled={providerPoolRouterBusy.value || !config.judge_model} class="rounded-xl bg-violet-600 px-4 py-2.5 text-sm font-medium text-white disabled:opacity-50">
                  {providerPoolRouterBusy.value ? t("settings.routerWorking") : t("settings.routerPreview")}
                </button>
              </div>
              <div class="rounded-2xl border border-gray-100 bg-gray-50 p-5">
                <h3 class="font-semibold text-gray-900">{t("settings.routerPreviewTitle")}</h3>
                {preview ? (
                  <div class="mt-4 space-y-3 text-sm text-gray-600">
                    <p class="font-medium text-violet-700">{t("settings.routerEvaluationModeLabel")}: {t(routerEvaluationModeKey(preview.evaluation_mode))}</p>
                    <p>{t("settings.routerPreviewQueries", preview.selected_snapshot_count, preview.eligible_snapshot_count)}</p>
                    <p>{t("settings.routerPreviewCalls", preview.direct_candidate_calls, preview.judge_calls, preview.max_judge_calls, preview.max_total_calls)}</p>
                    <p>{t("settings.routerPreviewJudgeTokens", formatNumber(preview.judge_prompt_tokens), formatNumber(preview.max_judge_token_exposure))}</p>
                    <p>{t("settings.routerPreviewTokens", formatNumber(preview.max_token_exposure))}</p>
                    <p>{t("settings.routerPreviewJudgeCost", preview.known_judge_estimated_cost.toFixed(6), preview.currency)}</p>
                    <p>{t("settings.routerPreviewCost", preview.known_estimated_cost.toFixed(6), preview.currency)}</p>
                    <p>{t("settings.routerBudgetBehavior", config.budget_amount.toFixed(2), config.budget_currency)}</p>
                    {(!preview.judge_price_known || preview.unknown_price_members.length > 0) && (
                      <>
                        <p class="rounded-xl bg-amber-100 p-3 text-amber-800">{t("settings.routerUnknownPriceWarning")}</p>
                        <label class="flex items-start gap-2 rounded-xl border border-amber-200 bg-white p-3 text-amber-900">
                          <input type="checkbox" checked={config.allow_unknown_pricing} onChange={(e) => updateProviderPoolRouterUnknownPricingConsent(e.currentTarget.checked)} />
                          <span>{t("settings.routerUnknownPriceConsent")}</span>
                        </label>
                      </>
                    )}
                    <button type="button" title={startBlockedReason} onClick={() => void startProviderPoolEvaluation()} disabled={providerPoolRouterBusy.value || !!startBlockedReason} class="mt-2 w-full rounded-xl bg-emerald-600 px-4 py-2.5 font-medium text-white disabled:cursor-not-allowed disabled:opacity-50">
                      {t("settings.routerStartExplicit")}
                    </button>
                    {startBlockedReason && <p class="rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-800">{startBlockedReason}</p>}
                  </div>
                ) : <p class="mt-3 text-sm text-gray-400">{t("settings.routerPreviewEmpty")}</p>}
              </div>
            </div>
          )}
          {step === "progress" && job && (
            <div class="mx-auto max-w-3xl space-y-5">
              <div class="flex items-center justify-between"><h3 class="text-lg font-semibold text-gray-900">{t("settings.routerEvaluationProgress")}</h3><span class="rounded-full bg-gray-100 px-3 py-1 text-xs font-medium text-gray-700">{t(`settings.routerJob.${job.status}`)}</span></div>
              {(() => {
                const calls = routerJobCallCounts(job, pool.members.length);
                return (
                  <div class="grid gap-3 sm:grid-cols-2">
                    <RouterSummaryCard label={t("settings.routerEvaluationModeLabel")} value={t(routerEvaluationModeKey(job.evaluation_mode))} />
                    <RouterSummaryCard label={t("settings.routerJobCalls")} value={t("settings.routerJobCallsValue", calls.candidateCalls, calls.judgeCalls, calls.maxJudgeCalls)} />
                    <RouterSummaryCard label={t("settings.routerJudgeTokenEstimate")} value={job.max_judge_token_exposure ? t("settings.routerJobJudgeTokensValue", formatNumber(job.judge_prompt_tokens || 0), formatNumber(job.max_judge_token_exposure)) : t("settings.routerEstimateUnavailable")} />
                    <RouterSummaryCard label={t("settings.routerKnownEstimate")} value={job.known_estimated_cost !== undefined ? `${job.known_estimated_cost.toFixed(6)} ${job.estimate_currency || job.budget_currency}` : t("settings.routerEstimateUnavailable")} />
                  </div>
                );
              })()}
              <div class="h-3 overflow-hidden rounded-full bg-gray-100"><div class="h-full rounded-full bg-violet-500 transition-all" style={{ width: `${progress}%` }} /></div>
              <p class="text-sm text-gray-600">{t(`settings.routerPhase.${job.phase || job.status}`)} · {job.current}/{job.total} ({progress}%)</p>
              <p class="text-sm text-gray-500">{t("settings.routerJobBudget", job.budget_amount.toFixed(2), job.budget_currency)} · {job.unknown_pricing || job.allow_unknown_pricing ? t("settings.routerJobUnknownPricingConsented") : t("settings.routerJobKnownPricing")}</p>
              <p class="text-xs text-gray-400">{t("settings.routerPollingHint")}</p>
              {job.error && <p class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{job.error}</p>}
              <div class="flex gap-3">
                {!terminal && <button type="button" onClick={() => void cancelProviderPoolEvaluation()} disabled={providerPoolRouterBusy.value || job.cancellation_requested} class="rounded-xl border border-red-200 px-4 py-2 text-sm text-red-700 disabled:opacity-50">{job.cancellation_requested ? t("settings.routerCancelling") : t("settings.routerCancel")}</button>}
                {job.status === "succeeded" && status?.latest_candidate_profile && <button type="button" onClick={() => void reviewProviderPoolRouterProfile(status.latest_candidate_profile!.id)} class="rounded-xl bg-violet-600 px-4 py-2 text-sm font-medium text-white">{t("settings.routerReviewCandidate")}</button>}
                {terminal && <button type="button" onClick={() => (providerPoolRouterDialogStep.value = "configure")} class="rounded-xl border border-gray-200 px-4 py-2 text-sm text-gray-700">{t("settings.routerRunAnother")}</button>}
              </div>
            </div>
          )}
          {step === "profile" && profile && (
            <div class="space-y-6">
              <div class="flex flex-wrap items-start justify-between gap-4">
                <div>
                  <h3 class="text-lg font-semibold text-gray-900">{t("settings.routerProfileVersion", profile.version)}</h3>
                  <p class="mt-1 text-sm text-gray-500">{formatDateTime(profile.generated_at)}</p>
                  <p class="mt-1 text-xs text-violet-700">{t("settings.routerProfileSchemaAlgorithm", profile.schema_version, t(`settings.routerAlgorithm.${routerProfileKind(profile)}`))}</p>
                </div>
                <div class="flex gap-2">
                  {profile.active && <span class="rounded-full bg-emerald-50 px-3 py-1 text-xs font-medium text-emerald-700">{t("settings.routerActive")}</span>}
                  <button type="button" title={routerActivationReasonKey(profile) ? t(routerActivationReasonKey(profile)!) : undefined} disabled={!profile.activation_allowed || profile.active || providerPoolRouterBusy.value} onClick={() => void activateProviderPoolRouterCandidate()} class="rounded-xl bg-emerald-600 px-4 py-2 text-sm font-medium text-white disabled:cursor-not-allowed disabled:opacity-40">{t("settings.routerActivate")}</button>
                </div>
              </div>
              {!profile.activation_allowed && <p class="rounded-xl bg-amber-50 p-4 text-sm text-amber-800">{t(routerActivationReasonKey(profile) || "settings.routerBlocked.unknown")}</p>}
              {isValidatedCollapsedProfile(profile) && <p class="rounded-xl bg-emerald-50 p-4 text-sm text-emerald-800">{t("settings.routerValidatedCollapse")}</p>}
              {profile.schema_version === 2 && profile.v2 ? <RouterV2ProfileReview profile={profile} /> : <RouterV1ProfileReview profile={profile} />}
            </div>
          )}
          {providerPoolRouterError.value && <p class="mt-5 rounded-xl bg-red-50 p-4 text-sm text-red-700">{providerPoolRouterError.value}</p>}
        </div>
      </div>
    </div>
  );
}

function RouterV1ProfileReview({ profile }: { profile: ProviderPoolRouterProfile }) {
  const b = profile.metrics.baselines;
  const hasBaselines = b && (b.best_single_model.quality > 0 || b.cheapest_model.quality > 0);
  return (
    <>
      <div class="grid gap-4 md:grid-cols-3">
        <RouterSummaryCard label={t("settings.routerTrainMetrics")} value={`${profile.metrics.train_utility.toFixed(3)} / ${profile.metrics.train_quality.toFixed(3)} / ${profile.metrics.train_cost_score.toFixed(3)}`} />
        <RouterSummaryCard label={t("settings.routerHeldOutMetrics")} value={`${profile.metrics.held_out_utility.toFixed(3)} / ${profile.metrics.held_out_quality.toFixed(3)} / ${profile.metrics.held_out_cost_score.toFixed(3)}`} />
        <RouterSummaryCard label={t("settings.routerSpend")} value={profile.metrics.monetary_spend_known ? `${profile.metrics.spend.toFixed(6)} ${profile.metrics.currency || ""}` : `${profile.metrics.spend.toFixed(6)} ${profile.metrics.cost_unit}`} />
      </div>
      {hasBaselines && (
        <>
          <h4 class="mb-3 font-semibold text-gray-900">{t("settings.routerBaselines")}</h4>
          <div class="grid gap-4 md:grid-cols-3">
            <RouterSummaryCard label={t("settings.routerBaselineRouted")} value={`Q ${(profile.metrics.held_out_quality * 100).toFixed(1)}%`} />
            <RouterSummaryCard label={t("settings.routerBaselineBestSingle")} value={`Q ${(b.best_single_model.quality * 100).toFixed(1)}%`} />
            <RouterSummaryCard label={t("settings.routerBaselineCheapest")} value={`Q ${(b.cheapest_model.quality * 100).toFixed(1)}%`} />
          </div>
          <div class="grid gap-4 md:grid-cols-3">
            <RouterSummaryCard label={t("settings.routerBaselineRandom")} value={`Q ${(b.random_model.quality * 100).toFixed(1)}%`} />
            <RouterSummaryCard label={t("settings.routerBaselineOracle")} value={`Q ${(b.oracle_model.quality * 100).toFixed(1)}%`} />
          </div>
        </>
      )}
      {profile.metrics.all_clusters_one_member && <p class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{t("settings.routerCollapseWarning")}</p>}
      <div>
        <h4 class="mb-3 font-semibold text-gray-900">{t("settings.routerClusters")}</h4>
        <div class="overflow-x-auto rounded-xl border border-gray-200">
          <table class="w-full text-left text-sm"><thead class="bg-gray-50 text-xs text-gray-500"><tr><th class="px-4 py-3">{t("settings.routerCluster")}</th><th class="px-4 py-3">{t("settings.routerSamples")}</th><th class="px-4 py-3">{t("settings.routerTarget")}</th><th class="px-4 py-3">P50 / P90 / P95 / P99</th><th class="px-4 py-3">{t("settings.routerOOD")}</th></tr></thead>
          <tbody class="divide-y divide-gray-100">{(profile.clusters || []).map((cluster) => <tr key={cluster.id}><td class="px-4 py-3 font-mono text-xs">{cluster.id}</td><td class="px-4 py-3">{cluster.sample_count}</td><td class="px-4 py-3">{cluster.target.model}</td><td class="px-4 py-3 tabular-nums">{cluster.distance_quantiles.p50.toFixed(3)} / {cluster.distance_quantiles.p90.toFixed(3)} / {cluster.distance_quantiles.p95.toFixed(3)} / {cluster.distance_quantiles.p99.toFixed(3)}</td><td class="px-4 py-3 tabular-nums">{cluster.ood_threshold.toFixed(3)}</td></tr>)}</tbody></table>
        </div>
      </div>
      <div><h4 class="mb-3 font-semibold text-gray-900">{t("settings.routerDistribution")}</h4><div class="grid gap-3 md:grid-cols-2">{(profile.candidate_distribution || []).map((item) => <div key={item.member_id} class="rounded-xl bg-gray-50 p-4 text-sm"><p class="font-medium text-gray-900">{item.target.model}</p><p class="mt-1 text-gray-500">{t("settings.routerDistributionValue", item.cluster_count, item.sample_count)}</p></div>)}</div></div>
    </>
  );
}

function RouterV2ProfileReview({ profile }: { profile: ProviderPoolRouterProfile }) {
  const summary = profile.v2!;
  const percent = (value: number) => `${(value * 100).toFixed(1)}%`;
  const knownComparableCost = summary.savings_known
    && summary.baseline_best_single_model.cost_known
    && summary.routed.cost_known
    && !!summary.baseline_best_single_model.currency
    && summary.baseline_best_single_model.currency === summary.routed.currency;
  const currency = summary.routed.currency || summary.baseline_best_single_model.currency || "";
  return (
    <>
      <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <RouterSummaryCard label={t("settings.routerV2Evidence")} value={t("settings.routerV2EvidenceValue", summary.sample_count, summary.query_group_count, summary.round_count)} />
        <RouterSummaryCard label={t("settings.routerV2QualityTarget")} value={`${percent(summary.target_quality_retention)} · ${percent(summary.confidence_level)} ${t("settings.routerConfidence")}`} />
        <RouterSummaryCard label={t("settings.routerV2Retention")} value={`${percent(summary.point_retention)} · ${percent(summary.conservative_retention)} ${t("settings.routerLowerBoundShort")}`} />
        <RouterSummaryCard label={t("settings.routerV2Feasibility")} value={profile.feasible ? t("settings.routerFeasible") : t("settings.routerInfeasible")} />
      </div>
      <div class="grid gap-4 md:grid-cols-3">
        <RouterSummaryCard label={t("settings.routerBestSingleQuality")} value={percent(summary.baseline_best_single_model.quality)} />
        <RouterSummaryCard label={t("settings.routerRoutedQuality")} value={percent(summary.routed.quality)} />
        <RouterSummaryCard label={t("settings.routerQualityConstraintGap")} value={summary.retention_lower_bound.toFixed(4)} />
      </div>
      {knownComparableCost ? (
        <div class="grid gap-4 md:grid-cols-3">
          <RouterSummaryCard label={t("settings.routerBestSingleCost")} value={`${summary.baseline_best_single_model.cost!.toFixed(6)} ${currency}`} />
          <RouterSummaryCard label={t("settings.routerRoutedCost")} value={`${summary.routed.cost!.toFixed(6)} ${currency}`} />
          <RouterSummaryCard label={t("settings.routerEstimatedSavings")} value={`${(summary.savings ?? 0).toFixed(6)} ${currency} (${percent(summary.savings_fraction || 0)})`} />
        </div>
      ) : (
        <p class="rounded-xl bg-gray-50 p-4 text-sm text-gray-600">{t("settings.routerSavingsUnavailable")}</p>
      )}
      <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <RouterSummaryCard label={t("settings.routerCoverage")} value={percent(summary.coverage)} />
        <RouterSummaryCard label={t("settings.routerFallbackRate")} value={percent(summary.fallback_rate)} />
        <RouterSummaryCard label={t("settings.routerLowConfidenceRate")} value={percent(summary.low_confidence_rate)} />
        <RouterSummaryCard label={t("settings.routerOODRate")} value={percent(summary.ood_rate)} />
      </div>
      <div>
        <h4 class="mb-3 font-semibold text-gray-900">{t("settings.routerPairwiseCV", summary.cv_fold_count)}</h4>
        <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <RouterSummaryCard label={t("settings.routerAccuracy")} value={percent(summary.pairwise_metrics.top_class_accuracy)} />
          <RouterSummaryCard label={t("settings.routerLogLoss")} value={summary.pairwise_metrics.log_loss.toFixed(4)} />
          <RouterSummaryCard label={t("settings.routerBrier")} value={summary.pairwise_metrics.brier.toFixed(4)} />
          <RouterSummaryCard label={t("settings.routerECE")} value={summary.pairwise_metrics.ece.toFixed(4)} />
        </div>
      </div>
      <div class="grid gap-4 md:grid-cols-2">
        <div class="rounded-2xl border border-gray-100 bg-gray-50 p-5">
          <h4 class="font-semibold text-gray-900">{t("settings.routerThresholds")}</h4>
          <dl class="mt-3 grid grid-cols-2 gap-3 text-sm">
            <div><dt class="text-gray-500">{t("settings.routerSimilarity")}</dt><dd class="font-medium tabular-nums">{summary.thresholds.minimum_similarity.toFixed(3)}</dd></div>
            <div><dt class="text-gray-500">{t("settings.routerConfidence")}</dt><dd class="font-medium tabular-nums">{summary.thresholds.minimum_confidence.toFixed(3)}</dd></div>
            <div><dt class="text-gray-500">{t("settings.routerMargin")}</dt><dd class="font-medium tabular-nums">{summary.thresholds.minimum_margin.toFixed(3)}</dd></div>
            <div><dt class="text-gray-500">{t("settings.routerQualitySlack")}</dt><dd class="font-medium tabular-nums">{summary.thresholds.quality_slack.toFixed(3)}</dd></div>
          </dl>
        </div>
        <div class="rounded-2xl border border-gray-100 bg-gray-50 p-5">
          <h4 class="font-semibold text-gray-900">{t("settings.routerValidationEvidence")}</h4>
          <p class="mt-2 text-sm text-gray-600">{t("settings.routerFallbackMember")}: <span class="font-mono">{profile.fallback_member_id}</span></p>
          <p class="mt-1 text-sm text-gray-600">{summary.optimize_known_cost ? t("settings.routerKnownCostOptimized") : t("settings.routerQualityOnlyCalibration")}</p>
          {summary.model_fallback_reason && <p class="mt-1 text-sm text-amber-700">{t("settings.routerSimilarityBTFallback")}</p>}
          {profile.collapsed_single_member && <p class="mt-1 text-sm text-gray-600">{t("settings.routerCollapsedMember", summary.collapsed_member_id || profile.fallback_member_id)}</p>}
        </div>
      </div>
      <div>
        <h4 class="mb-3 font-semibold text-gray-900">{t("settings.routerDistribution")}</h4>
        <div class="grid gap-3 md:grid-cols-2">{(profile.candidate_distribution || []).map((item) => <div key={item.member_id} class="rounded-xl bg-gray-50 p-4 text-sm"><p class="font-medium text-gray-900">{item.target.model}</p><p class="mt-1 text-gray-500">{t("settings.routerV2DistributionValue", item.sample_count, percent(item.fraction || 0))}</p></div>)}</div>
      </div>
      {(summary.warnings || []).length > 0 && (
        <div class="rounded-xl border border-amber-100 bg-amber-50 p-4">
          <h4 class="font-semibold text-amber-900">{t("settings.routerWarnings")}</h4>
          <ul class="mt-2 list-disc space-y-1 pl-5 text-sm text-amber-800">{summary.warnings!.map((warning, index) => <li key={`${warning}-${index}`}>{t(routerWarningKey(warning))}</li>)}</ul>
        </div>
      )}
    </>
  );
}

function routerWarningKey(warning: string): string {
  if (warning.includes("mixed currencies")) return "settings.routerWarning.mixed_currencies";
  if (warning.includes("price") || warning.includes("pricing")) return "settings.routerWarning.unknown_pricing";
  if (warning.includes("collapsed")) return "settings.routerWarning.collapsed";
  if (warning.includes("baseline quality is zero")) return "settings.routerWarning.zero_baseline";
  if (warning.includes("quality-retention")) return "settings.routerWarning.quality_constraint";
  if (warning.includes("insufficient") || warning.includes("could not fit")) return "settings.routerWarning.insufficient_evidence";
  return "settings.routerWarning.validation";
}

function RouterSummaryCard({ label, value }: { label: string; value: string }) {
  return <div class="rounded-2xl border border-gray-100 bg-white p-4 shadow-sm"><p class="text-xs font-medium uppercase tracking-wide text-gray-400">{label}</p><p class="mt-2 text-lg font-semibold text-gray-900">{value}</p></div>;
}

function RouterNumberField({ label, value, min, max, step = "1", onChange }: { label: string; value: number; min: number; max?: number; step?: string; onChange: (value: number) => void }) {
  return <label class="block text-sm text-gray-700"><span class="mb-1 block font-medium">{label}</span><input type="number" value={value} min={min} max={max} step={step} onInput={(e) => onChange(Number(e.currentTarget.value))} class="w-full rounded-xl border border-gray-200 px-3 py-2.5" /></label>;
}

function ProviderPoolDialog({
  open,
  editing,
  step,
  name,
  model,
  policy,
  enabled,
  members,
  models,
  error,
  saving,
  policies,
  policiesLoading,
  policiesError,
  cloudCredentialAvailable,
  onClose,
  onNext,
  onBack,
  onSave,
  onChangeName,
  onChangeModel,
  onChangePolicy,
  onChangeEnabled,
  onToggleSourceModel,
}: {
  open: boolean;
  editing: boolean;
  step: "basics" | "members";
  name: string;
  model: string;
  policy: string;
  enabled: boolean;
  members: ProviderPoolMember[];
  models: ModelInfo[];
  error: string;
  saving: boolean;
  policies: ProviderPoolPolicy[];
  policiesLoading: boolean;
  policiesError: string;
  cloudCredentialAvailable: boolean;
  onClose: () => void;
  onNext: () => void;
  onBack: () => void;
  onSave: () => void;
  onChangeName: (value: string) => void;
  onChangeModel: (value: string) => void;
  onChangePolicy: (value: string) => void;
  onChangeEnabled: (value: boolean) => void;
  onToggleSourceModel: (source: string, model: string, checked: boolean) => void;
}) {
  if (!open) return null;
  const sources = [
    { value: "local", label: t("settings.providerPoolSourceLocal") },
    { value: "cloud", label: providerPoolSourceLabel("cloud") },
    ...providers.value.filter((provider) => provider.enabled).map((provider) => ({
      value: `provider:${provider.id}`,
      label: provider.name,
    })),
  ];
  const sourceLabel = (source: string) => sources.find((item) => item.value === source)?.label || source;
  const modelLabel = (source: string, modelID: string) => {
    const item = models.find((candidate) => candidate.source === source && candidate.model === modelID);
    return item?.display_name || item?.label || modelID;
  };
  const activeSource = sources.some((source) => source.value === providerPoolSourceFilter.value)
    ? providerPoolSourceFilter.value
    : sources[0]?.value || "local";
  const search = providerPoolModelSearch.value.trim().toLocaleLowerCase();
  const catalog = models
    .filter((item) => item.source === activeSource && item.model)
    .map((item) => ({
      value: item.model,
      label: item.display_name || item.label || item.model,
    }))
    .filter((item, index, items) => items.findIndex((candidate) => candidate.value === item.value) === index)
    .filter((item) => !search || item.value.toLocaleLowerCase().includes(search) || item.label.toLocaleLowerCase().includes(search));
  const fallbackPolicies: ProviderPoolPolicy[] = [
    { type: "priority_weight", experimental: false, available: true },
    {
      type: "semantic",
      experimental: true,
      available: false,
      reason: policiesLoading ? "gateway_catalog_unavailable" : "gateway_catalog_unavailable",
    },
  ];
  const policyOptions = [...(policies.length > 0 ? policies : fallbackPolicies)];
  if (!policyOptions.some((item) => item.type === policy)) {
    policyOptions.push({
      type: policy,
      experimental: policy === "semantic",
      available: editingProviderPool.value?.policy_available !== false,
      reason: editingProviderPool.value?.policy_unavailable_reason,
    });
  }
  const persistedPolicyUnavailable = editingProviderPool.value?.policy === policy
    && editingProviderPool.value.policy_available === false;
  const selectedPolicyCapability = policyOptions.find((item) => item.type === policy);
  const selectedPolicyUnavailable = (
    persistedPolicyUnavailable
    && editingProviderPool.value?.policy_unavailable_reason !== "opencsg_login_required"
  ) || providerPoolPolicyHardUnavailable(selectedPolicyCapability);
  const selectedPolicyUnavailableReason = editingProviderPool.value?.policy_unavailable_reason
    || selectedPolicyCapability?.reason
    || t("settings.providerPoolPolicyUnavailable");
  const basicsValid = !!name.trim()
    && !!model.trim()
    && !selectedPolicyUnavailable
    && (policy !== "semantic" || cloudCredentialAvailable);
  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/50 p-3 sm:p-6" onClick={onClose}>
      <div class="flex max-h-[calc(100vh-1.5rem)] w-full max-w-6xl flex-col overflow-hidden rounded-2xl bg-white shadow-2xl sm:max-h-[calc(100vh-3rem)]" onClick={(event) => event.stopPropagation()}>
        <div class="border-b border-gray-100 px-5 py-4 sm:px-7 sm:py-5">
          <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 class="text-lg font-semibold text-gray-950">{editing ? t("settings.providerPoolEditTitle") : t("settings.providerPoolAddTitle")}</h2>
              <p class="mt-1 text-sm text-gray-500">{t("settings.providerPoolDialogDesc")}</p>
            </div>
            <div class="flex shrink-0 items-center gap-2" aria-label={t("settings.providerPoolProgress")}>
              {(["basics", "members"] as const).map((item, index) => {
                const active = step === item;
                const complete = item === "basics" && step === "members";
                return (
                  <div key={item} class="flex items-center gap-2">
                    {index > 0 && <span class="h-px w-5 bg-gray-200 sm:w-8" />}
                    <span class={`inline-flex items-center gap-2 text-xs font-medium ${active ? "text-indigo-700" : complete ? "text-emerald-700" : "text-gray-400"}`}>
                      <span class={`inline-flex h-6 w-6 items-center justify-center rounded-full ${active ? "bg-indigo-600 text-white" : complete ? "bg-emerald-100 text-emerald-700" : "bg-gray-100 text-gray-500"}`}>
                        {index + 1}
                      </span>
                      {item === "basics" ? t("settings.providerPoolStepBasics") : t("settings.providerPoolStepMembers")}
                    </span>
                  </div>
                );
              })}
            </div>
          </div>
        </div>
        {step === "basics" ? (
          <div class="flex-1 overflow-y-auto px-5 py-6 sm:px-7">
            <div class="mx-auto max-w-2xl">
              <div class="rounded-2xl border border-indigo-100 bg-indigo-50/70 p-4">
                <h3 class="text-sm font-semibold text-indigo-950">{t("settings.providerPoolBasicsTitle")}</h3>
                <p class="mt-1 text-sm leading-6 text-indigo-700">{t("settings.providerPoolBasicsDesc")}</p>
              </div>
              <div class="mt-6 space-y-5">
                <div>
                  <label class="mb-1.5 block text-sm font-medium text-gray-700">{t("settings.providerPoolName")}</label>
                  <input
                    class="w-full rounded-xl border border-gray-200 px-4 py-3 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                    value={name}
                    onInput={(event) => onChangeName((event.target as HTMLInputElement).value)}
                    placeholder={t("settings.providerPoolNamePlaceholder")}
                    disabled={saving}
                  />
                  <p class="mt-1.5 text-xs text-gray-500">{t("settings.providerPoolNameHint")}</p>
                </div>
                <div>
                  <label class="mb-1.5 block text-sm font-medium text-gray-700">{t("settings.providerPoolModel")}</label>
                  <input
                    class="w-full rounded-xl border border-gray-200 px-4 py-3 font-mono text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                    value={model}
                    onInput={(event) => onChangeModel((event.target as HTMLInputElement).value)}
                    placeholder={t("settings.providerPoolModelPlaceholder")}
                    disabled={saving}
                  />
                  <p class="mt-1.5 text-xs text-gray-500">{t("settings.providerPoolModelHint")}</p>
                </div>
                <fieldset>
                  <legend class="mb-1.5 text-sm font-medium text-gray-700">{t("settings.providerPoolPolicy")}</legend>
                  <p class="mb-3 text-xs text-gray-500">{t("settings.providerPoolPolicyHint")}</p>
                  <div class="grid gap-3 sm:grid-cols-2">
                    {policyOptions.map((item) => {
                      const unavailable = providerPoolPolicyHardUnavailable(item)
                        || (
                          editingProviderPool.value?.policy === item.type
                          && editingProviderPool.value.policy_available === false
                          && editingProviderPool.value.policy_unavailable_reason !== "opencsg_login_required"
                        );
                      return (
                        <label key={item.type} class={`rounded-xl border p-4 transition-colors ${unavailable ? "cursor-not-allowed border-gray-100 bg-gray-50 opacity-70" : policy === item.type ? "cursor-pointer border-indigo-300 bg-indigo-50/70" : "cursor-pointer border-gray-200 hover:border-indigo-200"}`}>
                          <span class="flex items-start gap-3">
                            <input
                              type="radio"
                              name="provider-pool-policy"
                              value={item.type}
                              checked={policy === item.type}
                              disabled={saving || unavailable}
                              onChange={() => onChangePolicy(item.type)}
                              class="mt-0.5 h-4 w-4 border-gray-300 text-indigo-600 focus:ring-indigo-500"
                            />
                            <span class="min-w-0">
                              <span class="flex flex-wrap items-center gap-2 text-sm font-medium text-gray-900">
                                {item.label?.trim() || providerPoolPolicyLabel(item.type)}
                                {item.type === "semantic" && (
                                  <span class="rounded-full bg-violet-100 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-violet-700">
                                    {t("settings.providerPoolPolicyExperimental")}
                                  </span>
                                )}
                              </span>
                              <span class="mt-1 block text-xs leading-5 text-gray-500">
                                {item.type === "semantic"
                                  ? t("settings.providerPoolPolicySemanticHint")
                                  : item.type === "priority_weight"
                                    ? t("settings.providerPoolPolicyPriorityHint")
                                    : item.type}
                              </span>
                              {unavailable && (
                                <span class="mt-1 block text-xs text-amber-700">
                                  {editingProviderPool.value?.policy === item.type
                                    ? providerPoolPolicyReason(editingProviderPool.value.policy_unavailable_reason || item.reason)
                                    : providerPoolPolicyReason(item.reason)}
                                </span>
                              )}
                            </span>
                          </span>
                        </label>
                      );
                    })}
                  </div>
                  {policiesLoading && <p class="mt-2 text-xs text-gray-500">{t("settings.providerPoolPoliciesLoading")}</p>}
                  {policiesError && (
                    <p class="mt-2 text-xs text-amber-700">
                      {t("settings.providerPoolPoliciesLoadFailedFallback")}
                      <button type="button" onClick={() => void fetchProviderPoolPolicies()} class="ml-2 font-medium underline" disabled={policiesLoading}>
                        {t("settings.providerPoolPoliciesRetry")}
                      </button>
                    </p>
                  )}
                  {policy === "semantic" && !cloudCredentialAvailable && (
                    <p class="mt-2 text-xs text-amber-700">{t("settings.providerPoolPolicyLoginRequired")}</p>
                  )}
                  {policy === "semantic" && selectedPolicyUnavailable && (
                    <p class="mt-2 text-xs text-amber-700">{providerPoolPolicyReason(selectedPolicyUnavailableReason)}</p>
                  )}
                </fieldset>
                <div class="flex items-center justify-between gap-4 rounded-xl border border-gray-200 px-4 py-3">
                  <div>
                    <p class="text-sm font-medium text-gray-800">{t("settings.providerPoolEnabledLabel")}</p>
                    <p class="mt-0.5 text-xs text-gray-500">{t("settings.providerPoolEnabledHint")}</p>
                  </div>
                  <label class="relative inline-flex shrink-0 cursor-pointer items-center">
                    <input type="checkbox" checked={enabled} onChange={(event) => onChangeEnabled((event.target as HTMLInputElement).checked)} disabled={saving} class="peer sr-only" />
                    <div class="h-6 w-11 rounded-full bg-gray-200 transition-all after:absolute after:left-[2px] after:top-[2px] after:h-5 after:w-5 after:rounded-full after:border after:border-gray-300 after:bg-white after:shadow-sm after:transition-all after:content-[''] peer-checked:bg-indigo-600 peer-checked:after:translate-x-full peer-checked:after:border-white peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-indigo-300 peer-disabled:opacity-60" />
                  </label>
                </div>
              </div>
              {error && <p class="mt-4 text-sm text-red-600">{error}</p>}
            </div>
          </div>
        ) : (
          <div class="flex min-h-0 flex-1 flex-col overflow-y-auto px-5 py-5 sm:px-7">
            <div class="mb-4">
              <h3 class="text-sm font-semibold text-gray-900">{t("settings.providerPoolMembersTitle")}</h3>
              <p class="mt-1 text-xs leading-5 text-gray-500">{t("settings.providerPoolMembersWizardDesc")}</p>
            </div>
            <div class="grid min-h-0 flex-1 gap-5 lg:grid-cols-[minmax(0,1fr)_22rem]">
              <div class="flex min-h-[24rem] min-w-0 flex-col overflow-hidden rounded-2xl border border-gray-200">
                <div class="border-b border-gray-100 bg-gray-50/70 p-3">
                  <div class="flex gap-2 overflow-x-auto pb-1">
                    {sources.map((source) => {
                      const selectedCount = members.filter((member) => member.source === source.value).length;
                      return (
                        <button
                          key={source.value}
                          type="button"
                          onClick={() => {
                            providerPoolSourceFilter.value = source.value;
                            providerPoolModelSearch.value = "";
                          }}
                          class={`shrink-0 rounded-lg px-3 py-2 text-xs font-medium transition-colors ${activeSource === source.value ? "bg-indigo-600 text-white shadow-sm" : "bg-white text-gray-600 hover:bg-gray-100"}`}
                        >
                          {source.label}
                          {selectedCount > 0 && <span class={`ml-2 rounded-full px-1.5 py-0.5 text-[10px] ${activeSource === source.value ? "bg-white/20 text-white" : "bg-indigo-50 text-indigo-700"}`}>{selectedCount}</span>}
                        </button>
                      );
                    })}
                  </div>
                  <div class="relative mt-2">
                    <svg class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                      <circle cx="11" cy="11" r="7" />
                      <path d="m20 20-3.5-3.5" />
                    </svg>
                    <input
                      type="search"
                      value={providerPoolModelSearch.value}
                      onInput={(event) => (providerPoolModelSearch.value = (event.target as HTMLInputElement).value)}
                      placeholder={t("settings.providerPoolSearchPlaceholder")}
                      aria-label={t("settings.providerPoolSearchLabel")}
                      class="w-full rounded-lg border border-gray-200 bg-white py-2.5 pl-9 pr-3 text-sm outline-none focus:ring-2 focus:ring-indigo-500"
                    />
                  </div>
                </div>
                <div class="min-h-0 flex-1 overflow-y-auto p-3">
                  {catalog.length === 0 ? (
                    <div class="flex h-full min-h-48 flex-col items-center justify-center px-6 text-center">
                      <p class="text-sm font-medium text-gray-600">{search ? t("settings.providerPoolSearchEmpty") : t("settings.providerPoolSourceEmpty")}</p>
                      <p class="mt-1 text-xs text-gray-400">{search ? t("settings.providerPoolSearchEmptyHint") : t("settings.providerPoolSourceEmptyHint")}</p>
                    </div>
                  ) : (
                    <div class="grid gap-2 sm:grid-cols-2">
                      {catalog.map((item) => {
                        const selected = members.some((member) => member.source === activeSource && member.model === item.value);
                        return (
                          <label key={item.value} class={`flex cursor-pointer items-start gap-3 rounded-xl border p-3 transition-colors ${selected ? "border-indigo-200 bg-indigo-50/60" : "border-gray-100 hover:border-gray-200 hover:bg-gray-50"}`}>
                            <input
                              type="checkbox"
                              checked={selected}
                              onChange={(event) => onToggleSourceModel(activeSource, item.value, (event.target as HTMLInputElement).checked)}
                              disabled={saving}
                              class="mt-0.5 h-4 w-4 shrink-0 rounded border-gray-300 text-indigo-600 focus:ring-indigo-500"
                            />
                            <span class="min-w-0">
                              <span class="block truncate text-sm font-medium text-gray-900">{item.label}</span>
                              <span class="mt-0.5 block truncate font-mono text-xs text-gray-500">{item.value}</span>
                            </span>
                          </label>
                        );
                      })}
                    </div>
                  )}
                </div>
              </div>
              <aside class="flex min-h-[18rem] flex-col overflow-hidden rounded-2xl border border-gray-200 bg-gray-50/70 lg:sticky lg:top-0 lg:max-h-[calc(100vh-15rem)]">
                <div class="flex items-center justify-between border-b border-gray-200 px-4 py-3">
                  <div>
                    <h4 class="text-sm font-semibold text-gray-900">{t("settings.providerPoolSelectedTitle")}</h4>
                    <p class="mt-0.5 text-xs text-gray-500">{t("settings.providerPoolSelectedHint")}</p>
                  </div>
                  <span class="rounded-full bg-indigo-100 px-2.5 py-1 text-xs font-semibold text-indigo-700">{members.length}</span>
                </div>
                <div class="min-h-0 flex-1 overflow-y-auto p-3">
                  {members.length === 0 ? (
                    <div class="flex h-full min-h-36 items-center justify-center px-5 text-center text-sm text-gray-400">{t("settings.providerPoolSelectedEmpty")}</div>
                  ) : (
                    <div class="space-y-2">
                      {members.map((member, memberIndex) => (
                        <div key={member.id} class="rounded-xl border border-gray-200 bg-white p-3 shadow-sm">
                          <div class="flex items-start justify-between gap-2">
                            <div class="min-w-0">
                              <p class="truncate text-sm font-medium text-gray-900">{modelLabel(member.source, member.model)}</p>
                              <p class="mt-0.5 truncate font-mono text-xs text-gray-500">{member.model}</p>
                              <div class="mt-2 flex flex-wrap items-center gap-1.5">
                                <span class="inline-flex items-center rounded-full bg-indigo-50 px-2 py-0.5 text-[11px] font-medium text-indigo-700" title={t("settings.providerPoolMemberPriority")}>
                                  {t("settings.providerPoolMemberPriority")} {member.priority ?? 0}
                                </span>
                                <span class="inline-flex rounded-full bg-gray-100 px-2 py-0.5 text-[11px] text-gray-600" title={t("settings.providerPoolMemberWeight")}>
                                  {t("settings.providerPoolMemberWeight")} {member.weight ?? 100}
                                </span>
                                <span class="inline-flex rounded-full bg-gray-100 px-2 py-0.5 text-[11px] text-gray-600">{sourceLabel(member.source)}</span>
                              </div>
                            </div>
                            <button
                              type="button"
                              onClick={() => onToggleSourceModel(member.source, member.model, false)}
                              disabled={saving}
                              aria-label={t("settings.providerPoolMemberRemove")}
                              title={t("settings.providerPoolMemberRemove")}
                              class="shrink-0 rounded-lg p-1.5 text-gray-400 transition-colors hover:bg-red-50 hover:text-red-600 disabled:opacity-60"
                            >
                              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="h-4 w-4" aria-hidden="true">
                                <path d="M3 6h18M8 6V4h8v2m-9 0 1 14h8l1-14M10 11v6m4-6v6" />
                              </svg>
                            </button>
                          </div>
                          <button
                            type="button"
                            onClick={() => openProviderPoolMemberConfigDialog(memberIndex)}
                            disabled={saving}
                            class="mt-3 w-full rounded-lg border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-600 transition-colors hover:border-indigo-200 hover:bg-indigo-50 hover:text-indigo-700 disabled:opacity-60"
                          >
                            {t("settings.providerPoolMemberConfigure")}
                          </button>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              </aside>
            </div>
            {error && <p class="mt-4 text-sm text-red-600">{error}</p>}
          </div>
        )}
        <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 bg-white px-5 py-4 sm:px-7">
          <button type="button" onClick={onClose} disabled={saving} class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-60">{t("upgrade.cancel")}</button>
          <div class="flex items-center gap-3">
            {step === "members" && (
              <button type="button" onClick={onBack} disabled={saving} class="rounded-lg border border-gray-200 px-4 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-60">{t("settings.providerPoolBack")}</button>
            )}
            {step === "basics" ? (
              <button type="button" onClick={onNext} disabled={saving || !basicsValid} class="rounded-lg bg-indigo-600 px-5 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50">{t("settings.providerPoolNext")}</button>
            ) : (
              <button type="button" onClick={onSave} disabled={saving || members.length === 0} class="rounded-lg bg-indigo-600 px-5 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50">{saving ? t("settings.providerPoolSaving") : t("settings.providerPoolSave")}</button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function ProviderPoolMemberConfigDialog({
  open,
  member,
  saving,
  onClose,
  onSave,
  onChange,
}: {
  open: boolean;
  member: ProviderPoolMember | null;
  saving: boolean;
  onClose: () => void;
  onSave: () => void;
  onChange: (key: "priority" | "weight" | "requests_per_minute" | "tokens_per_minute" | "max_concurrent", value: string) => void;
}) {
  if (!open || !member) return null;
  return (
    <div class="fixed inset-0 z-[60] flex items-center justify-center bg-gray-900/40 px-4" onClick={onClose}>
      <div class="w-full max-w-md rounded-2xl bg-white shadow-2xl" onClick={(event) => event.stopPropagation()}>
        <div class="border-b border-gray-100 px-6 py-5">
          <h2 class="text-lg font-semibold text-gray-900">{t("settings.providerPoolMemberConfigureTitle")}</h2>
          <p class="mt-1 truncate font-mono text-sm text-gray-500">{member.model}</p>
        </div>
        <div class="space-y-4 px-6 py-5">
          <p class="text-sm text-gray-500">{t("settings.providerPoolMemberConfigureDesc")}</p>
          <div class="grid gap-4 sm:grid-cols-2">
            <PoolNumberInput label={t("settings.providerPoolMemberPriority")} value={member.priority} disabled={saving} onChange={(value) => onChange("priority", value)} />
            <PoolNumberInput label={t("settings.providerPoolMemberWeight")} value={member.weight} disabled={saving} onChange={(value) => onChange("weight", value)} />
            <PoolNumberInput label={t("settings.providerPoolMemberRPM")} value={member.requests_per_minute} disabled={saving} onChange={(value) => onChange("requests_per_minute", value)} />
            <PoolNumberInput label={t("settings.providerPoolMemberTPM")} value={member.tokens_per_minute} disabled={saving} onChange={(value) => onChange("tokens_per_minute", value)} />
            <PoolNumberInput label={t("settings.providerPoolMemberMaxConcurrent")} value={member.max_concurrent} disabled={saving} onChange={(value) => onChange("max_concurrent", value)} />
          </div>
        </div>
        <div class="flex justify-end gap-3 border-t border-gray-100 px-6 py-4">
          <button type="button" onClick={onClose} disabled={saving} class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-60">{t("upgrade.cancel")}</button>
          <button type="button" onClick={onSave} disabled={saving} class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white transition-colors hover:bg-indigo-700 disabled:opacity-60">{t("settings.providerPoolMemberConfigureSave")}</button>
        </div>
      </div>
    </div>
  );
}

function PoolNumberInput({ label, value, disabled, onChange }: { label: string; value?: number; disabled: boolean; onChange: (value: string) => void }) {
  return (
    <label>
      <span class="mb-1 block text-xs font-medium text-gray-600">{label}</span>
      <input type="number" min="0" value={value ?? ""} onInput={(event) => onChange((event.target as HTMLInputElement).value)} disabled={disabled} class="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
    </label>
  );
}

function ProviderModelEditDialog({
  open,
  modelID,
  displayName,
  description,
  displayNamePlaceholder,
  error,
  saving,
  onClose,
  onSave,
  onChangeModelID,
  onChangeDisplayName,
  onChangeDescription,
}: {
  open: boolean;
  modelID: string;
  displayName: string;
  description: string;
  displayNamePlaceholder: string;
  error: string;
  saving: boolean;
  onClose: () => void;
  onSave: () => void;
  onChangeModelID: (value: string) => void;
  onChangeDisplayName: (value: string) => void;
  onChangeDescription: (value: string) => void;
}) {
  if (!open) return null;
  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 px-4" onClick={onClose}>
      <div class="w-full max-w-md rounded-2xl bg-white shadow-2xl" onClick={(e) => e.stopPropagation()}>
        <div class="border-b border-gray-100 px-6 py-5">
          <h2 class="text-lg font-semibold text-gray-900">{t("settings.providerModelEditTitle")}</h2>
          <p class="mt-1 text-sm text-gray-500">{t("settings.providerModelEditDesc")}</p>
        </div>
        <div class="space-y-4 px-6 py-5">
          <div>
            <label class="mb-1 block text-sm font-medium text-gray-700">{t("settings.providerModelID")}</label>
            <input
              class="w-full rounded-lg border border-gray-200 px-3 py-2.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500"
              value={modelID}
              onInput={(e) => onChangeModelID((e.target as HTMLInputElement).value)}
              placeholder="my-model"
              disabled={saving}
            />
          </div>
          <div>
            <label class="mb-1 block text-sm font-medium text-gray-700">{t("settings.providerModelDisplayName")}</label>
            <input
              class="w-full rounded-lg border border-gray-200 px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
              value={displayName}
              onInput={(e) => onChangeDisplayName((e.target as HTMLInputElement).value)}
              placeholder={displayNamePlaceholder || t("settings.providerModelDisplayNamePlaceholder")}
              disabled={saving}
            />
            <p class="mt-1 text-xs text-gray-400">{t("settings.providerModelOptionalFieldHint")}</p>
          </div>
          <div>
            <label class="mb-1 block text-sm font-medium text-gray-700">{t("settings.providerModelDescription")}</label>
            <textarea
              class="min-h-20 w-full rounded-lg border border-gray-200 px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
              value={description}
              onInput={(e) => onChangeDescription((e.target as HTMLTextAreaElement).value)}
              placeholder={t("settings.providerModelDescriptionPlaceholder")}
              disabled={saving}
            />
          </div>
          {error && <p class="text-sm text-red-600">{error}</p>}
        </div>
        <div class="flex justify-end gap-3 border-t border-gray-100 px-6 py-4">
          <button
            type="button"
            onClick={onClose}
            disabled={saving}
            class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-60"
          >
            {t("upgrade.cancel")}
          </button>
          <button
            type="button"
            onClick={onSave}
            disabled={saving}
            class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
          >
            {saving ? "..." : t("settings.providerModelSave")}
          </button>
        </div>
      </div>
    </div>
  );
}
