import { signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { DirectoryPickerDialog } from "../components/DirectoryPickerDialog";
import { UpgradeDialog, type UpgradeProgress } from "../components/UpgradeDialog";
import { t, locale, setLocale } from "../i18n";
import type { Locale } from "../i18n";
import {
  browseLocalDirectories,
  checkUpgrade,
  clearCloudAPIKey,
  clearCloudToken,
  getCloudAuthStatus,
  getSettings,
  saveCloudAPIKey,
  saveCloudToken,
  saveSettings,
  upgradeWithProgress,
  getProviders,
  createProvider,
  updateProvider,
  deleteProvider,
  validateProvider,
  getProviderManageTags,
  getProviderSelectedTags,
  replaceProviderManageTags,
  updateProviderManageTag,
  getLocalAPIKeys,
  updateLocalAPIKeySettings,
  createLocalAPIKey,
  deleteLocalAPIKey,
  getLocalAPIUsage,
} from "../api/client";
import type { AppSettings, CloudAuthStatus, LocalAPIKeysResponse, LocalAPIUsageResponse, LocalAPIUsageTotalSummary, LocalDirectoryBrowseResponse, ModelInfo, ProviderTagModelSelection, ThirdPartyProvider, WebSearchSettings } from "../api/client";

const contextLengthSteps = [4096, 8192, 16384, 32768, 65536, 131072, 262144];
const contextLengthLabels = ["4k", "8k", "16k", "32k", "64k", "128k", "256k"];
const contextStorageKey = "csghub.chat.num_ctx";
const parallelSteps = [1, 2, 4, 8];
const parallelLabels = ["1", "2", "4", "8"];
const parallelStorageKey = "csghub.chat.num_parallel";
const upgradeReloadTimeoutMs = 45_000;

const storageLocation = signal("");
const modelDirectory = signal("");
const datasetDirectory = signal("");
const appVersion = signal("");
const autostartEnabled = signal(false);
const isSavingAutostart = signal(false);
const contextIndex = signal(1);
const parallelIndex = signal(2);
const cloudAuth = signal<CloudAuthStatus | null>(null);
const cloudTokenInput = signal("");
const cloudAPIKeyInput = signal("");
const cloudAuthError = signal("");
const cloudAPIKeyError = signal("");
const isClearingCloudToken = signal(false);
const isSavingCloudToken = signal(false);
const isClearingCloudAPIKey = signal(false);
const isSavingCloudAPIKey = signal(false);
const isSavingStorageDir = signal(false);
const storageDirInput = signal("");
const storageDirError = signal("");
const isBrowsingStorageDir = signal(false);
const isStorageDirPickerOpen = signal(false);
const storageDirBrowser = signal<LocalDirectoryBrowseResponse | null>(null);
const storageDirBrowserError = signal("");
const upgradeDialogOpen = signal(false);
const upgradeProgress = signal<UpgradeProgress>({
  status: "idle",
  currentVersion: "",
  hasUpdate: false,
  percent: 0,
  message: "",
});
let upgradeReloadTimer: number | undefined;
const providers = signal<ThirdPartyProvider[]>([]);
const providersLoading = signal(false);
const providersError = signal("");
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
const providerModelEditPlaceholder = signal("");
const providerModelEditError = signal("");
const providerModelEditSaving = signal(false);
const providersChangedEvent = "csghub:providers-changed";
type SettingsTab = "system" | "apiKeys" | "usage";
type UsagePeriod = "week" | "month" | "year";

const activeSettingsTab = signal<SettingsTab>("system");
const localAPIKeys = signal<LocalAPIKeysResponse | null>(null);
const localAPIKeysLoading = signal(false);
const localAPIKeysError = signal("");
const localAPIKeyName = signal("");
const localAPIKeyCreated = signal("");
const localAPIKeySaving = signal(false);
const localAPIKeyDeleting = signal("");
const localAPIUsage = signal<LocalAPIUsageResponse | null>(null);
const localAPIUsageLoading = signal(false);
const localAPIUsageError = signal("");
const localAPIUsagePeriod = signal<UsagePeriod>("week");
const localAPIUsageProvider = signal("");
let localAPIUsageRequestID = 0;
const copiedBaseURL = signal("");
const webSearchEnabled = signal(false);
const webSearchMaxResults = signal(5);
const webSearchLanguage = signal("");
const webSearchSafeSearch = signal(1);
const webSearchTimeoutSeconds = signal(5);
const webSearchError = signal("");
const isSavingWebSearch = signal(false);

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

function notifyProvidersChanged() {
  window.dispatchEvent(new Event(providersChangedEvent));
}

function loadContextIndex(): number {
  try {
    const raw = localStorage.getItem(contextStorageKey);
    const num = Number(raw);
    const idx = contextLengthSteps.indexOf(num);
    if (idx >= 0) return idx;
  } catch {
    /* ignore */
  }
  return 1;
}

function saveContextIndex(idx: number) {
  const value = contextLengthSteps[idx] || contextLengthSteps[1];
  try {
    localStorage.setItem(contextStorageKey, String(value));
  } catch {
    /* ignore */
  }
}

function loadParallelIndex(): number {
  try {
    const raw = localStorage.getItem(parallelStorageKey);
    const num = Number(raw);
    const idx = parallelSteps.indexOf(num);
    if (idx >= 0) return idx;
  } catch {
    /* ignore */
  }
  return 2; // default index for 4
}

function saveParallelIndex(idx: number) {
  const value = parallelSteps[idx] || parallelSteps[2];
  try {
    localStorage.setItem(parallelStorageKey, String(value));
  } catch {
    /* ignore */
  }
}

function resetDefaults() {
  contextIndex.value = 1;
  saveContextIndex(1);
  parallelIndex.value = 2;
  saveParallelIndex(2);
  fetchSettings();
}

function applySettings(data: AppSettings) {
  storageLocation.value = data.storage_dir || "";
  storageDirInput.value = data.storage_dir || "";
  modelDirectory.value = data.model_dir || "";
  datasetDirectory.value = data.dataset_dir || "";
  appVersion.value = data.version || "";
  upgradeProgress.value = {
    ...upgradeProgress.value,
    currentVersion: data.version || upgradeProgress.value.currentVersion,
  };
  autostartEnabled.value = data.autostart ?? false;
  const webSearch = data.web_search;
  webSearchEnabled.value = webSearch?.enabled ?? false;
  webSearchMaxResults.value = webSearch?.max_results || 5;
  webSearchLanguage.value = webSearch?.language || "";
  webSearchSafeSearch.value = webSearch?.safe_search ?? 1;
  webSearchTimeoutSeconds.value = webSearch?.timeout_seconds || 5;
}

function fetchSettings() {
  getSettings()
    .then((data) => {
      applySettings(data);
      storageDirError.value = "";
    })
    .catch(() => {});
}

function fetchCloudAuth() {
  getCloudAuthStatus()
    .then((status) => {
      cloudAuth.value = status;
      cloudAuthError.value = "";
    })
    .catch((err: any) => {
      cloudAuth.value = null;
      cloudAuthError.value = err?.message || "";
    });
}

async function fetchProviders() {
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

async function fetchUpgradeInfo() {
  try {
    const upgrade = await checkUpgrade();
    upgradeProgress.value = {
      ...upgradeProgress.value,
      currentVersion: upgrade.current_version || appVersion.value || "unknown",
      latestVersion: upgrade.latest_version || undefined,
      hasUpdate: !!upgrade.update_available,
    };
    if (upgrade.current_version) {
      appVersion.value = upgrade.current_version;
    }
  } catch {
    upgradeProgress.value = {
      ...upgradeProgress.value,
      currentVersion: appVersion.value || upgradeProgress.value.currentVersion,
    };
  }
}

function displayVersion(version: string): string {
  if (!version) return "...";
  return version.startsWith("v") ? version : `v${version}`;
}

function normalizeVersion(version?: string): string {
  return (version || "").trim().replace(/^v/i, "");
}

function reloadAfterUpgrade() {
  const url = new URL(window.location.href);
  url.searchParams.set("_upgrade", Date.now().toString());
  window.location.replace(url.toString());
}

function reloadWhenUpgraded(expectedVersion?: string) {
  const expected = normalizeVersion(expectedVersion);
  const deadline = Date.now() + upgradeReloadTimeoutMs;

  if (upgradeReloadTimer !== undefined) {
    window.clearTimeout(upgradeReloadTimer);
  }

  const poll = async () => {
    try {
      const settings = await getSettings();
      if (!expected || normalizeVersion(settings.version) === expected) {
        reloadAfterUpgrade();
        return;
      }
    } catch {
      // The server is expected to be briefly unavailable while it restarts.
    }

    if (Date.now() < deadline) {
      upgradeReloadTimer = window.setTimeout(poll, 1000);
    }
  };

  upgradeReloadTimer = window.setTimeout(poll, 2500);
}

function openUpgradeDialog() {
  if (!upgradeProgress.value.hasUpdate) return;
  upgradeProgress.value = { ...upgradeProgress.value, status: "confirming" };
  upgradeDialogOpen.value = true;
}

function doUpgrade() {
  upgradeProgress.value = {
    ...upgradeProgress.value,
    status: "upgrading",
    percent: 0,
    message: t("upgrade.starting"),
    error: undefined,
  };

  upgradeWithProgress((data) => {
    if (data.progress !== undefined) {
      upgradeProgress.value = {
        ...upgradeProgress.value,
        percent: data.progress,
        message: data.message || "",
      };
    }
    if (data.status === "completed") {
      upgradeProgress.value = {
        ...upgradeProgress.value,
        status: "success",
        latestVersion: data.version || upgradeProgress.value.latestVersion,
        percent: 100,
        message: data.message || "",
      };
      reloadWhenUpgraded(data.version || upgradeProgress.value.latestVersion);
      return;
    }
    if (data.status === "error") {
      upgradeProgress.value = {
        ...upgradeProgress.value,
        status: "error",
        error: data.message || t("upgrade.failed"),
      };
      return;
    }
    if (["checking", "downloading", "extracting", "installing"].includes(data.status)) {
      upgradeProgress.value = {
        ...upgradeProgress.value,
        status: "upgrading",
        latestVersion: data.version || upgradeProgress.value.latestVersion,
        message: data.message || upgradeProgress.value.message,
      };
    }
  }).catch(() => {
    if (upgradeProgress.value.status !== "success") {
      upgradeProgress.value = {
        ...upgradeProgress.value,
        status: "error",
        error: t("upgrade.connectionError"),
      };
    }
  });
}

function openExternal(url?: string) {
  if (url) {
    window.open(url, "_blank", "noopener,noreferrer");
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
  providerModelEditDescription.value = "";
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
    if (description) {
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
    await fetchProviders();
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
    await fetchProviders();
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

function defaultProviderModelDisplayName(model: ModelInfo): string {
  return model.display_name || model.label || model.model;
}

function providerModelLabel(model: ModelInfo): string {
  return defaultProviderModelDisplayName(model);
}

function pipelineTagLabel(tag?: string): string {
  switch (tag) {
    case "text-to-image":
      return t("pipeline.imageGeneration");
    case "image-to-image":
      return t("pipeline.imageToImage");
    case "text-to-video":
      return t("pipeline.textToVideo");
    case "image-to-video":
      return t("pipeline.imageToVideo");
    case "video-text-to-text":
      return t("pipeline.videoUnderstanding");
    case "text-to-speech":
      return t("pipeline.textToSpeech");
    case "automatic-speech-recognition":
      return t("pipeline.speechRecognition");
    default:
      return t("pipeline.languageModel");
  }
}

function modalityLabel(modality: string): string {
  switch (modality) {
    case "text":
      return t("pipeline.modalityText");
    case "image":
      return t("pipeline.modalityImage");
    case "video":
      return t("pipeline.modalityVideo");
    case "audio":
    case "speech":
      return t("pipeline.modalityAudio");
    case "file":
      return t("pipeline.modalityFile");
    case "transcription":
      return t("pipeline.modalityText");
    default:
      return modality;
  }
}

function uniqueModalities(values: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of values) {
    const key = value.trim().toLowerCase();
    if (!key || seen.has(key)) continue;
    seen.add(key);
    out.push(key);
  }
  return out;
}

function modelOutputModalities(model: ModelInfo): string[] {
  const outputs = uniqueModalities(model.output_modalities || []);
  if (outputs.length > 0) return outputs;
  switch (model.pipeline_tag) {
    case "text-to-image":
    case "image-to-image":
      return ["image"];
    case "text-to-video":
    case "image-to-video":
      return ["video"];
    case "text-to-speech":
      return ["audio"];
    case "automatic-speech-recognition":
      return ["text"];
    default:
      return ["text"];
  }
}

function modelInputModalities(model: ModelInfo): string[] {
  return uniqueModalities(model.input_modalities || []).filter((item) => item !== "text");
}

function ProviderModelModalityBadges({
  model,
  showPipelineTag = false,
  showInputs = false,
  showOutputs = true,
  compact = false,
}: {
  model: ModelInfo;
  showPipelineTag?: boolean;
  showInputs?: boolean;
  showOutputs?: boolean;
  compact?: boolean;
}) {
  const pill = compact ? "rounded px-1.5 py-0.5 text-[10px]" : "rounded-full px-2 py-0.5 text-[11px]";
  const inputs = showInputs ? modelInputModalities(model) : [];
  const outputs = showOutputs ? modelOutputModalities(model) : [];
  if (!showPipelineTag && inputs.length === 0 && outputs.length === 0) return null;
  return (
    <span class={`flex min-w-0 flex-wrap items-center gap-1 ${compact ? "" : "gap-1.5"}`}>
      {showPipelineTag && (
        <span class={`${pill} bg-gray-100 text-gray-600`}>
          {pipelineTagLabel(model.pipeline_tag)}
        </span>
      )}
      {inputs.map((item) => (
        <span key={`in-${item}`} class={`${pill} bg-blue-50 text-blue-700`}>
          {t("pipeline.inputCapability", modalityLabel(item))}
        </span>
      ))}
      {outputs.map((item) => (
        <span key={`out-${item}`} class={`${pill} bg-violet-50 text-violet-700`}>
          {t("pipeline.outputCapability", modalityLabel(item))}
        </span>
      ))}
    </span>
  );
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

async function saveStorageDir() {
  const newDir = storageDirInput.value.trim();
  if (!newDir) return;

  isSavingStorageDir.value = true;
  storageDirError.value = "";
  try {
    const data = await saveSettings({ storage_dir: newDir });
    applySettings(data);
  } catch (err: any) {
    storageDirError.value = err?.message || t("settings.storageDirSaveFailed");
  } finally {
    isSavingStorageDir.value = false;
  }
}

async function saveWebSearchSettings() {
  const settings: WebSearchSettings = {
    enabled: webSearchEnabled.value,
    max_results: Math.max(1, Math.min(10, Number(webSearchMaxResults.value) || 5)),
    language: webSearchLanguage.value.trim() || undefined,
    safe_search: Math.max(0, Math.min(2, Number(webSearchSafeSearch.value) || 0)),
    timeout_seconds: Math.max(1, Math.min(30, Number(webSearchTimeoutSeconds.value) || 5)),
  };

  isSavingWebSearch.value = true;
  webSearchError.value = "";
  try {
    const data = await saveSettings({ web_search: settings });
    applySettings(data);
  } catch (err: any) {
    webSearchError.value = err?.message || t("settings.webSearchSaveFailed");
  } finally {
    isSavingWebSearch.value = false;
  }
}

async function browseStorageDir(path?: string) {
  isBrowsingStorageDir.value = true;
  storageDirBrowserError.value = "";
  try {
    storageDirBrowser.value = await browseLocalDirectories(path);
  } catch (err: any) {
    storageDirBrowserError.value = err?.message || t("settings.directoryBrowseFailed");
  } finally {
    isBrowsingStorageDir.value = false;
  }
}

function openStorageDirPicker() {
  isStorageDirPickerOpen.value = true;
  void browseStorageDir(storageLocation.value || storageDirInput.value);
}

function closeStorageDirPicker() {
  isStorageDirPickerOpen.value = false;
  storageDirBrowserError.value = "";
}

function selectStorageDir(path: string) {
  storageDirInput.value = path;
  storageDirError.value = "";
  closeStorageDirPicker();
}

function cloudUserLabel(status: CloudAuthStatus | null): string {
  const user = status?.user;
  return (user?.nickname || user?.username || "").trim();
}

function cloudUserInitial(status: CloudAuthStatus | null): string {
  const label = cloudUserLabel(status);
  return label ? label[0].toUpperCase() : "?";
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

function localAPIOrigin(): string {
  return window.location.origin;
}

function copySettingsSnippet(value: string) {
  void navigator.clipboard?.writeText(value);
  copiedBaseURL.value = value;
  window.setTimeout(() => {
    if (copiedBaseURL.value === value) {
      copiedBaseURL.value = "";
    }
  }, 1500);
}

function selectLocalAPIUsagePeriod(period: UsagePeriod) {
  localAPIUsagePeriod.value = period;
  void fetchLocalAPIUsage(period);
}

function selectLocalAPIUsageProvider(provider: string) {
  localAPIUsageProvider.value = provider;
  void fetchLocalAPIUsage(localAPIUsagePeriod.value, provider);
}

export function Settings() {
  void locale.value;
  const showTokenInput = !(cloudAuth.value?.authenticated && cloudAuth.value?.user);

  useEffect(() => {
    fetchSettings();
    fetchCloudAuth();
    void fetchProviders();
    void fetchLocalAPIKeys();
    void fetchLocalAPIUsage();
    void fetchUpgradeInfo();
    contextIndex.value = loadContextIndex();
    parallelIndex.value = loadParallelIndex();
  }, []);

  const handleOpenCloudLogin = () => {
    openExternal(cloudAuth.value?.login_url);
  };

  const handleOpenCloudTokenPage = () => {
    openExternal(cloudAuth.value?.access_token_url);
  };

  const handleOpenCloudAPIKeyPage = () => {
    openExternal("https://opencsg.com/settings/api-keys");
  };

  const handleLogout = async () => {
    if (isClearingCloudToken.value) return;
    isClearingCloudToken.value = true;
    cloudAuthError.value = "";
    try {
      cloudAuth.value = await clearCloudToken();
    } catch (err: any) {
      cloudAuthError.value = err?.message || t("chat.failedResp");
    } finally {
      isClearingCloudToken.value = false;
    }
  };

  const handleSaveCloudToken = async () => {
    const token = cloudTokenInput.value.trim();
    if (!token) {
      cloudAuthError.value = t("chat.cloudTokenEmpty");
      return;
    }

    isSavingCloudToken.value = true;
    cloudAuthError.value = "";
    try {
      const status = await saveCloudToken(token);
      cloudAuth.value = status;
      if (!status.authenticated) {
        cloudAuthError.value = t("chat.cloudLoginExpired");
        return;
      }
      cloudTokenInput.value = "";
    } catch (err: any) {
      cloudAuthError.value = err?.message || t("chat.failedResp");
    } finally {
      isSavingCloudToken.value = false;
    }
  };

  const handleSaveCloudAPIKey = async () => {
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
  };

  const handleClearCloudAPIKey = async () => {
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
  };

  return (
    <div class="mx-auto max-w-6xl p-8">
      <h1 class="text-2xl font-bold text-gray-900">{t("settings.title")}</h1>
      <p class="text-gray-500 text-sm mt-1 mb-6">{t("settings.subtitle")}</p>

      <div class="mb-10 flex flex-wrap gap-2 rounded-xl border border-gray-200 bg-white p-1">
        <SettingsTabButton tab="system" label={t("settings.tabSystem")} />
        <SettingsTabButton tab="apiKeys" label={t("settings.tabAPIKeys")} />
        <SettingsTabButton tab="usage" label={t("settings.tabUsage")} />
      </div>

          {activeSettingsTab.value === "system" && (
            <>
      {/* Storage location */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.modelLocation")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-3 ml-7">{t("settings.modelLocationDesc")}</p>
        <div class="ml-7 flex flex-col sm:flex-row gap-3">
          <input
            type="text"
            spellcheck={false}
            class="flex-1 rounded-lg border border-gray-200 px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            value={storageDirInput.value}
            onInput={(e) => (storageDirInput.value = (e.target as HTMLInputElement).value)}
          />
          <button
            onClick={openStorageDirPicker}
            disabled={isBrowsingStorageDir.value}
            class="px-4 py-2 border border-gray-200 rounded-lg text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
          >
            {isBrowsingStorageDir.value ? "..." : t("settings.browse")}
          </button>
          <button
            onClick={() => void saveStorageDir()}
            disabled={isSavingStorageDir.value || !storageDirInput.value.trim() || storageDirInput.value.trim() === storageLocation.value}
            class="px-4 py-2 border border-indigo-200 rounded-lg text-sm text-indigo-700 hover:bg-indigo-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
          >
            {isSavingStorageDir.value ? "..." : t("settings.save")}
          </button>
        </div>
        <div class="ml-7 mt-3 space-y-1 text-xs text-gray-500">
          <p>{t("settings.modelsPath", modelDirectory.value || "...")}</p>
          <p>{t("settings.datasetsPath", datasetDirectory.value || "...")}</p>
        </div>
        {storageDirError.value && (
          <p class="mt-3 ml-7 text-sm text-red-600">{storageDirError.value}</p>
        )}
      </div>

      {/* Context length */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.contextLength")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-4 ml-7">{t("settings.contextLengthDesc")}</p>
        <div class="ml-7">
          <input
            type="range"
            min="0"
            max={contextLengthSteps.length - 1}
            step="1"
            value={contextIndex.value}
            onInput={(e) => {
              const idx = Number((e.target as HTMLInputElement).value);
              contextIndex.value = idx;
              saveContextIndex(idx);
            }}
            class="w-full h-1.5 bg-gray-200 rounded-full appearance-none cursor-pointer accent-indigo-600"
          />
          <div class="flex justify-between mt-2">
            {contextLengthLabels.map((label) => (
              <span key={label} class="text-xs text-gray-400">{label}</span>
            ))}
          </div>
        </div>
      </div>

      {/* Parallel slots */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M4 6h16M4 12h16M4 18h16" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.parallelSlots")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-4 ml-7">{t("settings.parallelSlotsDesc")}</p>
        <div class="ml-7">
          <input
            type="range"
            min="0"
            max={parallelSteps.length - 1}
            step="1"
            value={parallelIndex.value}
            onInput={(e) => {
              const idx = Number((e.target as HTMLInputElement).value);
              parallelIndex.value = idx;
              saveParallelIndex(idx);
            }}
            class="w-full h-1.5 bg-gray-200 rounded-full appearance-none cursor-pointer accent-indigo-600"
          />
          <div class="flex justify-between mt-2">
            {parallelLabels.map((label) => (
              <span key={label} class="text-xs text-gray-400">{label}</span>
            ))}
          </div>
        </div>
      </div>

      {/* Language */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M3 5h12M9 3v2m1.048 9.5A18.022 18.022 0 016.412 9m6.088 9h7M11 21l5-10 5 10M12.751 5C11.783 10.77 8.07 15.61 3 18.129" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.language")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-3 ml-7">{t("settings.languageDesc")}</p>
        <div class="flex gap-2 ml-7">
          <LangBtn code="en" label="EN" />
          <LangBtn code="zh" label="中文" />
        </div>
      </div>

      {/* Web search */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 3a9 9 0 100 18 9 9 0 000-18z" />
            <path stroke-linecap="round" stroke-linejoin="round" d="M3.6 9h16.8M3.6 15h16.8M12 3c2 2.3 3 5.3 3 9s-1 6.7-3 9c-2-2.3-3-5.3-3-9s1-6.7 3-9z" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.webSearch")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-3 ml-7">{t("settings.webSearchDesc")}</p>
        <div class="ml-7 rounded-xl border border-gray-200 bg-white p-4">
          <div class="mb-4 flex items-center gap-3">
            <label class="relative inline-flex items-center cursor-pointer">
              <input
                type="checkbox"
                checked={webSearchEnabled.value}
                disabled={isSavingWebSearch.value}
                onChange={(e) => (webSearchEnabled.value = (e.target as HTMLInputElement).checked)}
                class="sr-only peer"
              />
              <div class="w-11 h-6 bg-gray-200 peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-indigo-300 rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-indigo-600 peer-disabled:opacity-60 peer-disabled:cursor-not-allowed"></div>
            </label>
            <span class="text-sm text-gray-700">
              {webSearchEnabled.value ? t("settings.webSearchOn") : t("settings.webSearchOff")}
            </span>
          </div>
          <div class="mb-3 rounded-lg border border-blue-100 bg-blue-50 px-3 py-2 text-sm text-blue-700">
            {t("settings.webSearchEngines")}
          </div>
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="text-sm font-medium text-gray-700">
              {t("settings.webSearchMaxResults")}
              <input
                type="number"
                min={1}
                max={10}
                class="mt-2 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={webSearchMaxResults.value}
                onInput={(e) => (webSearchMaxResults.value = Number((e.target as HTMLInputElement).value))}
              />
            </label>
            <label class="text-sm font-medium text-gray-700">
              {t("settings.webSearchTimeout")}
              <input
                type="number"
                min={1}
                max={30}
                class="mt-2 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={webSearchTimeoutSeconds.value}
                onInput={(e) => (webSearchTimeoutSeconds.value = Number((e.target as HTMLInputElement).value))}
              />
            </label>
            <label class="text-sm font-medium text-gray-700">
              {t("settings.webSearchLanguage")}
              <input
                type="text"
                class="mt-2 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder="auto"
                value={webSearchLanguage.value}
                onInput={(e) => (webSearchLanguage.value = (e.target as HTMLInputElement).value)}
              />
            </label>
            <label class="text-sm font-medium text-gray-700">
              {t("settings.webSearchSafeSearch")}
              <select
                class="mt-2 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={webSearchSafeSearch.value}
                onChange={(e) => (webSearchSafeSearch.value = Number((e.target as HTMLSelectElement).value))}
              >
                <option value={0}>{t("settings.webSearchSafeOff")}</option>
                <option value={1}>{t("settings.webSearchSafeModerate")}</option>
                <option value={2}>{t("settings.webSearchSafeStrict")}</option>
              </select>
            </label>
          </div>
          {webSearchError.value && <p class="mt-3 text-sm text-red-600">{webSearchError.value}</p>}
          <div class="mt-4 flex justify-end">
            <button
              onClick={() => void saveWebSearchSettings()}
              disabled={isSavingWebSearch.value}
              class="px-4 py-2 border border-indigo-200 rounded-lg text-sm text-indigo-700 hover:bg-indigo-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
            >
              {isSavingWebSearch.value ? "..." : t("settings.save")}
            </button>
          </div>
        </div>
      </div>

      {/* Autostart */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M5.636 18.364a9 9 0 010-12.728m12.728 0a9 9 0 010 12.728M12 2v2m0 16v2" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.autostart")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-3 ml-7">{t("settings.autostartDesc")}</p>
        <div class="ml-7 flex items-center gap-3">
          <label class="relative inline-flex items-center cursor-pointer">
            <input
              type="checkbox"
              checked={autostartEnabled.value}
              disabled={isSavingAutostart.value}
              onChange={async (e) => {
                const enabled = (e.target as HTMLInputElement).checked;
                isSavingAutostart.value = true;
                try {
                  const data = await saveSettings({ autostart: enabled });
                  applySettings(data);
                } catch {
                  autostartEnabled.value = !enabled;
                } finally {
                  isSavingAutostart.value = false;
                }
              }}
              class="sr-only peer"
            />
            <div class="w-11 h-6 bg-gray-200 peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-indigo-300 rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-indigo-600 peer-disabled:opacity-60 peer-disabled:cursor-not-allowed"></div>
          </label>
          <span class="text-sm text-gray-700">
            {autostartEnabled.value ? t("settings.autostartOn") : t("settings.autostartOff")}
          </span>
        </div>
      </div>

      {/* Account */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M5.121 17.804A9 9 0 1118.88 17.8M15 11a3 3 0 11-6 0 3 3 0 016 0z" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.account")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-3 ml-7">{t("settings.accountDesc")}</p>
        <div class="ml-7 rounded-xl border border-gray-200 bg-white p-4">
          {cloudAuth.value === null ? (
            <p class="text-sm text-gray-500">...</p>
          ) : cloudAuth.value.authenticated && cloudAuth.value.user ? (
            <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
              <div class="flex items-center gap-4 min-w-0">
                {cloudAuth.value.user.avatar ? (
                  <img
                    src={cloudAuth.value.user.avatar}
                    alt={cloudUserLabel(cloudAuth.value)}
                    class="w-12 h-12 rounded-full border border-gray-200 object-cover bg-gray-50"
                  />
                ) : (
                  <div class="w-12 h-12 rounded-full bg-indigo-50 text-indigo-700 flex items-center justify-center text-lg font-semibold">
                    {cloudUserInitial(cloudAuth.value)}
                  </div>
                )}
                <div class="min-w-0">
                  <p class="text-sm font-semibold text-gray-900 truncate">{cloudUserLabel(cloudAuth.value)}</p>
                  <p class="text-sm text-gray-500 truncate">@{cloudAuth.value.user.username}</p>
                  {cloudAuth.value.user.email && (
                    <p class="text-sm text-gray-500 truncate">{cloudAuth.value.user.email}</p>
                  )}
                </div>
              </div>
              <div class="flex gap-2">
                <button
                  onClick={handleOpenCloudTokenPage}
                  class="px-4 py-2 border border-gray-200 rounded-lg text-sm text-gray-700 hover:bg-gray-50 transition-colors"
                >
                  {t("settings.openTokenPage")}
                </button>
                <button
                  onClick={handleLogout}
                  disabled={isClearingCloudToken.value}
                  class="px-4 py-2 border border-red-200 rounded-lg text-sm text-red-600 hover:bg-red-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
                >
                  {isClearingCloudToken.value ? t("settings.loggingOut") : t("settings.logout")}
                </button>
              </div>
            </div>
          ) : cloudAuth.value.has_token ? (
            <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <p class="text-sm font-semibold text-gray-900">{t("settings.tokenSaved")}</p>
                <p class="text-sm text-gray-500">{t("settings.tokenSavedDesc")}</p>
              </div>
              <div class="flex gap-2">
                <button
                  onClick={handleOpenCloudLogin}
                  class="px-4 py-2 border border-gray-200 rounded-lg text-sm text-gray-700 hover:bg-gray-50 transition-colors"
                >
                  {t("settings.login")}
                </button>
                <button
                  onClick={handleLogout}
                  disabled={isClearingCloudToken.value}
                  class="px-4 py-2 border border-red-200 rounded-lg text-sm text-red-600 hover:bg-red-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
                >
                  {isClearingCloudToken.value ? t("settings.loggingOut") : t("settings.logout")}
                </button>
              </div>
            </div>
          ) : (
            <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <p class="text-sm font-semibold text-gray-900">{t("settings.loggedOut")}</p>
                <p class="text-sm text-gray-500">{t("settings.loggedOutDesc")}</p>
              </div>
              <div class="flex gap-2">
                <button
                  onClick={handleOpenCloudLogin}
                  class="px-4 py-2 border border-gray-200 rounded-lg text-sm text-gray-700 hover:bg-gray-50 transition-colors"
                >
                  {t("settings.login")}
                </button>
                <button
                  onClick={handleOpenCloudTokenPage}
                  class="px-4 py-2 border border-gray-200 rounded-lg text-sm text-gray-700 hover:bg-gray-50 transition-colors"
                >
                  {t("settings.openTokenPage")}
                </button>
              </div>
            </div>
          )}
          {showTokenInput && (
            <div class="mt-5 border-t border-gray-100 pt-5">
              <label class="mb-2 block text-sm font-medium text-gray-700">{t("chat.cloudTokenLabel")}</label>
              <p class="mb-3 text-sm text-gray-500">{t("settings.tokenInputHint")}</p>
              <div class="flex flex-col gap-3 sm:flex-row sm:items-end">
                <div class="flex-1">
                  <input
                    type="password"
                    autoComplete="off"
                    spellcheck={false}
                    class="w-full rounded-lg border border-gray-200 px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                    placeholder={t("chat.cloudTokenPlaceholder")}
                    value={cloudTokenInput.value}
                    onInput={(e) => (cloudTokenInput.value = (e.target as HTMLInputElement).value)}
                  />
                </div>
                <button
                  onClick={handleSaveCloudToken}
                  disabled={isSavingCloudToken.value}
                  class="px-4 py-2 border border-indigo-200 rounded-lg text-sm text-indigo-700 hover:bg-indigo-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
                >
                  {isSavingCloudToken.value ? t("chat.cloudSavingToken") : t("chat.cloudSaveToken")}
                </button>
              </div>
            </div>
          )}
          {cloudAuthError.value && (
            <p class="mt-3 text-sm text-red-600">{cloudAuthError.value}</p>
          )}
        </div>
      </div>
            </>
          )}

          {activeSettingsTab.value === "apiKeys" && (
            <>
      <LocalAPIKeysSection />

      {/* OpenCSG API Key */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 7.5a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0zM4.5 20.25a8.25 8.25 0 1115 0" />
            <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 14.25l1.5 1.5 3-3" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.cloudAPIKey")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-3 ml-7">{t("settings.cloudAPIKeyDesc")}</p>
        <div class="ml-7 rounded-xl border border-gray-200 bg-white p-4">
          <div class="mb-3 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2">
            <div class="text-xs font-medium uppercase tracking-wide text-gray-400">{t("chat.cloudGatewayLabel")}</div>
            <div class="mt-1 text-sm font-medium text-gray-800">{t("chat.cloudGatewayValue")}</div>
          </div>
          <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <p class="text-sm font-semibold text-gray-900">{cloudAPIKeyStatus(cloudAuth.value)}</p>
              <p class="mt-1 text-sm text-gray-500">{t("settings.cloudAPIKeyStatusHint")}</p>
            </div>
            <div class="flex gap-2">
              <button
                onClick={handleOpenCloudAPIKeyPage}
                class="px-4 py-2 border border-gray-200 rounded-lg text-sm text-gray-700 hover:bg-gray-50 transition-colors"
              >
                {t("settings.openAPIKeyPage")}
              </button>
              {hasManualCloudAPIKey(cloudAuth.value) && (
                <button
                  onClick={handleClearCloudAPIKey}
                  disabled={isClearingCloudAPIKey.value}
                  class="px-4 py-2 border border-red-200 rounded-lg text-sm text-red-600 hover:bg-red-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
                >
                  {isClearingCloudAPIKey.value ? t("settings.clearingAPIKey") : t("settings.clearAPIKey")}
                </button>
              )}
            </div>
          </div>
          <div class="mt-5 border-t border-gray-100 pt-5">
            <label class="mb-2 block text-sm font-medium text-gray-700">{t("chat.cloudApiKeyLabel")}</label>
            <p class="mb-3 text-sm text-gray-500">{t("settings.cloudAPIKeyInputHint")}</p>
            <div class="flex flex-col gap-3 sm:flex-row sm:items-end">
              <div class="flex-1">
                <input
                  type="password"
                  autoComplete="off"
                  spellcheck={false}
                  class="w-full rounded-lg border border-gray-200 px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  placeholder={t("chat.cloudApiKeyPlaceholder")}
                  value={cloudAPIKeyInput.value}
                  onInput={(e) => (cloudAPIKeyInput.value = (e.target as HTMLInputElement).value)}
                />
              </div>
              <button
                onClick={handleSaveCloudAPIKey}
                disabled={isSavingCloudAPIKey.value}
                class="px-4 py-2 border border-indigo-200 rounded-lg text-sm text-indigo-700 hover:bg-indigo-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
              >
                {isSavingCloudAPIKey.value ? t("chat.cloudApiKeySaving") : t("chat.cloudApiKeySave")}
              </button>
            </div>
          </div>
          {cloudAuth.value?.api_key_error && (
            <p class="mt-3 text-sm text-amber-700">{cloudAuth.value.api_key_error}</p>
          )}
          {cloudAPIKeyError.value && (
            <p class="mt-3 text-sm text-red-600">{cloudAPIKeyError.value}</p>
          )}
        </div>
      </div>

      {/* Third-party providers */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M13.5 10.5V6.75A2.25 2.25 0 0011.25 4.5h-4.5A2.25 2.25 0 004.5 6.75v10.5A2.25 2.25 0 006.75 19.5h10.5a2.25 2.25 0 002.25-2.25v-4.5a2.25 2.25 0 00-2.25-2.25H13.5z" />
            <path stroke-linecap="round" stroke-linejoin="round" d="M13.5 4.5h6m0 0v6m0-6L10.5 13.5" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.providers")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-3 ml-7">{t("settings.providersDesc")}</p>
        <div class="ml-7 rounded-xl border border-gray-200 bg-white p-4">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-sm font-semibold text-gray-900">{t("settings.providersConfigured", providers.value.length)}</p>
              <p class="mt-1 text-sm text-gray-500">{t("settings.providersHint")}</p>
            </div>
            <button
              type="button"
              onClick={() => openProviderDialog()}
              class="px-4 py-2 border border-indigo-200 rounded-lg text-sm text-indigo-700 hover:bg-indigo-50 transition-colors"
            >
              {t("settings.providerAdd")}
            </button>
          </div>
          {providersError.value && <p class="mt-3 text-sm text-red-600">{providersError.value}</p>}
          <div class="mt-4 space-y-2">
            {providersLoading.value ? (
              <p class="text-sm text-gray-500">...</p>
            ) : providers.value.length === 0 ? (
              <p class="text-sm text-gray-400">{t("settings.providersEmpty")}</p>
            ) : (
              providers.value.map((provider) => (
                <div key={provider.id} class="flex flex-col gap-3 rounded-lg border border-gray-100 px-3 py-3 sm:flex-row sm:items-center sm:justify-between">
                  <div class="min-w-0">
                    <p class="text-sm font-medium text-gray-900 truncate">{provider.name}</p>
                    <p class="text-xs text-gray-500 truncate">{provider.base_url}</p>
                    <p class="mt-1 text-[11px] uppercase tracking-wide text-gray-400">{provider.provider || "openai"}</p>
                    <div class="mt-2">
                      {(providerSelectedModels.value[provider.id] || []).length === 0 ? (
                        <span class="text-xs text-gray-400">{t("settings.providerModelsNoneSelected")}</span>
                      ) : (
                        <div class="grid max-h-40 grid-cols-2 gap-1.5 overflow-y-auto pr-1">
                          {(providerSelectedModels.value[provider.id] || []).map((model) => (
                            <div key={model.model} class="min-w-0 rounded-md bg-indigo-50 px-2 py-1">
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
                                  class="shrink-0 rounded border border-indigo-100 bg-white/70 p-1 text-indigo-700 hover:bg-white transition-colors"
                                >
                                  <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                                    <path stroke-linecap="round" stroke-linejoin="round" d="M16.862 4.487l1.687-1.688a1.875 1.875 0 112.652 2.652L10.582 16.07a4.5 4.5 0 01-1.897 1.13L6 18l.8-2.685a4.5 4.5 0 011.13-1.897l8.932-8.931z" />
                                    <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 7.125L16.875 4.5" />
                                  </svg>
                                </button>
                              </div>
                              <div class="mt-0.5">
                                <ProviderModelModalityBadges model={model} showOutputs compact />
                              </div>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  </div>
                  <div class="flex items-center gap-2">
                    <label class="relative inline-flex items-center cursor-pointer">
                      <input
                        type="checkbox"
                        checked={provider.enabled}
                        onChange={() => void toggleProviderEnabled(provider)}
                        class="sr-only peer"
                      />
                      <div class="w-9 h-5 bg-gray-200 peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-indigo-300 rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-indigo-600"></div>
                    </label>
                    <button
                      type="button"
                      onClick={() => openProviderDialog(provider)}
                      class="px-3 py-1.5 border border-gray-200 rounded-lg text-xs text-gray-700 hover:bg-gray-50 transition-colors"
                    >
                      {t("settings.providerEdit")}
                    </button>
                    <button
                      type="button"
                      onClick={() => void removeProvider(provider)}
                      class="px-3 py-1.5 border border-red-200 rounded-lg text-xs text-red-600 hover:bg-red-50 transition-colors"
                    >
                      {t("settings.providerDelete")}
                    </button>
                  </div>
                </div>
              ))
            )}
          </div>
        </div>
      </div>
            </>
          )}

          {activeSettingsTab.value === "system" && (
            <>
      {/* API docs */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.253v13m0-13C10.832 5.477 8.955 5 7 5a4 4 0 00-4 4v9a4 4 0 014-4c1.955 0 3.832.477 5 1.253m0-9C13.168 5.477 15.045 5 17 5a4 4 0 014 4v9a4 4 0 00-4-4c-1.955 0-3.832.477-5 1.253" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.apiDocs")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-3 ml-7">{t("settings.apiDocsDesc")}</p>
        <div class="ml-7 rounded-xl border border-gray-200 bg-white p-4">
          <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <p class="text-sm font-semibold text-gray-900">{t("settings.apiDocsTitle")}</p>
              <p class="mt-1 text-sm text-gray-500">{t("settings.apiDocsHint")}</p>
            </div>
            <a
              href="/api-docs.html"
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex items-center justify-center px-4 py-2 border border-indigo-200 rounded-lg text-sm text-indigo-700 hover:bg-indigo-50 transition-colors"
            >
              {t("settings.openApiDocs")}
            </a>
          </div>
        </div>
      </div>

      {/* Version information */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.versionInfo")}</span>
        </div>
        <div class="ml-7 mt-3 rounded-xl border border-gray-200 bg-white p-4">
          <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <p class="text-sm font-semibold text-gray-900">{displayVersion(upgradeProgress.value.currentVersion || appVersion.value)}</p>
              <p class="mt-1 text-sm text-gray-500">
                {upgradeProgress.value.hasUpdate && upgradeProgress.value.latestVersion
                  ? t("upgrade.available", displayVersion(upgradeProgress.value.latestVersion))
                  : t("upgrade.upToDate")}
              </p>
            </div>
            <button
              type="button"
              onClick={openUpgradeDialog}
              disabled={!upgradeProgress.value.hasUpdate}
              class="px-4 py-2 border border-indigo-200 rounded-lg text-sm text-indigo-700 hover:bg-indigo-50 disabled:border-gray-200 disabled:text-gray-400 disabled:opacity-70 disabled:cursor-not-allowed transition-colors"
            >
              {t("upgrade.upgrade")}
            </button>
          </div>
        </div>
      </div>

      <div class="flex justify-end border-t border-gray-100 pt-6">
        <button
          onClick={resetDefaults}
          class="px-4 py-2 border border-gray-200 rounded-lg text-sm text-gray-700 hover:bg-gray-50 transition-colors"
        >
          {t("settings.resetDefaults")}
        </button>
      </div>
            </>
          )}

          {activeSettingsTab.value === "usage" && <UsageStatisticsSection />}

      <DirectoryPickerDialog
        open={isStorageDirPickerOpen.value}
        loading={isBrowsingStorageDir.value}
        data={storageDirBrowser.value}
        error={storageDirBrowserError.value}
        onClose={closeStorageDirPicker}
        onBrowse={(path) => void browseStorageDir(path)}
        onSelect={selectStorageDir}
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
      <UpgradeDialog
        open={upgradeDialogOpen.value}
        progress={upgradeProgress.value}
        onConfirm={doUpgrade}
        onClose={() => {
          upgradeDialogOpen.value = false;
          if (upgradeProgress.value.status !== "upgrading") {
            upgradeProgress.value = { ...upgradeProgress.value, status: "idle" };
          }
        }}
      />
    </div>
  );
}

function SettingsTabButton({ tab, label }: { tab: SettingsTab; label: string }) {
  const active = activeSettingsTab.value === tab;
  return (
    <button
      type="button"
      onClick={() => (activeSettingsTab.value = tab)}
      class={`flex-1 rounded-lg px-4 py-2 text-center text-sm transition-colors ${
        active
          ? "bg-indigo-50 text-indigo-700 font-medium"
          : "text-gray-600 hover:bg-gray-50"
      }`}
    >
      {label}
    </button>
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
    <div class="mb-10">
      <div class="flex items-center gap-2 mb-1">
        <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 5.25a3 3 0 11-4.243 4.243L6 15v3h3l5.507-5.507A3 3 0 0115.75 5.25z" />
        </svg>
        <span class="font-semibold text-gray-900">{t("settings.localAPIKeys")}</span>
      </div>
      <p class="text-sm text-gray-500 mb-3 ml-7">{t("settings.localAPIKeysDesc")}</p>
      <div class="ml-7 rounded-xl border border-gray-200 bg-white p-4">
        <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p class="text-sm font-semibold text-gray-900">{t("settings.localAPIAuth")}</p>
            <p class="mt-1 text-sm text-gray-500">{t("settings.localAPIAuthDesc")}</p>
          </div>
          <div class="flex items-center gap-3">
            <label class="relative inline-flex items-center cursor-pointer">
              <input
                type="checkbox"
                checked={authEnabled}
                disabled={localAPIKeySaving.value}
                onChange={(e) => void toggleLocalAPIAuth((e.target as HTMLInputElement).checked)}
                class="sr-only peer"
              />
              <div class="w-11 h-6 bg-gray-200 peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-indigo-300 rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-indigo-600 peer-disabled:opacity-60 peer-disabled:cursor-not-allowed"></div>
            </label>
            <span class="text-sm text-gray-700">{authEnabled ? t("settings.localAPIAuthOn") : t("settings.localAPIAuthOff")}</span>
          </div>
        </div>

        <div class="mt-5 border-t border-gray-100 pt-5">
          <div class="flex flex-col gap-3 sm:flex-row sm:items-end">
            <div class="flex-1">
              <label class="mb-1 block text-sm font-medium text-gray-700">{t("settings.localAPIKeyName")}</label>
              <input
                class="w-full rounded-lg border border-gray-200 px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                value={localAPIKeyName.value}
                onInput={(e) => (localAPIKeyName.value = (e.target as HTMLInputElement).value)}
                placeholder={t("settings.localAPIKeyNamePlaceholder")}
              />
            </div>
            <button
              type="button"
              onClick={() => void createLocalKey()}
              disabled={localAPIKeySaving.value}
              class="px-4 py-2 border border-indigo-200 rounded-lg text-sm text-indigo-700 hover:bg-indigo-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
            >
              {localAPIKeySaving.value ? "..." : t("settings.localAPIKeyCreate")}
            </button>
          </div>
          {localAPIKeyCreated.value && (
            <div class="mt-4 rounded-lg border border-amber-200 bg-amber-50 p-3">
              <p class="text-sm font-medium text-amber-900">{t("settings.localAPIKeyCreated")}</p>
              <input
                readOnly
                class="mt-2 w-full rounded-lg border border-amber-200 bg-white px-3 py-2 text-sm font-mono text-amber-900"
                value={localAPIKeyCreated.value}
                onFocus={(e) => (e.target as HTMLInputElement).select()}
              />
              <p class="mt-2 text-xs text-amber-800">{t("settings.localAPIKeyCreatedHint")}</p>
            </div>
          )}
        </div>

        {localAPIKeysError.value && <p class="mt-3 text-sm text-red-600">{localAPIKeysError.value}</p>}
        <div class="mt-5 space-y-2 border-t border-gray-100 pt-5">
          {localAPIKeysLoading.value ? (
            <p class="text-sm text-gray-500">...</p>
          ) : keys.length === 0 ? (
            <p class="text-sm text-gray-400">{t("settings.localAPIKeysEmpty")}</p>
          ) : (
            keys.map((key) => (
              <div key={key.id} class="flex flex-col gap-3 rounded-lg border border-gray-100 px-3 py-3 sm:flex-row sm:items-center sm:justify-between">
                <div class="min-w-0">
                  <p class="text-sm font-medium text-gray-900 truncate">{key.name}</p>
                  <p class="text-xs text-gray-500">{t("settings.localAPIKeyPrefix", key.prefix)}</p>
                  <p class="text-xs text-gray-400">{t("settings.localAPIKeyLastUsed", formatDateTime(key.last_used_at))}</p>
                </div>
                <button
                  type="button"
                  onClick={() => void removeLocalKey(key.id)}
                  disabled={localAPIKeyDeleting.value === key.id}
                  class="px-3 py-1.5 border border-red-200 rounded-lg text-xs text-red-600 hover:bg-red-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
                >
                  {localAPIKeyDeleting.value === key.id ? "..." : t("settings.localAPIKeyDelete")}
                </button>
              </div>
            ))
          )}
        </div>
      </div>

      <div class="ml-7 mt-4 rounded-xl border border-gray-200 bg-white p-4">
        <p class="text-sm font-semibold text-gray-900">{t("settings.localAPIBaseURL")}</p>
        <p class="mt-1 text-sm text-gray-500">{t("settings.localAPIBaseURLDesc")}</p>
        <div class="mt-4 space-y-3">
          <BaseURLRow label={t("settings.localAPIBaseURLOpenAI")} value={openAIBaseURL} />
          <BaseURLRow label={t("settings.localAPIBaseURLAnthropic")} value={anthropicBaseURL} />
        </div>
        <div class="mt-5 border-t border-gray-100 pt-5">
          <p class="text-sm font-semibold text-gray-900">{t("settings.localAPIAccessMethod")}</p>
          <div class="mt-3 space-y-3">
            <CurlExample label={t("settings.localAPIBaseURLOpenAI")} value={openAICurl} />
            <CurlExample label={t("settings.localAPIBaseURLAnthropic")} value={anthropicCurl} />
          </div>
        </div>
      </div>
    </div>
  );
}

function BaseURLRow({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div class="text-xs font-medium uppercase tracking-wide text-gray-400">{label}</div>
      <div class="mt-1 flex gap-2">
        <input
          readOnly
          class="min-w-0 flex-1 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm font-mono text-gray-800"
          value={value}
          onFocus={(e) => (e.target as HTMLInputElement).select()}
        />
        <button
          type="button"
          onClick={() => copySettingsSnippet(value)}
          class="shrink-0 rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 transition-colors"
        >
          {copiedBaseURL.value === value ? t("settings.copied") : t("settings.copy")}
        </button>
      </div>
    </div>
  );
}

function CurlExample({ label, value }: { label: string; value: string }) {
  return (
    <div class="rounded-lg border border-gray-100 bg-gray-50 p-3">
      <div class="mb-2 flex items-center justify-between gap-3">
        <span class="text-xs font-medium uppercase tracking-wide text-gray-400">{label}</span>
        <button
          type="button"
          onClick={() => copySettingsSnippet(value)}
          class="shrink-0 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-xs text-gray-700 hover:bg-gray-50 transition-colors"
        >
          {copiedBaseURL.value === value ? t("settings.copied") : t("settings.copy")}
        </button>
      </div>
      <pre class="overflow-x-auto whitespace-pre-wrap break-words text-xs text-gray-700"><code>{value}</code></pre>
    </div>
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
    <div class="mb-10">
      <div class="flex flex-wrap items-start justify-between gap-3 mb-4">
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
            class="px-4 py-2 border border-gray-200 rounded-lg text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
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
      <div class="mt-6 mb-3 flex flex-wrap items-center justify-between gap-3">
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
                    <td
                      class="truncate whitespace-nowrap px-4 py-3 text-gray-600"
                      title={apiUsageSourceRowLabel(row.source_type, row.source_name)}
                    >
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

function chartXAxisLabels(xAxis: string[]): Array<{ index: number; label: string }> {
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

function formatChartDate(value: string): string {
  const date = new Date(`${value}T00:00:00`);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(locale.value === "zh" ? "zh-CN" : "en-US", {
    month: "numeric",
    day: "numeric",
  }).format(date);
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

function formatNumber(value: number): string {
  return new Intl.NumberFormat().format(value || 0);
}

function formatDateTime(value?: string): string {
  if (!value) return t("settings.never");
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return t("settings.never");
  return new Intl.DateTimeFormat("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(date);
}

function LangBtn({ code, label }: { code: Locale; label: string }) {
  const active = locale.value === code;
  return (
    <button
      onClick={() => setLocale(code)}
      class={`px-4 py-2 text-sm rounded-lg border transition-colors ${
        active
          ? "bg-indigo-50 border-indigo-300 text-indigo-700 font-medium"
          : "border-gray-200 text-gray-600 hover:bg-gray-50"
      }`}
    >
      {label}
    </button>
  );
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
              <label class="relative inline-flex items-center cursor-pointer">
                <input
                  type="checkbox"
                  checked={enabled}
                  onChange={(e) => onChangeEnabled((e.target as HTMLInputElement).checked)}
                  class="sr-only peer"
                />
                <div class="w-11 h-6 bg-gray-200 peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-indigo-300 rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-indigo-600"></div>
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
            class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-60 transition-colors"
          >
            {t("upgrade.cancel")}
          </button>
          <button
            type="button"
            onClick={step === "models" ? onSaveModels : onSave}
            disabled={saving || modelsSaving || (step === "models" && modelsLoading)}
            class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white hover:bg-indigo-700 disabled:opacity-60 transition-colors"
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
            class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-60 transition-colors"
          >
            {t("upgrade.cancel")}
          </button>
          <button
            type="button"
            onClick={onSave}
            disabled={saving}
            class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white hover:bg-indigo-700 disabled:opacity-60 transition-colors"
          >
            {saving ? "..." : t("settings.providerModelSave")}
          </button>
        </div>
      </div>
    </div>
  );
}
