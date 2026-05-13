import { signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { DirectoryPickerDialog } from "../components/DirectoryPickerDialog";
import { UpgradeDialog, type UpgradeProgress } from "../components/UpgradeDialog";
import { t, locale, setLocale } from "../i18n";
import type { Locale } from "../i18n";
import {
  browseLocalDirectories,
  checkUpgrade,
  clearCloudToken,
  getCloudAuthStatus,
  getSettings,
  saveCloudToken,
  saveSettings,
  upgradeWithProgress,
  getProviders,
  createProvider,
  updateProvider,
  deleteProvider,
  validateProvider,
  getLocalAPIKeys,
  updateLocalAPIKeySettings,
  createLocalAPIKey,
  deleteLocalAPIKey,
  getLocalAPIUsage,
} from "../api/client";
import type { AppSettings, CloudAuthStatus, LocalAPIKeysResponse, LocalAPIUsageResponse, LocalDirectoryBrowseResponse, ThirdPartyProvider } from "../api/client";

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
const cloudAuthError = signal("");
const isClearingCloudToken = signal(false);
const isSavingCloudToken = signal(false);
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
const providersChangedEvent = "csghub:providers-changed";
type SettingsTab = "system" | "apiKeys" | "usage";

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
const copiedBaseURL = signal("");

const providerTypes = [
  { value: "openai", label: "OpenAI Compatible", name: "OpenAI", baseURL: "https://api.openai.com/v1" },
  { value: "deepseek", label: "DeepSeek", name: "DeepSeek", baseURL: "https://api.deepseek.com/v1" },
  { value: "mimo", label: "MiMo (Xiaomi)", name: "MiMo", baseURL: "https://api.xiaomimimo.com/v1" },
  { value: "kimi", label: "Kimi (Moonshot)", name: "Kimi", baseURL: "https://api.moonshot.cn/v1" },
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
    providers.value = await getProviders();
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

async function fetchLocalAPIUsage() {
  localAPIUsageLoading.value = true;
  localAPIUsageError.value = "";
  try {
    localAPIUsage.value = await getLocalAPIUsage();
  } catch (err: any) {
    localAPIUsageError.value = err?.message || t("settings.apiUsageLoadFailed");
  } finally {
    localAPIUsageLoading.value = false;
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
  providerFormName.value = provider?.name || "";
  providerFormBaseURL.value = provider?.base_url || "";
  providerFormAPIKey.value = "";
  providerFormType.value = provider?.provider || "openai";
  providerFormEnabled.value = provider?.enabled ?? true;
  providerFormError.value = "";
  isProviderDialogOpen.value = true;
}

function closeProviderDialog() {
  if (providerFormSaving.value) return;
  isProviderDialogOpen.value = false;
  editingProvider.value = null;
  providerFormError.value = "";
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
    if (editingProvider.value) {
      await updateProvider(editingProvider.value.id, {
        name,
        base_url: baseURL,
        api_key: apiKey || undefined,
        provider: providerType,
        enabled,
      });
    } else {
      await createProvider({
        name,
        base_url: baseURL,
        api_key: apiKey,
        provider: providerType,
        enabled,
      });
    }
    await fetchProviders();
    notifyProvidersChanged();
    isProviderDialogOpen.value = false;
    editingProvider.value = null;
  } catch (err: any) {
    providerFormError.value = err?.message || t("settings.providerSaveFailed");
  } finally {
    providerFormSaving.value = false;
  }
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

function hasCloudAuth(status: CloudAuthStatus | null | undefined): boolean {
  return status?.authenticated ?? status?.has_token ?? false;
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

  const handleOpenCloudTokenPage = () => {
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
      cloudAuthError.value = t("chat.cloudApiKeyEmpty");
      return;
    }

    isSavingCloudToken.value = true;
    cloudAuthError.value = "";
    try {
      const status = await saveCloudToken(token);
      cloudAuth.value = status;
      if (!hasCloudAuth(status)) {
        cloudAuthError.value = t("chat.cloudApiKeyInvalid");
        return;
      }
      cloudTokenInput.value = "";
    } catch (err: any) {
      cloudAuthError.value = err?.message || t("chat.failedResp");
    } finally {
      isSavingCloudToken.value = false;
    }
  };

  return (
    <div class="p-8 max-w-3xl mx-auto">
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
            </>
          )}

          {activeSettingsTab.value === "apiKeys" && (
            <>
      <LocalAPIKeysSection />

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
              <label class="mb-2 block text-sm font-medium text-gray-700">{t("chat.cloudApiKeyLabel")}</label>
              <p class="mb-3 text-sm text-gray-500">{t("settings.tokenInputHint")}</p>
              <div class="mb-3 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2">
                <div class="text-xs font-medium uppercase tracking-wide text-gray-400">{t("chat.cloudGatewayLabel")}</div>
                <div class="mt-1 text-sm font-medium text-gray-800">{t("chat.cloudGatewayValue")}</div>
              </div>
              <div class="flex flex-col gap-3 sm:flex-row sm:items-end">
                <div class="flex-1">
                  <input
                    type="password"
                    autoComplete="off"
                    spellcheck={false}
                    class="w-full rounded-lg border border-gray-200 px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                    placeholder={t("chat.cloudApiKeyPlaceholder")}
                    value={cloudTokenInput.value}
                    onInput={(e) => (cloudTokenInput.value = (e.target as HTMLInputElement).value)}
                  />
                </div>
                <button
                  onClick={handleSaveCloudToken}
                  disabled={isSavingCloudToken.value}
                  class="px-4 py-2 border border-indigo-200 rounded-lg text-sm text-indigo-700 hover:bg-indigo-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
                >
                  {isSavingCloudToken.value ? t("chat.cloudApiKeySaving") : t("chat.cloudApiKeySave")}
                </button>
              </div>
            </div>
          )}
          {cloudAuthError.value && (
            <p class="mt-3 text-sm text-red-600">{cloudAuthError.value}</p>
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
        name={providerFormName.value}
        baseURL={providerFormBaseURL.value}
        apiKey={providerFormAPIKey.value}
        providerType={providerFormType.value}
        enabled={providerFormEnabled.value}
        error={providerFormError.value}
        saving={providerFormSaving.value}
        onClose={closeProviderDialog}
        onSave={() => void saveProviderForm()}
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
  return (
    <div class="mb-10">
      <div class="flex items-center justify-between gap-3 mb-4">
        <div>
          <h2 class="font-semibold text-gray-900">{t("settings.apiUsage")}</h2>
          <p class="mt-1 text-sm text-gray-500">{t("settings.apiUsageDesc")}</p>
        </div>
        <button
          type="button"
          onClick={() => void fetchLocalAPIUsage()}
          disabled={localAPIUsageLoading.value}
          class="px-4 py-2 border border-gray-200 rounded-lg text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
        >
          {localAPIUsageLoading.value ? "..." : t("settings.apiUsageRefresh")}
        </button>
      </div>
      <div class="grid gap-3 sm:grid-cols-4">
        <UsageCard label={t("settings.apiUsageRequests")} value={formatNumber(usage?.totals.requests || 0)} />
        <UsageCard label={t("settings.apiUsageInput")} value={formatNumber(usage?.totals.input_tokens || 0)} />
        <UsageCard label={t("settings.apiUsageOutput")} value={formatNumber(usage?.totals.output_tokens || 0)} />
        <UsageCard label={t("settings.apiUsageTotal")} value={formatNumber(usage?.totals.total_tokens || 0)} />
      </div>
      {localAPIUsageError.value && <p class="mt-3 text-sm text-red-600">{localAPIUsageError.value}</p>}
      <div class="mt-5 overflow-hidden rounded-xl border border-gray-200 bg-white">
        {rows.length === 0 ? (
          <p class="p-4 text-sm text-gray-400">{localAPIUsageLoading.value ? "..." : t("settings.apiUsageEmpty")}</p>
        ) : (
          <div class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-100 text-sm">
              <thead class="bg-gray-50 text-left text-xs uppercase tracking-wide text-gray-400">
                <tr>
                  <th class="px-4 py-3">{t("settings.apiUsageKey")}</th>
                  <th class="px-4 py-3">{t("settings.apiUsageModel")}</th>
                  <th class="px-4 py-3">{t("settings.apiUsageRequests")}</th>
                  <th class="px-4 py-3">{t("settings.apiUsageInput")}</th>
                  <th class="px-4 py-3">{t("settings.apiUsageOutput")}</th>
                  <th class="px-4 py-3">{t("settings.apiUsageTotal")}</th>
                  <th class="px-4 py-3">{t("settings.apiUsageLastUsed")}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100">
                {rows.map((row) => (
                  <tr key={`${row.api_key_id}:${row.model}`}>
                    <td class="px-4 py-3 font-medium text-gray-900">{row.api_key_name}</td>
                    <td class="px-4 py-3 text-gray-600">{row.model}</td>
                    <td class="px-4 py-3 text-gray-600">{formatNumber(row.requests)}</td>
                    <td class="px-4 py-3 text-gray-600">{formatNumber(row.input_tokens)}</td>
                    <td class="px-4 py-3 text-gray-600">{formatNumber(row.output_tokens)}</td>
                    <td class="px-4 py-3 text-gray-600">{formatNumber(row.total_tokens)}</td>
                    <td class="px-4 py-3 text-gray-500">{formatDateTime(row.last_used_at)}</td>
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

function UsageCard({ label, value }: { label: string; value: string }) {
  return (
    <div class="rounded-xl border border-gray-200 bg-white p-4">
      <p class="text-xs uppercase tracking-wide text-gray-400">{label}</p>
      <p class="mt-2 text-xl font-semibold text-gray-900">{value}</p>
    </div>
  );
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat().format(value || 0);
}

function formatDateTime(value?: string): string {
  if (!value) return t("settings.never");
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return t("settings.never");
  return date.toLocaleString();
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
  name,
  baseURL,
  apiKey,
  providerType,
  enabled,
  error,
  saving,
  onClose,
  onSave,
  onChangeName,
  onChangeBaseURL,
  onChangeAPIKey,
  onChangeProviderType,
  onChangeEnabled,
}: {
  open: boolean;
  editing: boolean;
  name: string;
  baseURL: string;
  apiKey: string;
  providerType: string;
  enabled: boolean;
  error: string;
  saving: boolean;
  onClose: () => void;
  onSave: () => void;
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
          <h2 class="text-lg font-semibold text-gray-900">{editing ? t("settings.providerEditTitle") : t("settings.providerAddTitle")}</h2>
          <p class="mt-1 text-sm text-gray-500">{t("settings.providerDialogDesc")}</p>
        </div>
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
            {saving ? "..." : t("settings.providerSave")}
          </button>
        </div>
      </div>
    </div>
  );
}
