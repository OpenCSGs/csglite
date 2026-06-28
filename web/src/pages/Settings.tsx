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
  getTags,
  installImageRuntime,
  getSettings,
  saveCloudToken,
  saveSettings,
  upgradeWithProgress,
} from "../api/client";
import type { AppSettings, CloudAuthStatus, LocalDirectoryBrowseResponse, WebSearchSettings } from "../api/client";

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
const webSearchEnabled = signal(false);
const webSearchMaxResults = signal(5);
const webSearchLanguage = signal("");
const webSearchSafeSearch = signal(1);
const webSearchTimeoutSeconds = signal(5);
const webSearchError = signal("");
const isSavingWebSearch = signal(false);
const serverUrlInput = signal("");
const aiGatewayUrlInput = signal("");
const cloudProviderNameInput = signal("");
const defaultServerUrl = signal("");
const defaultAiGatewayUrl = signal("");
const defaultCloudProviderName = signal("");
const serviceUrlsError = signal("");
const serviceUrlsMessage = signal("");
const isSavingServiceUrls = signal(false);
const isRefreshingCloudModels = signal(false);
const isResettingDefaults = signal(false);
const resetDefaultsMessage = signal("");
const resetDefaultsError = signal("");
const isUpgradingDiffuser = signal(false);
const diffuserUpgradeMessage = signal("");
const diffuserUpgradeError = signal("");
const providersChangedEvent = "csghub:providers-changed";

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

async function resetDefaults() {
  isResettingDefaults.value = true;
  resetDefaultsMessage.value = "";
  resetDefaultsError.value = "";
  contextIndex.value = 1;
  saveContextIndex(1);
  parallelIndex.value = 2;
  saveParallelIndex(2);
  serviceUrlsError.value = "";
  serviceUrlsMessage.value = "";
  try {
    const data = await saveSettings({ server_url: "", ai_gateway_url: "", cloud_provider_name: "" });
    applySettings(data);
    notifyProvidersChanged();
    serviceUrlsMessage.value = t("settings.serviceUrlsResetSuccess");
    resetDefaultsMessage.value = t("settings.resetDefaultsSuccess");
    fetchCloudAuth();
  } catch (err: any) {
    const message = err?.message || t("settings.resetDefaultsFailed");
    serviceUrlsError.value = message;
    resetDefaultsError.value = message;
    fetchSettings();
  } finally {
    isResettingDefaults.value = false;
  }
}

function applySettings(data: AppSettings) {
  storageLocation.value = data.storage_dir || "";
  storageDirInput.value = data.storage_dir || "";
  modelDirectory.value = data.model_dir || "";
  datasetDirectory.value = data.dataset_dir || "";
  serverUrlInput.value = data.server_url || "";
  aiGatewayUrlInput.value = data.ai_gateway_url || "";
  cloudProviderNameInput.value = data.cloud_provider_name || "";
  defaultServerUrl.value = data.default_server_url || "";
  defaultAiGatewayUrl.value = data.default_ai_gateway_url || "";
  defaultCloudProviderName.value = data.default_cloud_provider_name || "csghub";
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

const feedbackURL = "https://github.com/opencsgs/csglite";

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

async function saveServiceURLs() {
  isSavingServiceUrls.value = true;
  serviceUrlsError.value = "";
  serviceUrlsMessage.value = "";
  try {
    const data = await saveSettings({
      server_url: serverUrlInput.value,
      ai_gateway_url: aiGatewayUrlInput.value,
      cloud_provider_name: cloudProviderNameInput.value,
    });
    applySettings(data);
    notifyProvidersChanged();
    serviceUrlsMessage.value = t("settings.serviceUrlsSaveSuccess");
    fetchCloudAuth();
  } catch (err: any) {
    serviceUrlsError.value = err?.message || t("settings.serviceUrlsSaveFailed");
  } finally {
    isSavingServiceUrls.value = false;
  }
}

async function refreshCloudModels() {
  if (isRefreshingCloudModels.value) return;
  isRefreshingCloudModels.value = true;
  serviceUrlsError.value = "";
  serviceUrlsMessage.value = "";
  try {
    await getTags({ refresh: true });
    notifyProvidersChanged();
    serviceUrlsMessage.value = t("settings.cloudModelsRefreshSuccess");
  } catch (err: any) {
    serviceUrlsError.value = err?.message || t("settings.cloudModelsRefreshFailed");
  } finally {
    isRefreshingCloudModels.value = false;
  }
}

async function upgradeDiffuser() {
  if (isUpgradingDiffuser.value) return;
  isUpgradingDiffuser.value = true;
  diffuserUpgradeMessage.value = "";
  diffuserUpgradeError.value = "";
  try {
    await installImageRuntime({ upgrade_packages: true });
    diffuserUpgradeMessage.value = t("settings.diffuserUpgradeSuccess");
  } catch (err: any) {
    diffuserUpgradeError.value = err?.message || t("settings.diffuserUpgradeFailed");
  } finally {
    isUpgradingDiffuser.value = false;
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

export function Settings() {
  void locale.value;
  const showTokenInput = !(cloudAuth.value?.authenticated && cloudAuth.value?.user);

  useEffect(() => {
    fetchSettings();
    fetchCloudAuth();
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

  return (
    <div class="mx-auto max-w-6xl p-8">
      <h1 class="text-2xl font-bold text-gray-900">{t("settings.title")}</h1>
      <p class="text-gray-500 text-sm mt-1 mb-6">{t("settings.subtitle")}</p>

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

      {/* Service URLs */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.serviceUrls")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-3 ml-7">{t("settings.serviceUrlsDesc")}</p>
        <div class="ml-7 rounded-xl border border-gray-200 bg-white p-4 space-y-4">
          <label class="block">
            <span class="text-sm font-medium text-gray-700">{t("settings.serverUrl")}</span>
            <input
              type="url"
              value={serverUrlInput.value}
              onInput={(event) => {
                serverUrlInput.value = (event.currentTarget as HTMLInputElement).value;
              }}
              placeholder={defaultServerUrl.value}
              class="mt-1 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100"
            />
            <span class="mt-1 block text-xs text-gray-400">{t("settings.serviceUrlDefault", defaultServerUrl.value || "-")}</span>
          </label>
          <label class="block">
            <span class="text-sm font-medium text-gray-700">{t("settings.aiGatewayUrl")}</span>
            <input
              type="url"
              value={aiGatewayUrlInput.value}
              onInput={(event) => {
                aiGatewayUrlInput.value = (event.currentTarget as HTMLInputElement).value;
              }}
              placeholder={defaultAiGatewayUrl.value}
              class="mt-1 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100"
            />
            <span class="mt-1 block text-xs text-gray-400">{t("settings.serviceUrlDefault", defaultAiGatewayUrl.value || "-")}</span>
          </label>
          <label class="block">
            <span class="text-sm font-medium text-gray-700">{t("settings.cloudProviderName")}</span>
            <input
              type="text"
              value={cloudProviderNameInput.value}
              onInput={(event) => {
                cloudProviderNameInput.value = (event.currentTarget as HTMLInputElement).value;
              }}
              placeholder={defaultCloudProviderName.value}
              class="mt-1 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100"
            />
            <span class="mt-1 block text-xs text-gray-400">{t("settings.cloudProviderNameHint", defaultCloudProviderName.value || "csghub")}</span>
          </label>
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div class="text-sm">
              {serviceUrlsError.value && <span class="text-red-600">{serviceUrlsError.value}</span>}
              {serviceUrlsMessage.value && <span class="text-green-600">{serviceUrlsMessage.value}</span>}
            </div>
            <div class="flex flex-wrap gap-2">
              <button
                type="button"
                onClick={() => void refreshCloudModels()}
                disabled={isRefreshingCloudModels.value}
                class="px-4 py-2 border border-gray-200 text-gray-700 rounded-lg text-sm hover:bg-gray-50 disabled:opacity-60 transition-colors"
              >
                {isRefreshingCloudModels.value ? t("settings.cloudModelsRefreshing") : t("settings.cloudModelsRefresh")}
              </button>
              <button
                type="button"
                onClick={() => void saveServiceURLs()}
                disabled={isSavingServiceUrls.value}
                class="px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm hover:bg-indigo-700 disabled:opacity-60 transition-colors"
              >
                {isSavingServiceUrls.value ? "..." : t("settings.save")}
              </button>
            </div>
          </div>
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

      {/* Feedback */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M8.625 9.75h.008v.008h-.008V9.75zm3.367 0H12v.008h-.008V9.75zm3.375 0h.008v.008h-.008V9.75z" />
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 12c0 4.418-4.03 8-9 8a9.77 9.77 0 01-3.792-.744L3 20l1.377-3.216A7.54 7.54 0 013 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.feedback")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-3 ml-7">{t("settings.feedbackDesc")}</p>
        <div class="ml-7 rounded-xl border border-gray-200 bg-white p-4">
          <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <p class="text-sm font-semibold text-gray-900">{t("settings.feedbackTitle")}</p>
              <p class="mt-1 text-sm text-gray-500">{t("settings.feedbackHint")}</p>
            </div>
            <button
              type="button"
              onClick={() => openExternal(feedbackURL)}
              class="inline-flex items-center justify-center px-4 py-2 border border-indigo-200 rounded-lg text-sm text-indigo-700 hover:bg-indigo-50 transition-colors"
            >
              {t("settings.openFeedback")}
            </button>
          </div>
        </div>
      </div>

      {/* Diffuser */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1M12 12V4m0 8l-3-3m3 3l3-3" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.diffuser")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-3 ml-7">{t("settings.diffuserDesc")}</p>
        <div class="ml-7 rounded-xl border border-gray-200 bg-white p-4">
          <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <p class="text-sm font-semibold text-gray-900">{t("settings.diffuserUpgradeTitle")}</p>
              <p class="mt-1 text-sm text-gray-500">{t("settings.diffuserUpgradeHint")}</p>
            </div>
            <button
              type="button"
              onClick={() => void upgradeDiffuser()}
              disabled={isUpgradingDiffuser.value}
              class="inline-flex items-center justify-center px-4 py-2 border border-indigo-200 rounded-lg text-sm text-indigo-700 hover:bg-indigo-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
            >
              {isUpgradingDiffuser.value ? t("settings.diffuserUpgrading") : t("settings.diffuserUpgrade")}
            </button>
          </div>
          {diffuserUpgradeMessage.value && <p class="mt-3 text-sm text-green-600">{diffuserUpgradeMessage.value}</p>}
          {diffuserUpgradeError.value && <p class="mt-3 text-sm text-red-600">{diffuserUpgradeError.value}</p>}
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

      <div class="flex flex-col gap-3 border-t border-gray-100 pt-6 sm:flex-row sm:items-center sm:justify-between">
        <div class="text-sm">
          {resetDefaultsMessage.value && <span class="text-green-600">{resetDefaultsMessage.value}</span>}
          {resetDefaultsError.value && <span class="text-red-600">{resetDefaultsError.value}</span>}
        </div>
        <button
          onClick={() => void resetDefaults()}
          disabled={isResettingDefaults.value}
          class="px-4 py-2 border border-gray-200 rounded-lg text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-60 disabled:cursor-not-allowed transition-colors"
        >
          {isResettingDefaults.value ? t("settings.resettingDefaults") : t("settings.resetDefaults")}
        </button>
      </div>
      <DirectoryPickerDialog
        open={isStorageDirPickerOpen.value}
        loading={isBrowsingStorageDir.value}
        data={storageDirBrowser.value}
        error={storageDirBrowserError.value}
        onClose={closeStorageDirPicker}
        onBrowse={(path) => void browseStorageDir(path)}
        onSelect={selectStorageDir}
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
