import { signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { DirectoryPickerDialog } from "../components/DirectoryPickerDialog";
import { UpgradeDialog, type UpgradeProgress } from "../components/UpgradeDialog";
import { t, locale, setLocale } from "../i18n";
import type { Locale } from "../i18n";
import {
  browseLocalDirectories,
  checkUpgrade,
  clearObservabilityData,
  clearCloudToken,
  getCloudAuthStatus,
  getTags,
  installImageRuntime,
  getSettings,
  saveSettings,
  upgradeWithProgress,
} from "../api/client";
import type { AppSettings, ArtifactSource, CloudAuthStatus, LocalDirectoryBrowseResponse } from "../api/client";

const contextLengthSteps = [4096, 8192, 16384, 32768, 65536, 131072, 262144];
const contextLengthLabels = ["4k", "8k", "16k", "32k", "64k", "128k", "256k"];
const contextStorageKey = "csghub.chat.num_ctx";
const contextModeStorageKey = "csghub.chat.num_ctx_mode";
type ContextLengthMode = "global" | "model_max";
const parallelSteps = [1, 2, 4, 8];
const parallelLabels = ["1", "2", "4", "8"];
const parallelStorageKey = "csghub.chat.num_parallel";
const upgradeReloadTimeoutMs = 45_000;

const storageLocation = signal("");
const modelDirectory = signal("");
const datasetDirectory = signal("");
const appVersion = signal("");
const desktopMode = signal(false);
const localAPIURL = signal("");
const autostartEnabled = signal(false);
const isSavingAutostart = signal(false);
const contextIndex = signal(1);
const contextMode = signal<ContextLengthMode>("global");
const parallelIndex = signal(2);
const cloudAuth = signal<CloudAuthStatus | null>(null);
const cloudAuthError = signal("");
const isClearingCloudToken = signal(false);
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
const serverUrlInput = signal("");
const aiGatewayUrlInput = signal("");
const cloudProviderNameInput = signal("");
const defaultServerUrl = signal("");
const defaultAiGatewayUrl = signal("");
const defaultCloudProviderName = signal("");
const huggingFaceEndpointInput = signal("");
const huggingFaceTokenInput = signal("");
const huggingFaceTokenConfigured = signal(false);
const modelScopeEndpointInput = signal("");
const modelScopeTokenInput = signal("");
const modelScopeTokenConfigured = signal(false);
const cloudServiceTab = signal<ArtifactSource>("opencsg");
const cloudServiceError = signal("");
const cloudServiceMessage = signal("");
const savingCloudService = signal<ArtifactSource | "">("");
const isRefreshingCloudModels = signal(false);
const isResettingDefaults = signal(false);
const resetDefaultsMessage = signal("");
const resetDefaultsError = signal("");
const isUpgradingDiffuser = signal(false);
const diffuserUpgradeMessage = signal("");
const diffuserUpgradeError = signal("");
const observabilityRetentionPresets = [7, 30, 90, 365, 0] as const;
const observabilityRetentionDays = signal(30);
const isSavingObservability = signal(false);
const isClearingObservability = signal(false);
const observabilityMessage = signal("");
const observabilityError = signal("");
const providersChangedEvent = "csghub:providers-changed";

function normalizeObservabilityRetentionDays(days: number | undefined | null): number {
  const value = Math.max(0, Math.min(3650, Math.round(days ?? 30)));
  if ((observabilityRetentionPresets as readonly number[]).includes(value)) return value;
  if (value === 0) return 0;
  let best = 30;
  let bestDistance = Number.POSITIVE_INFINITY;
  for (const preset of observabilityRetentionPresets) {
    if (preset === 0) continue;
    const distance = Math.abs(preset - value);
    if (distance < bestDistance) {
      best = preset;
      bestDistance = distance;
    }
  }
  return best;
}

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

function loadContextMode(): ContextLengthMode {
  try {
    return localStorage.getItem(contextModeStorageKey) === "model_max" ? "model_max" : "global";
  } catch {
    return "global";
  }
}

function setContextModeLocal(mode: ContextLengthMode) {
  contextMode.value = mode;
  try {
    localStorage.setItem(contextModeStorageKey, mode);
  } catch {
    /* ignore */
  }
}

async function saveContextMode(mode: ContextLengthMode) {
  const previous = contextMode.value;
  setContextModeLocal(mode);
  try {
    applySettings(await saveSettings({ llama_use_model_max_ctx: mode === "model_max" }));
  } catch {
    setContextModeLocal(previous);
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
  setContextModeLocal("global");
  parallelIndex.value = 2;
  saveParallelIndex(2);
  setCloudServiceFeedback("", "");
  try {
    const data = await saveSettings({
      server_url: "",
      ai_gateway_url: "",
      cloud_provider_name: "",
      llama_use_model_max_ctx: false,
    });
    applySettings(data);
    notifyProvidersChanged();
    setCloudServiceFeedback("", t("settings.serviceUrlsResetSuccess"));
    resetDefaultsMessage.value = t("settings.resetDefaultsSuccess");
    fetchCloudAuth();
  } catch (err: any) {
    const message = err?.message || t("settings.resetDefaultsFailed");
    setCloudServiceFeedback(message, "");
    resetDefaultsError.value = message;
    fetchSettings();
  } finally {
    isResettingDefaults.value = false;
  }
}

function applySettings(data: AppSettings) {
  setContextModeLocal(data.llama_use_model_max_ctx ? "model_max" : "global");
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
  huggingFaceEndpointInput.value = data.huggingface_endpoint || "https://huggingface.co";
  huggingFaceTokenConfigured.value = data.huggingface_token_configured ?? false;
  modelScopeEndpointInput.value = data.modelscope_endpoint || "https://modelscope.cn";
  modelScopeTokenConfigured.value = data.modelscope_token_configured ?? false;
  appVersion.value = data.version || "";
  desktopMode.value = data.desktop_mode ?? false;
  localAPIURL.value = data.local_api_url || "";
  upgradeProgress.value = {
    ...upgradeProgress.value,
    currentVersion: data.version || upgradeProgress.value.currentVersion,
  };
  autostartEnabled.value = data.autostart ?? false;
  observabilityRetentionDays.value = normalizeObservabilityRetentionDays(data.observability?.retention_days);
}

async function saveObservabilityRetention() {
  const days = normalizeObservabilityRetentionDays(observabilityRetentionDays.value);
  observabilityRetentionDays.value = days;
  isSavingObservability.value = true;
  observabilityMessage.value = "";
  observabilityError.value = "";
  try {
    const data = await saveSettings({ observability: { retention_days: days } });
    applySettings(data);
    observabilityMessage.value = t("observability.retentionSaved");
  } catch (err: any) {
    observabilityError.value = err?.message || t("observability.retentionSaveFailed");
  } finally {
    isSavingObservability.value = false;
  }
}

async function clearSavedObservabilityData() {
  if (!confirm(t("observability.clearConfirm"))) return;
  isClearingObservability.value = true;
  observabilityMessage.value = "";
  observabilityError.value = "";
  try {
    await clearObservabilityData();
    observabilityMessage.value = t("observability.clearSuccess");
  } catch (err: any) {
    observabilityError.value = err?.message || t("observability.clearFailed");
  } finally {
    isClearingObservability.value = false;
  }
}

async function copyLocalAPIURL() {
  if (localAPIURL.value) {
    await navigator.clipboard.writeText(localAPIURL.value);
  }
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

function pollCloudAuthAfterLogin() {
  const deadline = Date.now() + 5 * 60 * 1000;
  let timer: number | undefined;

  const poll = async () => {
    try {
      const status = await getCloudAuthStatus();
      cloudAuth.value = status;
      if (status.authenticated && status.user) {
        cloudAuthError.value = "";
        return;
      }
    } catch (err: any) {
      cloudAuthError.value = err?.message || "";
    }
    if (Date.now() < deadline) {
      timer = window.setTimeout(poll, 1500);
    }
  };

  void poll();
  return () => {
    if (timer !== undefined) window.clearTimeout(timer);
  };
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

function setCloudServiceFeedback(error: string, message: string) {
  cloudServiceError.value = error;
  cloudServiceMessage.value = message;
}

function selectCloudServiceTab(tab: ArtifactSource) {
  if (cloudServiceTab.value === tab) return;
  cloudServiceTab.value = tab;
  setCloudServiceFeedback("", "");
}

function cloudServiceTabBorderClass(source: ArtifactSource): string {
  if (source === "huggingface") return "border-[#FFD21E]";
  if (source === "opencsg") return "border-[#169F95]";
  return "border-[#624AFF]";
}

function cloudServiceIconClass(source: ArtifactSource): string {
  if (source === "huggingface") return "bg-[#FFD21E] text-gray-900";
  if (source === "opencsg") return "bg-[#169F95] text-white";
  return "bg-[#624AFF] text-white";
}

function cloudServiceShortLabel(source: ArtifactSource): string {
  if (source === "huggingface") return "HF";
  if (source === "modelscope") return "MS";
  return "OC";
}

function cloudServiceLabel(source: ArtifactSource): string {
  if (source === "huggingface") return t("mp.sourceHuggingFace");
  if (source === "modelscope") return t("mp.sourceModelScope");
  return t("mp.sourceOpenCSG");
}

function cloudServiceHint(source: ArtifactSource): string {
  if (source === "huggingface") return t("settings.huggingfaceServiceHint");
  if (source === "modelscope") return t("settings.modelscopeServiceHint");
  return t("settings.opencsgServiceHint");
}

function cloudServiceStatus(source: ArtifactSource): { text: string; ok: boolean } {
  if (source === "opencsg") {
    if (cloudAuth.value?.authenticated && cloudAuth.value.user) {
      return { text: cloudUserLabel(cloudAuth.value) || t("settings.signedIn"), ok: true };
    }
    if (cloudAuth.value?.has_token) return { text: t("settings.tokenSaved"), ok: true };
    return { text: t("settings.loggedOut"), ok: false };
  }
  const configured = source === "huggingface" ? huggingFaceTokenConfigured.value : modelScopeTokenConfigured.value;
  return configured
    ? { text: t("settings.registryTokenConfigured"), ok: true }
    : { text: t("settings.registryTokenNotConfigured"), ok: false };
}

async function saveOpenCSGService() {
  savingCloudService.value = "opencsg";
  setCloudServiceFeedback("", "");
  try {
    const data = await saveSettings({
      server_url: serverUrlInput.value,
      ai_gateway_url: aiGatewayUrlInput.value,
      cloud_provider_name: cloudProviderNameInput.value,
    });
    applySettings(data);
    notifyProvidersChanged();
    setCloudServiceFeedback("", t("settings.providerSaveSuccess", cloudServiceLabel("opencsg")));
    fetchCloudAuth();
  } catch (err: any) {
    setCloudServiceFeedback(err?.message || t("settings.serviceUrlsSaveFailed"), "");
  } finally {
    savingCloudService.value = "";
  }
}

async function saveRegistryService(source: "huggingface" | "modelscope") {
  savingCloudService.value = source;
  setCloudServiceFeedback("", "");
  try {
    const data = await saveSettings(source === "huggingface"
      ? {
        huggingface_endpoint: huggingFaceEndpointInput.value,
        ...(huggingFaceTokenInput.value.trim() ? { huggingface_token: huggingFaceTokenInput.value.trim() } : {}),
      }
      : {
        modelscope_endpoint: modelScopeEndpointInput.value,
        ...(modelScopeTokenInput.value.trim() ? { modelscope_token: modelScopeTokenInput.value.trim() } : {}),
      });
    if (source === "huggingface") huggingFaceTokenInput.value = "";
    else modelScopeTokenInput.value = "";
    applySettings(data);
    setCloudServiceFeedback("", t("settings.providerSaveSuccess", cloudServiceLabel(source)));
  } catch (err: any) {
    setCloudServiceFeedback(err?.message || t("settings.serviceUrlsSaveFailed"), "");
  } finally {
    savingCloudService.value = "";
  }
}

async function clearRegistryToken(source: "huggingface" | "modelscope") {
  savingCloudService.value = source;
  setCloudServiceFeedback("", "");
  try {
    const data = await saveSettings(source === "huggingface"
      ? { huggingface_token: "" }
      : { modelscope_token: "" });
    applySettings(data);
    setCloudServiceFeedback("", t("settings.registryTokenCleared"));
  } catch (err: any) {
    setCloudServiceFeedback(err?.message || t("settings.serviceUrlsSaveFailed"), "");
  } finally {
    savingCloudService.value = "";
  }
}

async function refreshCloudModels() {
  if (isRefreshingCloudModels.value) return;
  isRefreshingCloudModels.value = true;
  setCloudServiceFeedback("", "");
  try {
    await getTags({ refresh: true });
    notifyProvidersChanged();
    setCloudServiceFeedback("", t("settings.cloudModelsRefreshSuccess"));
  } catch (err: any) {
    setCloudServiceFeedback(err?.message || t("settings.cloudModelsRefreshFailed"), "");
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

function RegistryCredentialFields({
  endpoint,
  token,
  configured,
  disabled,
  onEndpointChange,
  onTokenChange,
  onClear,
}: {
  endpoint: string;
  token: string;
  configured: boolean;
  disabled: boolean;
  onEndpointChange: (value: string) => void;
  onTokenChange: (value: string) => void;
  onClear: () => void;
}) {
  return (
    <div class="space-y-4">
      <label class="block">
        <span class="text-sm font-medium text-gray-700">{t("settings.registryEndpoint")}</span>
        <input
          type="url"
          value={endpoint}
          disabled={disabled}
          onInput={(event) => onEndpointChange((event.currentTarget as HTMLInputElement).value)}
          class="mt-1 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100"
        />
      </label>
      <label class="block">
        <span class="flex items-center justify-between gap-2">
          <span class="text-sm font-medium text-gray-700">{t("settings.registryToken")}</span>
          <span class={`text-xs ${configured ? "text-emerald-600" : "text-gray-400"}`}>
            {configured ? t("settings.registryTokenConfigured") : t("settings.registryTokenNotConfigured")}
          </span>
        </span>
        <input
          type="password"
          value={token}
          disabled={disabled}
          autocomplete="new-password"
          placeholder={configured ? t("settings.registryTokenKeep") : t("settings.registryTokenPlaceholder")}
          onInput={(event) => onTokenChange((event.currentTarget as HTMLInputElement).value)}
          class="mt-1 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100"
        />
      </label>
      {configured && (
        <button
          type="button"
          disabled={disabled}
          onClick={onClear}
          class="text-xs text-red-600 hover:text-red-700 disabled:opacity-60"
        >
          {t("settings.registryTokenClear")}
        </button>
      )}
    </div>
  );
}

function openCloudLogin() {
  openExternal(cloudAuth.value?.login_url);
  pollCloudAuthAfterLogin();
}

async function logoutCloudAccount() {
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
}

function OpenCSGAccountPanel() {
  return (
    <div class="rounded-xl border border-gray-100 bg-gray-50/60 p-4">
      <div class="mb-3 flex items-center justify-between gap-2">
        <p class="text-sm font-semibold text-gray-900">{t("settings.account")}</p>
        {cloudAuth.value?.authenticated && cloudAuth.value.user && (
          <span class="inline-flex items-center gap-1.5 rounded-full bg-emerald-50 px-2.5 py-0.5 text-xs font-medium text-emerald-700">
            <span class="h-1.5 w-1.5 rounded-full bg-emerald-500" />
            {t("settings.signedIn")}
          </span>
        )}
      </div>
      {cloudAuth.value === null ? (
        <p class="text-sm text-gray-500">...</p>
      ) : cloudAuth.value.authenticated && cloudAuth.value.user ? (
        <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div class="flex min-w-0 items-center gap-3">
            {cloudAuth.value.user.avatar ? (
              <img
                src={cloudAuth.value.user.avatar}
                alt={cloudUserLabel(cloudAuth.value)}
                class="h-11 w-11 rounded-full border border-gray-200 bg-white object-cover"
              />
            ) : (
              <div class="flex h-11 w-11 items-center justify-center rounded-full bg-[#169F95] text-base font-semibold text-white">
                {cloudUserInitial(cloudAuth.value)}
              </div>
            )}
            <div class="min-w-0">
              <p class="truncate text-sm font-semibold text-gray-900">{cloudUserLabel(cloudAuth.value)}</p>
              <p class="truncate text-xs text-gray-500">
                @{cloudAuth.value.user.username}
                {cloudAuth.value.user.email ? ` · ${cloudAuth.value.user.email}` : ""}
              </p>
            </div>
          </div>
          <div class="flex gap-2">
            <button
              type="button"
              onClick={() => void logoutCloudAccount()}
              disabled={isClearingCloudToken.value}
              class="rounded-lg border border-red-200 bg-white px-3.5 py-1.5 text-sm text-red-600 transition-colors hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-60"
            >
              {isClearingCloudToken.value ? t("settings.loggingOut") : t("settings.logout")}
            </button>
          </div>
        </div>
      ) : cloudAuth.value.has_token ? (
        <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p class="text-sm font-semibold text-gray-900">{t("settings.tokenSaved")}</p>
            <p class="mt-0.5 text-xs text-gray-500">{t("settings.tokenSavedDesc")}</p>
          </div>
          <div class="flex gap-2">
            <button
              type="button"
              onClick={openCloudLogin}
              class="rounded-lg border border-gray-200 bg-white px-3.5 py-1.5 text-sm text-gray-700 transition-colors hover:bg-gray-100"
            >
              {t("settings.login")}
            </button>
            <button
              type="button"
              onClick={() => void logoutCloudAccount()}
              disabled={isClearingCloudToken.value}
              class="rounded-lg border border-red-200 bg-white px-3.5 py-1.5 text-sm text-red-600 transition-colors hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-60"
            >
              {isClearingCloudToken.value ? t("settings.loggingOut") : t("settings.logout")}
            </button>
          </div>
        </div>
      ) : (
        <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p class="text-sm font-semibold text-gray-900">{t("settings.loggedOut")}</p>
            <p class="mt-0.5 text-xs text-gray-500">{t("settings.loggedOutDesc")}</p>
          </div>
          <div class="flex gap-2">
            <button
              type="button"
              onClick={openCloudLogin}
              class="rounded-lg bg-[#169F95] px-3.5 py-1.5 text-sm font-medium text-white transition-colors hover:bg-[#128a81]"
            >
              {t("settings.login")}
            </button>
          </div>
        </div>
      )}
      {cloudAuthError.value && (
        <p class="mt-3 text-sm text-red-600">{cloudAuthError.value}</p>
      )}
    </div>
  );
}

function CloudServiceActions({
  saving,
  onSave,
}: {
  saving: boolean;
  onSave: () => void;
}) {
  return (
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div class="text-sm">
        {cloudServiceError.value && <span class="text-red-600">{cloudServiceError.value}</span>}
        {cloudServiceMessage.value && <span class="text-green-600">{cloudServiceMessage.value}</span>}
      </div>
      <button
        type="button"
        onClick={onSave}
        disabled={saving}
        class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
      >
        {saving ? "..." : t("settings.save")}
      </button>
    </div>
  );
}

export function Settings() {
  void locale.value;

  useEffect(() => {
    fetchSettings();
    fetchCloudAuth();
    void fetchUpgradeInfo();
    contextIndex.value = loadContextIndex();
    contextMode.value = loadContextMode();
    parallelIndex.value = loadParallelIndex();
  }, []);

  return (
    <div class="page-shell">
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

      {/* Observability retention */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="h-5 w-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M4 19V9m5 10V5m5 14v-7m5 7V3M3 19h18" />
          </svg>
          <span class="font-semibold text-gray-900">{t("observability.settingsTitle")}</span>
        </div>
        <p class="mb-3 ml-7 text-sm text-gray-500">{t("observability.settingsDesc")}</p>
        <div class="ml-7 rounded-xl border border-gray-200 bg-white p-4">
          <div class="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
            <div class="min-w-0 flex-1">
              <p class="text-sm font-semibold text-gray-900">{t("observability.retention")}</p>
              <div class="mt-3 inline-flex max-w-full flex-wrap rounded-lg border border-gray-200 bg-gray-50 p-1">
                {observabilityRetentionPresets.map((days) => {
                  const selected = observabilityRetentionDays.value === days;
                  return (
                    <button
                      type="button"
                      key={days}
                      onClick={() => (observabilityRetentionDays.value = days)}
                      class={`rounded-md px-3 py-1.5 text-sm transition ${
                        selected
                          ? "bg-white font-medium text-indigo-700 shadow-sm ring-1 ring-gray-200"
                          : "text-gray-600 hover:text-gray-900"
                      }`}
                    >
                      {days === 0 ? t("observability.retentionForever") : t("observability.retentionDays", days)}
                    </button>
                  );
                })}
              </div>
            </div>
            <div class="flex shrink-0 flex-wrap items-center gap-2">
              <button
                type="button"
                onClick={() => void saveObservabilityRetention()}
                disabled={isSavingObservability.value}
                class="inline-flex items-center justify-center rounded-lg border border-indigo-200 px-4 py-2 text-sm text-indigo-700 hover:bg-indigo-50 disabled:cursor-not-allowed disabled:opacity-60 transition-colors"
              >
                {isSavingObservability.value ? "..." : t("settings.save")}
              </button>
              <button
                type="button"
                onClick={() => void clearSavedObservabilityData()}
                disabled={isClearingObservability.value}
                title={t("observability.clearDataHint")}
                class="inline-flex items-center justify-center rounded-lg border border-red-200 px-4 py-2 text-sm font-medium text-red-600 hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-60 transition-colors"
              >
                {isClearingObservability.value ? "..." : t("observability.clearNow")}
              </button>
            </div>
          </div>
          {observabilityMessage.value && <p class="mt-3 text-sm text-emerald-600">{observabilityMessage.value}</p>}
          {observabilityError.value && <p class="mt-3 text-sm text-red-600">{observabilityError.value}</p>}
        </div>
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
        <div class="ml-7 space-y-4">
          <div class="grid gap-3 sm:grid-cols-2">
            {([
              ["global", "settings.contextLengthGlobal", "settings.contextLengthGlobalDesc"],
              ["model_max", "settings.contextLengthModelMax", "settings.contextLengthModelMaxDesc"],
            ] as const).map(([mode, labelKey, descriptionKey]) => {
              const selected = contextMode.value === mode;
              return (
                <button
                  key={mode}
                  type="button"
                  onClick={() => void saveContextMode(mode)}
                  class={`rounded-xl border p-4 text-left transition ${
                    selected
                      ? "border-indigo-300 bg-indigo-50 ring-1 ring-indigo-200"
                      : "border-gray-200 bg-white hover:border-gray-300"
                  }`}
                >
                  <span class={`block text-sm font-medium ${selected ? "text-indigo-800" : "text-gray-800"}`}>
                    {t(labelKey)}
                  </span>
                  <span class="mt-1 block text-xs leading-5 text-gray-500">{t(descriptionKey)}</span>
                </button>
              );
            })}
          </div>
          {contextMode.value === "global" && (
            <div>
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
          )}
          {contextMode.value === "model_max" && (
            <p class="rounded-lg bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-700">
              {t("settings.contextLengthModelMaxWarning")}
            </p>
          )}
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

      {/* Cloud services */}
      <div class="mb-10">
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-5 h-5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
          </svg>
          <span class="font-semibold text-gray-900">{t("settings.serviceUrls")}</span>
        </div>
        <p class="text-sm text-gray-500 mb-3 ml-7">{t("settings.serviceUrlsDesc")}</p>
        <div class="ml-7 overflow-hidden rounded-xl border border-gray-200 bg-white">
          <div class="flex overflow-x-auto border-b border-gray-100 px-2" role="tablist" aria-label={t("settings.serviceUrls")}>
            {(["opencsg", "huggingface", "modelscope"] as ArtifactSource[]).map((source) => {
              const selected = cloudServiceTab.value === source;
              const status = cloudServiceStatus(source);
              return (
                <button
                  key={source}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  title={status.text}
                  onClick={() => selectCloudServiceTab(source)}
                  class={`-mb-px flex flex-none items-center gap-2 border-b-2 px-4 py-3 text-sm transition ${
                    selected
                      ? `${cloudServiceTabBorderClass(source)} font-semibold text-gray-900`
                      : "border-transparent text-gray-500 hover:text-gray-800"
                  }`}
                >
                  <span class={`flex h-6 w-6 flex-none items-center justify-center rounded-md text-[10px] font-bold ${cloudServiceIconClass(source)}`}>
                    {cloudServiceShortLabel(source)}
                  </span>
                  {cloudServiceLabel(source)}
                  <span class={`h-1.5 w-1.5 flex-none rounded-full ${status.ok ? "bg-emerald-500" : "bg-gray-300"}`} />
                </button>
              );
            })}
          </div>
          <div class="p-5">
            <p class="mb-5 text-sm leading-5 text-gray-500">{cloudServiceHint(cloudServiceTab.value)}</p>
              {cloudServiceTab.value === "opencsg" && (
                <div class="space-y-5">
                  <OpenCSGAccountPanel />
                  <div class="space-y-4 border-t border-gray-100 pt-5">
                    <label class="block">
                      <span class="text-sm font-medium text-gray-700">{t("settings.serverUrl")}</span>
                      <input
                        type="url"
                        value={serverUrlInput.value}
                        onInput={(event) => {
                          serverUrlInput.value = (event.currentTarget as HTMLInputElement).value;
                        }}
                        placeholder={defaultServerUrl.value}
                        class="mt-1.5 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100"
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
                        class="mt-1.5 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100"
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
                        class="mt-1.5 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100"
                      />
                      <span class="mt-1 block text-xs text-gray-400">{t("settings.cloudProviderNameHint", defaultCloudProviderName.value || "csghub")}</span>
                    </label>
                  </div>
                  <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 pt-4">
                    <div class="text-sm">
                      {cloudServiceError.value && <span class="text-red-600">{cloudServiceError.value}</span>}
                      {cloudServiceMessage.value && <span class="text-emerald-600">{cloudServiceMessage.value}</span>}
                    </div>
                    <div class="flex flex-wrap gap-2">
                      <button
                        type="button"
                        onClick={() => void refreshCloudModels()}
                        disabled={isRefreshingCloudModels.value}
                        class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-60"
                      >
                        {isRefreshingCloudModels.value ? t("settings.cloudModelsRefreshing") : t("settings.cloudModelsRefresh")}
                      </button>
                      <button
                        type="button"
                        onClick={() => void saveOpenCSGService()}
                        disabled={savingCloudService.value === "opencsg"}
                        class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
                      >
                        {savingCloudService.value === "opencsg" ? "..." : t("settings.save")}
                      </button>
                    </div>
                  </div>
                </div>
              )}
              {cloudServiceTab.value === "huggingface" && (
                <div class="space-y-5">
                  <RegistryCredentialFields
                    endpoint={huggingFaceEndpointInput.value}
                    token={huggingFaceTokenInput.value}
                    configured={huggingFaceTokenConfigured.value}
                    disabled={savingCloudService.value === "huggingface"}
                    onEndpointChange={(value) => (huggingFaceEndpointInput.value = value)}
                    onTokenChange={(value) => (huggingFaceTokenInput.value = value)}
                    onClear={() => void clearRegistryToken("huggingface")}
                  />
                  <div class="border-t border-gray-100 pt-4">
                    <CloudServiceActions
                      saving={savingCloudService.value === "huggingface"}
                      onSave={() => void saveRegistryService("huggingface")}
                    />
                  </div>
                </div>
              )}
              {cloudServiceTab.value === "modelscope" && (
                <div class="space-y-5">
                  <RegistryCredentialFields
                    endpoint={modelScopeEndpointInput.value}
                    token={modelScopeTokenInput.value}
                    configured={modelScopeTokenConfigured.value}
                    disabled={savingCloudService.value === "modelscope"}
                    onEndpointChange={(value) => (modelScopeEndpointInput.value = value)}
                    onTokenChange={(value) => (modelScopeTokenInput.value = value)}
                    onClear={() => void clearRegistryToken("modelscope")}
                  />
                  <div class="border-t border-gray-100 pt-4">
                    <CloudServiceActions
                      saving={savingCloudService.value === "modelscope"}
                      onSave={() => void saveRegistryService("modelscope")}
                    />
                  </div>
                </div>
              )}
          </div>
        </div>
      </div>

      {/* Autostart */}
      {!desktopMode.value && (
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
      )}

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
          {desktopMode.value && localAPIURL.value && (
            <div class="mb-4 border-b border-gray-100 pb-4">
              <p class="text-sm font-semibold text-gray-900">{t("settings.localAPIAddress")}</p>
              <p class="mt-1 text-sm text-gray-500">{t("settings.localAPIHint")}</p>
              <div class="mt-3 flex flex-col gap-2 sm:flex-row">
                <input
                  readOnly
                  value={localAPIURL.value}
                  class="min-w-0 flex-1 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 font-mono text-sm text-gray-700"
                />
                <button
                  type="button"
                  onClick={() => void copyLocalAPIURL()}
                  class="inline-flex items-center justify-center rounded-lg border border-indigo-200 px-4 py-2 text-sm text-indigo-700 hover:bg-indigo-50 transition-colors"
                >
                  {t("settings.copy")}
                </button>
              </div>
            </div>
          )}
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
            {!desktopMode.value && (
              <button
                type="button"
                onClick={openUpgradeDialog}
                disabled={!upgradeProgress.value.hasUpdate}
                class="px-4 py-2 border border-indigo-200 rounded-lg text-sm text-indigo-700 hover:bg-indigo-50 disabled:border-gray-200 disabled:text-gray-400 disabled:opacity-70 disabled:cursor-not-allowed transition-colors"
              >
                {t("upgrade.upgrade")}
              </button>
            )}
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
      {!desktopMode.value && (
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
      )}
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
