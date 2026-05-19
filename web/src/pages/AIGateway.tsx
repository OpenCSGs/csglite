import { signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { t, locale } from "../i18n";
import { formatNumber, formatDateTime, formatChartDate, chartXAxisLabels } from "../utils/format";
import { ProviderModelModalityBadges, providerModelLabel, defaultProviderModelDisplayName } from "../components/ProviderModelBadges";
import {
  clearCloudAPIKey,
  createProvider,
  createLocalAPIKey,
  deleteProvider,
  deleteLocalAPIKey,
  getCloudAuthStatus,
  getLocalAPIKeys,
  getLocalAPIUsage,
  getProviderManageTags,
  getProviderSelectedTags,
  getProviders,
  replaceProviderManageTags,
  saveCloudAPIKey,
  updateProvider,
  updateProviderManageTag,
  validateProvider,
  updateLocalAPIKeySettings,
} from "../api/client";
import type { CloudAuthStatus, LocalAPIKeysResponse, LocalAPIUsageResponse, LocalAPIUsageTotalSummary, ModelInfo, ProviderTagModelSelection, ThirdPartyProvider } from "../api/client";

type GatewayTab = "apiKeys" | "providers" | "usage";
type UsagePeriod = "week" | "month" | "year";

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
const providersLoading = signal(false);
const providersError = signal("");
const cloudAuth = signal<CloudAuthStatus | null>(null);
const cloudAPIKeyInput = signal("");
const cloudAPIKeyError = signal("");
const isClearingCloudAPIKey = signal(false);
const isSavingCloudAPIKey = signal(false);
const copiedSnippet = signal("");
const isProviderDialogOpen = signal(false);
const editingProvider = signal<ThirdPartyProvider | null>(null);
const providerFormName = signal("");
const providerFormBaseURL = signal("");
const providerFormAPIKey = signal("");
const providerFormType = signal("openai");
const providerFormEnabled = signal(true);
const providerFormError = signal("");
const providerFormSaving = signal(false);
const providerDialogStep = signal<"details" | "models">("details");
const providerModelTarget = signal<ThirdPartyProvider | null>(null);
const providerModelCatalog = signal<ModelInfo[]>([]);
const providerModelSelected = signal<Record<string, boolean>>({});
const providerModelDisplayNames = signal<Record<string, string>>({});
const providerModelsLoading = signal(false);
const providerModelsSaving = signal(false);
const providerModelsError = signal("");
const providerSelectedModels = signal<Record<string, ModelInfo[]>>({});
const isProviderModelEditOpen = signal(false);
const providerModelEditProvider = signal<ThirdPartyProvider | null>(null);
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

async function fetchProviderOptions() {
  providersLoading.value = true;
  providersError.value = "";
  try {
    const list = await getProviders();
    providers.value = list;
    const entries = await Promise.all(
      list.map(async (provider) => {
        try {
          return [provider.id, await getProviderSelectedTags(provider.name || provider.id)] as const;
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
  providerFormType.value = provider?.provider || "openai";
  providerFormEnabled.value = provider?.enabled ?? true;
  providerFormError.value = "";
  isProviderDialogOpen.value = true;
}

function closeProviderDialog() {
  if (providerFormSaving.value || providerModelsSaving.value) return;
  isProviderDialogOpen.value = false;
  editingProvider.value = null;
  providerModelTarget.value = null;
  providerDialogStep.value = "details";
  providerModelCatalog.value = [];
  providerModelSelected.value = {};
  providerModelDisplayNames.value = {};
  providerModelsError.value = "";
  providerFormError.value = "";
}

function openProviderModelEditDialog(provider: ThirdPartyProvider, model: ModelInfo) {
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

async function loadProviderDialogModels(provider: ThirdPartyProvider) {
  providerModelsLoading.value = true;
  providerModelsError.value = "";
  try {
    const [catalog, selected] = await Promise.all([
      getProviderManageTags(provider.id),
      getProviderSelectedTags(provider.name || provider.id),
    ]);
    const selectedIDs = new Set(selected.map((model) => model.model));
    const defaultNames = Object.fromEntries(catalog.map((model) => [model.model, defaultProviderModelDisplayName(model)] as const));
    const selectedDisplayNames = Object.fromEntries(selected.flatMap((model) => {
      const displayName = defaultProviderModelDisplayName(model).trim();
      const defaultName = (defaultNames[model.model] || model.model).trim();
      return displayName && displayName !== defaultName ? [[model.model, displayName] as const] : [];
    }));
    providerModelCatalog.value = catalog;
    providerModelSelected.value = Object.fromEntries(catalog.map((model) => [model.model, selectedIDs.has(model.model)]));
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

async function saveProviderForm() {
  const name = providerFormName.value.trim();
  const baseURL = providerFormBaseURL.value.trim();
  const apiKey = providerFormAPIKey.value.trim();
  const providerType = providerFormType.value.trim() || "openai";
  const enabled = providerFormEnabled.value;

  if (!name || !baseURL) {
    providerFormError.value = t("settings.providerNameURLRequired");
    return;
  }
  if (!editingProvider.value && !apiKey) {
    providerFormError.value = t("settings.providerAPIKeyRequired");
    return;
  }

  providerFormSaving.value = true;
  providerFormError.value = "";
  try {
    await validateProvider({
      id: editingProvider.value?.id,
      name,
      base_url: baseURL,
      api_key: apiKey || undefined,
      provider: providerType,
      enabled,
    });
    let savedProvider: ThirdPartyProvider;
    if (editingProvider.value) {
      savedProvider = await updateProvider(editingProvider.value.id, {
        name,
        base_url: baseURL,
        api_key: apiKey || undefined,
        provider: providerType,
        enabled,
      });
    } else {
      savedProvider = await createProvider({
        name,
        base_url: baseURL,
        api_key: apiKey,
        provider: providerType,
        enabled,
      });
    }
    await fetchProviderOptions();
    notifyProvidersChanged();
    editingProvider.value = savedProvider;
    providerModelTarget.value = savedProvider;
    providerDialogStep.value = "models";
    await loadProviderDialogModels(savedProvider);
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

function selectLocalAPIUsagePeriod(period: UsagePeriod) {
  localAPIUsagePeriod.value = period;
  void fetchLocalAPIUsage(period);
}

function selectLocalAPIUsageProvider(provider: string) {
  localAPIUsageProvider.value = provider;
  void fetchLocalAPIUsage(localAPIUsagePeriod.value, provider);
}

function localAPIOrigin(): string {
  return window.location.origin;
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

export function AIGateway() {
  void locale.value;

  useEffect(() => {
    void fetchLocalAPIKeys();
    void fetchLocalAPIUsage();
    fetchCloudAuth();
    void fetchProviderOptions();
  }, []);

  return (
    <div class="mx-auto max-w-6xl p-8">
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
        <GatewayTabButton tab="usage" label={t("settings.tabUsage")} />
      </div>

      {activeGatewayTab.value === "apiKeys" && <APIKeysSection />}
      {activeGatewayTab.value === "providers" && <ProvidersSection />}
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
        providerType={providerFormType.value}
        enabled={providerFormEnabled.value}
        error={providerFormError.value}
        saving={providerFormSaving.value}
        modelTarget={providerModelTarget.value}
        modelCatalog={providerModelCatalog.value}
        modelSelected={providerModelSelected.value}
        modelDisplayNames={providerModelDisplayNames.value}
        modelsLoading={providerModelsLoading.value}
        modelsSaving={providerModelsSaving.value}
        modelsError={providerModelsError.value}
        onClose={closeProviderDialog}
        onSave={() => void saveProviderForm()}
        onSaveModels={() => void saveProviderModels()}
        onToggleModel={toggleProviderModel}
        onChangeModelDisplayName={changeProviderModelDisplayName}
        onChangeName={(value) => (providerFormName.value = value)}
        onChangeBaseURL={(value) => (providerFormBaseURL.value = value)}
        onChangeAPIKey={(value) => (providerFormAPIKey.value = value)}
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
            {t("gateway.cloudKeyBadge")}
          </div>
          <h2 class="mt-3 text-base font-semibold text-gray-900">{t("settings.cloudAPIKey")}</h2>
          <p class="mt-1 text-sm leading-6 text-gray-500">{t("settings.cloudAPIKeyDesc")}</p>
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
          <p class="mt-1 text-sm font-semibold text-gray-900">{t("chat.cloudGatewayValue")}</p>
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
  const origin = localAPIOrigin();
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
              <div class="h-7 w-12 rounded-full bg-gray-200 transition-all after:absolute after:left-[3px] after:top-[3px] after:h-6 after:w-6 after:rounded-full after:border after:border-gray-200 after:bg-white after:shadow-sm after:transition-all after:content-[''] peer-checked:bg-indigo-600 peer-checked:after:translate-x-5 peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-indigo-200 peer-disabled:cursor-not-allowed peer-disabled:opacity-60" />
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
        ) : providers.value.length === 0 ? (
          <div class="rounded-2xl border border-dashed border-gray-200 p-6 text-center lg:col-span-2">
            <p class="text-sm font-medium text-gray-700">{t("settings.providersEmpty")}</p>
            <p class="mt-1 text-sm text-gray-500">{t("settings.providersHint")}</p>
          </div>
        ) : (
          providers.value.map((provider) => {
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
                    </div>
                    <p class="mt-1 truncate text-xs text-gray-500">{provider.base_url}</p>
                    <p class="mt-1 text-[11px] uppercase tracking-wide text-gray-400">{provider.provider || "openai"}</p>
                  </div>
                  <label class="relative inline-flex shrink-0 cursor-pointer items-center">
                    <input
                      type="checkbox"
                      checked={provider.enabled}
                      onChange={() => void toggleProviderEnabled(provider)}
                      class="peer sr-only"
                    />
                    <div class="h-5 w-9 rounded-full bg-gray-200 transition-all after:absolute after:left-[2px] after:top-[2px] after:h-4 after:w-4 after:rounded-full after:border after:border-gray-300 after:bg-white after:transition-all after:content-[''] peer-checked:bg-indigo-600 peer-checked:after:translate-x-full peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-indigo-300" />
                  </label>
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
                            <button
                              type="button"
                              onClick={() => openProviderModelEditDialog(provider, model)}
                              title={t("settings.providerModelEdit")}
                              aria-label={t("settings.providerModelEdit")}
                              class="shrink-0 rounded border border-indigo-100 bg-white/70 p-1 text-indigo-700 transition-colors hover:bg-white"
                            >
                              <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                                <path stroke-linecap="round" stroke-linejoin="round" d="M16.862 4.487l1.687-1.688a1.875 1.875 0 112.652 2.652L10.582 16.07a4.5 4.5 0 01-1.897 1.13L6 18l.8-2.685a4.5 4.5 0 011.13-1.897l8.932-8.931z" />
                                <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 7.125L16.875 4.5" />
                              </svg>
                            </button>
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
                </div>
              </div>
            );
          })
        )}
      </div>
    </section>
  );
}

function UsageStatisticsSection() {
  const usage = localAPIUsage.value;
  const rows = usage?.rows || [];
  const summary = usage?.total_summary;
  const providerOptions = providers.value
    .map((provider) => provider.name.trim())
    .filter((name, index, names) => name && names.indexOf(name) === index);

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
              {providerOptions.map((name) => (
                <option key={name} value={name}>{name}</option>
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
          <span class="text-xs text-gray-400">{t("settings.apiUsageRequests")}: {formatNumber(usage?.totals.requests || 0)}</span>
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
                    <td class="truncate whitespace-nowrap px-4 py-3 text-gray-600" title={apiUsageSourceRowLabel(row.source_type, row.source_name)}>
                      {apiUsageSourceRowLabel(row.source_type, row.source_name)}
                    </td>
                    <td class="truncate whitespace-nowrap px-4 py-3 text-gray-600" title={row.model}>
                      {row.model}
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
      return t("settings.apiUsageSourceCloud");
    case "provider":
      return sourceName
        ? `${t("settings.apiUsageSourceProvider")} · ${sourceName}`
        : t("settings.apiUsageSourceProvider");
    default:
      return t("settings.apiUsageSourceUnknown");
  }
}

function apiUsageSourceRowLabel(sourceType: string, sourceName?: string): string {
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
  providerType,
  enabled,
  error,
  saving,
  modelTarget,
  modelCatalog,
  modelSelected,
  modelDisplayNames,
  modelsLoading,
  modelsSaving,
  modelsError,
  onClose,
  onSave,
  onSaveModels,
  onToggleModel,
  onChangeModelDisplayName,
  onChangeName,
  onChangeBaseURL,
  onChangeAPIKey,
  onChangeProviderType,
  onChangeEnabled,
}: {
  open: boolean;
  editing: boolean;
  step: "details" | "models";
  name: string;
  baseURL: string;
  apiKey: string;
  providerType: string;
  enabled: boolean;
  error: string;
  saving: boolean;
  modelTarget: ThirdPartyProvider | null;
  modelCatalog: ModelInfo[];
  modelSelected: Record<string, boolean>;
  modelDisplayNames: Record<string, string>;
  modelsLoading: boolean;
  modelsSaving: boolean;
  modelsError: string;
  onClose: () => void;
  onSave: () => void;
  onSaveModels: () => void;
  onToggleModel: (modelID: string, checked: boolean) => void;
  onChangeModelDisplayName: (modelID: string, value: string) => void;
  onChangeName: (value: string) => void;
  onChangeBaseURL: (value: string) => void;
  onChangeAPIKey: (value: string) => void;
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
              <label class="mb-1 block text-sm font-medium text-gray-700">{t("settings.providerAPIKey")}</label>
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
            <div class="flex items-center gap-3">
              <label class="relative inline-flex cursor-pointer items-center">
                <input
                  type="checkbox"
                  checked={enabled}
                  onChange={(e) => onChangeEnabled((e.target as HTMLInputElement).checked)}
                  class="peer sr-only"
                />
                <div class="h-6 w-11 rounded-full bg-gray-200 transition-all after:absolute after:left-[2px] after:top-[2px] after:h-5 after:w-5 after:rounded-full after:border after:border-gray-300 after:bg-white after:transition-all after:content-[''] peer-checked:bg-indigo-600 peer-checked:after:translate-x-full peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-indigo-300" />
              </label>
              <span class="text-sm text-gray-700">{enabled ? t("settings.providerEnabled") : t("settings.providerDisabled")}</span>
            </div>
            {error && <p class="text-sm text-red-600">{error}</p>}
          </div>
        ) : (
          <div class="px-6 py-5">
            {modelsLoading ? (
              <p class="text-sm text-gray-500">...</p>
            ) : modelCatalog.length === 0 ? (
              <p class="text-sm text-gray-500">{modelsError || t("settings.providerModelsEmpty")}</p>
            ) : (
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
            )}
            {modelsError && modelCatalog.length > 0 && <p class="mt-3 text-sm text-red-600">{modelsError}</p>}
          </div>
        )}
        <div class="flex justify-end gap-3 border-t border-gray-100 px-6 py-4">
          <button
            type="button"
            onClick={onClose}
            disabled={saving || modelsSaving}
            class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-60"
          >
            {t("upgrade.cancel")}
          </button>
          <button
            type="button"
            onClick={step === "models" ? onSaveModels : onSave}
            disabled={saving || modelsSaving || (step === "models" && modelsLoading)}
            class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
          >
            {saving || modelsSaving ? "..." : step === "models" ? t("settings.providerModelsSave") : t("settings.providerSaveNext")}
          </button>
        </div>
      </div>
    </div>
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
