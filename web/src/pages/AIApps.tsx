import type { ComponentChildren } from "preact";
import { computed, signal } from "@preact/signals";
import { useEffect, useRef, useState } from "preact/hooks";
import {
  getAIApps,
  getCloudAuthStatus,
  getTags,
  installAIApp,
  openAIApp,
  saveAIAppModel,
  saveCloudToken,
  stopAIApp,
  streamAIAppLogs,
  uninstallAIApp,
  type AIAppInfo as RemoteAIAppInfo,
  type CloudAuthStatus,
  type ModelInfo,
} from "../api/client";
import {
  aiAppCategoryOptions,
  aiAppsCatalog,
  createAIAppStateSnapshot,
  getLocalizedText,
  type AIAppCatalogEntry,
  type AIAppCategory,
  type AIAppInstallMode,
  type AIAppProgressMode,
  type AIAppRuntimeState,
} from "../data/aiApps";
import { locale, t, type Locale } from "../i18n";

type AIAppFilter = "all" | AIAppCategory;
type DrawerMode = "details" | "install";

const searchQuery = signal("");
const categoryFilter = signal<AIAppFilter>("all");
const selectedAppId = signal("");
const drawerMode = signal<DrawerMode>("details");
const drawerLogs = signal<string[]>([]);
const drawerStreaming = signal(false);
const appStates = signal<Record<string, AIAppRuntimeState>>(createAIAppStateSnapshot());
const loadingApps = signal(true);
const loadError = signal("");
const actionError = signal("");
const pendingInstallId = signal("");
const pendingUninstallId = signal("");
const pendingOpenId = signal("");
const pendingStopId = signal("");
const visibleError = computed(() => actionError.value || loadError.value);

function hasCloudAuth(status: CloudAuthStatus | null | undefined): boolean {
  return status?.authenticated ?? status?.has_token ?? false;
}

function aiAppModelKey(model: Pick<ModelInfo, "model" | "source">): string {
  return `${model.source || "local"}:${model.model}`;
}

function parseAIAppModelKey(key: string): { source: string; model: string } {
  const providerPrefix = "provider:";
  if (key.startsWith(providerPrefix)) {
    const next = key.indexOf(":", providerPrefix.length);
    if (next > providerPrefix.length) {
      return { source: key.slice(0, next), model: key.slice(next + 1) };
    }
  }
  const first = key.indexOf(":");
  if (first > 0) {
    return { source: key.slice(0, first), model: key.slice(first + 1) };
  }
  return { source: "", model: key };
}

const filteredApps = computed(() => {
  const currentLocale = locale.value;
  const query = searchQuery.value.trim().toLowerCase();

  return aiAppsCatalog.filter((app) => {
    if (categoryFilter.value !== "all" && app.category !== categoryFilter.value) {
      return false;
    }
    if (!query) return true;

    const searchable = [
      app.name,
      app.siteLabel,
      app.website,
      getLocalizedText(app.description, currentLocale),
      getCategoryLabel(app.category, currentLocale),
    ].join(" ").toLowerCase();

    return searchable.includes(query);
  });
});

const groupedApps = computed(() => {
  const states = appStates.value;
  const installed: AIAppCatalogEntry[] = [];
  const others: AIAppCatalogEntry[] = [];

  for (const app of filteredApps.value) {
    if (states[app.id]?.status === "installed") {
      installed.push(app);
      continue;
    }
    others.push(app);
  }

  return {
    installed,
    others,
    hasInstalled: installed.length > 0,
  };
});

const selectedApp = computed(() => aiAppsCatalog.find((app) => app.id === selectedAppId.value) || null);

export function AIApps() {
  void locale.value;

  useEffect(() => {
    let disposed = false;

    const refresh = async () => {
      try {
        const apps = await getAIApps();
        if (disposed) return;
        mergeAppStates(apps);
        loadError.value = "";
      } catch (error) {
        if (disposed) return;
        loadError.value = localizeAIAppErrorMessage((error as Error).message, t("aiApps.loadFailed"));
      } finally {
        if (!disposed) {
          loadingApps.value = false;
        }
      }
    };

    refresh();
    const interval = window.setInterval(refresh, 3000);
    return () => {
      disposed = true;
      clearInterval(interval);
    };
  }, []);

  const openDrawer = (appId: string, mode: DrawerMode) => {
    selectedAppId.value = appId;
    drawerMode.value = mode;
  };

  const closeDrawer = () => {
    selectedAppId.value = "";
  };

  const handleInstall = async (appId: string) => {
    pendingInstallId.value = appId;
    actionError.value = "";
    try {
      const state = await installAIApp(appId);
      mergeAppStates([state]);
      openDrawer(appId, "install");
    } catch (error) {
      actionError.value = localizeAIAppErrorMessage((error as Error).message, t("aiApps.installFailed"));
    } finally {
      pendingInstallId.value = "";
    }
  };

  const handleUninstall = async (appId: string) => {
    pendingUninstallId.value = appId;
    actionError.value = "";
    try {
      const state = await uninstallAIApp(appId);
      mergeAppStates([state]);
    } catch (error) {
      actionError.value = localizeAIAppErrorMessage((error as Error).message, t("aiApps.uninstallFailed"));
    } finally {
      pendingUninstallId.value = "";
    }
  };

  const handleOpenApp = async (appId: string, modelId?: string, source?: string) => {
    pendingOpenId.value = appId;
    actionError.value = "";
    const popup = openAppPopup();
    try {
      const { url } = await openAIApp(appId, modelId, undefined, source);
      if (popup) {
        popup.location.replace(url);
      } else {
        window.location.href = url;
      }
    } catch (error) {
      if (popup) {
        popup.close();
      }
      actionError.value = localizeAIAppErrorMessage((error as Error).message, t("aiApps.openFailed"));
    } finally {
      pendingOpenId.value = "";
    }
  };

  const handleStopApp = async (appId: string) => {
    pendingStopId.value = appId;
    actionError.value = "";
    try {
      const state = await stopAIApp(appId);
      mergeAppStates([state]);
    } catch (error) {
      actionError.value = localizeAIAppErrorMessage((error as Error).message, t("aiApps.stopFailed"));
    } finally {
      pendingStopId.value = "";
    }
  };

  const grouped = groupedApps.value;

  return (
    <div class="p-8 max-w-5xl mx-auto">
      <h1 class="text-2xl font-bold text-gray-900">{t("aiApps.title")}</h1>
      <p class="text-gray-500 text-sm mt-1 mb-6">{t("aiApps.subtitle")}</p>

      {visibleError.value && (
        <div class="mb-4 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          <div class="flex items-start justify-between gap-3 flex-wrap">
            <div class="flex items-start gap-2 min-w-0">
              <svg class="w-4 h-4 mt-0.5 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
              <span class="min-w-0">{visibleError.value}</span>
            </div>
          </div>
        </div>
      )}

      <div class="flex items-center gap-3 mb-6 flex-wrap">
        <div class="relative flex-1 min-w-[260px]">
          <svg class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35m1.85-5.15a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            type="text"
            value={searchQuery.value}
            onInput={(e) => (searchQuery.value = (e.currentTarget as HTMLInputElement).value)}
            placeholder={t("aiApps.search")}
            class="w-full pl-10 pr-20 py-2.5 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent"
          />
          <span class="absolute right-3 top-1/2 -translate-y-1/2 text-[11px] font-medium text-gray-400 bg-gray-50 px-2 py-0.5 rounded-full">
            {t("aiApps.results", filteredApps.value.length)}
          </span>
        </div>

        <div class="relative w-48">
          <select
            value={categoryFilter.value}
            onChange={(e) => (categoryFilter.value = (e.currentTarget as HTMLSelectElement).value as AIAppFilter)}
            class="appearance-none w-full border border-gray-200 rounded-lg pl-3 pr-9 py-2.5 text-sm text-gray-700 focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent"
          >
            {aiAppCategoryOptions.map((option) => (
              <option key={option.id} value={option.id}>
                {getLocalizedText(option.label, locale.value)}
              </option>
            ))}
          </select>
          <svg class="absolute right-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400 pointer-events-none" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        </div>
      </div>

      {loadingApps.value ? (
        <div class="border border-dashed border-gray-300 rounded-2xl bg-white px-6 py-12 text-center text-sm text-gray-400">
          {t("aiApps.loading")}
        </div>
      ) : filteredApps.value.length === 0 ? (
        <div class="border border-dashed border-gray-300 rounded-2xl bg-white px-6 py-12 text-center text-sm text-gray-400">
          {t("aiApps.noResults")}
        </div>
      ) : grouped.hasInstalled ? (
        <div class="space-y-6">
          <section class="space-y-3">
            <div class="flex items-center gap-3">
              <span class="text-sm font-semibold text-gray-900">{t("aiApps.installed")}</span>
              <div class="h-px flex-1 bg-indigo-100" />
            </div>
            <AIAppsGrid
              apps={grouped.installed}
              onOpenDetails={(appId) => openDrawer(appId, "details")}
              onInstall={handleInstall}
              onOpenChat={handleOpenApp}
            />
          </section>

          {grouped.others.length > 0 && (
            <>
              <div class="h-px bg-gray-200" />
              <AIAppsGrid
                apps={grouped.others}
                onOpenDetails={(appId) => openDrawer(appId, "details")}
                onInstall={handleInstall}
                onOpenChat={handleOpenApp}
              />
            </>
          )}
        </div>
      ) : (
        <AIAppsGrid
          apps={filteredApps.value}
          onOpenDetails={(appId) => openDrawer(appId, "details")}
          onInstall={handleInstall}
          onOpenChat={handleOpenApp}
        />
      )}

      {selectedApp.value && (
        <LiveLogsDrawer
          app={selectedApp.value}
          state={appStates.value[selectedApp.value.id]}
          mode={drawerMode.value}
          onClose={closeDrawer}
          onInstall={() => handleInstall(selectedApp.value!.id)}
          onUninstall={() => handleUninstall(selectedApp.value!.id)}
          pendingInstall={pendingInstallId.value === selectedApp.value.id}
          pendingUninstall={pendingUninstallId.value === selectedApp.value.id}
          pendingOpen={pendingOpenId.value === selectedApp.value.id}
          pendingStop={pendingStopId.value === selectedApp.value.id}
          onOpenChat={(modelId?: string, source?: string) => handleOpenApp(selectedApp.value!.id, modelId, source)}
          onStop={() => handleStopApp(selectedApp.value!.id)}
        />
      )}
    </div>
  );
}

function AIAppsGrid({
  apps,
  onOpenDetails,
  onInstall,
  onOpenChat,
}: {
  apps: AIAppCatalogEntry[];
  onOpenDetails: (appId: string) => void;
  onInstall: (appId: string) => void;
  onOpenChat: (appId: string) => void;
}) {
  return (
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
      {apps.map((app) => (
        <AIAppCard
          key={app.id}
          app={app}
          state={appStates.value[app.id]}
          pendingInstall={pendingInstallId.value === app.id}
          pendingOpen={pendingOpenId.value === app.id}
          onOpenDetails={() => onOpenDetails(app.id)}
          onInstall={() => onInstall(app.id)}
          onOpenChat={() => onOpenChat(app.id)}
        />
      ))}
    </div>
  );
}

function AIAppCard({
  app,
  state,
  pendingInstall,
  pendingOpen,
  onOpenDetails,
  onInstall,
  onOpenChat,
}: {
  app: AIAppCatalogEntry;
  state: AIAppRuntimeState;
  pendingInstall: boolean;
  pendingOpen: boolean;
  onOpenDetails: () => void;
  onInstall: () => void;
  onOpenChat: () => void;
}) {
  const currentLocale = locale.value;
  const isInstalled = state.status === "installed";
  const isInstalling = state.status === "installing";
  const isUninstalling = state.status === "uninstalling";
  const isWorking = isInstalling || isUninstalling;
  const canOpenChat = canOpenAIApp(app, state);
  const showRuntimeIndicator = canControlAIAppRuntime(state);

  return (
    <div class={`relative border rounded-xl bg-white p-5 flex flex-col justify-between min-h-[236px] overflow-hidden ${
      state.disabled ? "border-gray-200 opacity-80" : "border-gray-200"
    }`}>
      {showRuntimeIndicator ? <RuntimeStatusCorner state={state} /> : isInstalled && <InstalledCorner />}

      <div>
        <div class="flex items-start gap-3">
          <img src={app.icon} alt={`${app.name} icon`} class="w-11 h-11 rounded-xl border border-gray-100 flex-shrink-0 bg-white object-cover" />
          <div class="min-w-0">
            <a
              href={app.website}
              target="_blank"
              rel="noreferrer"
              class="font-semibold text-gray-900 text-lg leading-6 hover:text-indigo-600 transition-colors"
            >
              {app.name}
            </a>
            <div>
              <a
                href={app.website}
                target="_blank"
                rel="noreferrer"
                class="text-xs text-indigo-500 hover:text-indigo-600 hover:underline"
              >
                {app.siteLabel}
              </a>
            </div>
          </div>
        </div>

        <p class="text-sm text-gray-500 mt-3 line-clamp-3 min-h-[4rem]">
          {getLocalizedText(app.description, currentLocale)}
        </p>

        <div class="mt-3 flex items-center gap-2 flex-wrap">
          <span class={`inline-flex items-center rounded-full px-2.5 py-1 text-xs font-medium ${categoryClassName(app.category)}`}>
            {getCategoryLabel(app.category, currentLocale)}
          </span>
          {state.disabled && (
            <span class="inline-flex items-center rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-500">
              {t("aiApps.disabled")}
            </span>
          )}
        </div>
      </div>

      <div class="mt-5 pt-4 border-t border-gray-100">
        {isWorking ? (
          <div class="flex items-center gap-3">
            <button onClick={onOpenDetails} class="text-xs text-indigo-600 hover:text-indigo-700 transition-colors whitespace-nowrap">
              {t("aiApps.liveLogs")}
            </button>
            <div class="min-w-0 flex-1">
              {state.progressMode === "percent" && typeof state.progress === "number" ? (
                <PercentProgress value={state.progress} label={phaseLabel(state.phase)} />
              ) : (
                <SpinnerProgress label={phaseLabel(state.phase)} />
              )}
            </div>
          </div>
        ) : (
          <div class="flex items-center justify-between gap-3">
            <div class="min-w-0">
              <div class={`text-xs font-medium ${statusColorClass(state.status)}`}>
                {statusLabel(state)}
              </div>
              {state.version && (
                <div class="text-[11px] text-gray-400 truncate mt-1">{state.version}</div>
              )}
              {state.updateAvailable && state.latestVersion && (
                <div class="text-[11px] text-amber-600 truncate mt-1">
                  {t("aiApps.updateAvailableShort", state.latestVersion)}
                </div>
              )}
            </div>
            <div class="flex items-center gap-3">
              <button onClick={onOpenDetails} class="text-xs text-gray-500 hover:text-indigo-600 transition-colors">
                {t("aiApps.details")}
              </button>
              {canOpenChat && (
                <button
                  onClick={onOpenChat}
                  disabled={pendingOpen}
                  class={`inline-flex items-center gap-1.5 text-xs font-medium transition-colors ${
                    pendingOpen
                      ? "text-gray-300 cursor-not-allowed"
                      : "text-indigo-600 hover:text-indigo-700"
                  }`}
                >
                  <OpenIcon className="w-3.5 h-3.5" />
                  {pendingOpen ? t("aiApps.opening") : t("aiApps.open")}
                </button>
              )}
              {!isInstalled && (
                <button
                  onClick={onInstall}
                  disabled={state.disabled || pendingInstall}
                  class={`inline-flex items-center gap-1.5 text-xs font-medium transition-colors ${
                    state.disabled || pendingInstall
                      ? "text-gray-300 cursor-not-allowed"
                      : "text-indigo-600 hover:text-indigo-700"
                  }`}
                >
                  <InstallIcon className="w-3.5 h-3.5" />
                  {actionLabel(state)}
                </button>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function LiveLogsDrawer({
  app,
  state,
  mode,
  onClose,
  onInstall,
  onUninstall,
  pendingInstall,
  pendingUninstall,
  pendingOpen,
  pendingStop,
  onOpenChat,
  onStop,
}: {
  app: AIAppCatalogEntry;
  state: AIAppRuntimeState;
  mode: DrawerMode;
  onClose: () => void;
  onInstall: () => void;
  onUninstall: () => void;
  pendingInstall: boolean;
  pendingUninstall: boolean;
  pendingOpen: boolean;
  pendingStop: boolean;
  onOpenChat: (modelId?: string, source?: string) => void;
  onStop: () => void;
}) {
  void locale.value;

  const logRef = useRef<HTMLDivElement>(null);
  const copyResetRef = useRef<number | null>(null);
  const isInstalling = state.status === "installing";
  const isUninstalling = state.status === "uninstalling";
  const isWorking = isInstalling || isUninstalling;
  const showTaskLogs = state.liveLogsReady && (mode === "install" || isWorking);
  const requestPending = pendingInstall || pendingUninstall || pendingStop;
  const canOpenChat = canOpenAIApp(app, state);
  const canSelectModel = canSelectAIAppModel(app);
  const showProgressSummary = !state.disabled && isWorking;
  const showRuntimeSummary = canControlAIAppRuntime(state);
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [selectedModel, setSelectedModel] = useState("");
  const [copiedModel, setCopiedModel] = useState(false);
  const [cloudAuth, setCloudAuth] = useState<CloudAuthStatus | null>(null);
  const [showCloudAuthDialog, setShowCloudAuthDialog] = useState(false);
  const [cloudAuthError, setCloudAuthError] = useState("");
  const [cloudTokenInput, setCloudTokenInput] = useState("");
  const [isSavingCloudToken, setIsSavingCloudToken] = useState(false);
  const selectedModelParts = selectedModel ? parseAIAppModelKey(selectedModel) : null;
  const currentModelID = selectedModelParts?.model || state.modelID?.trim() || "";
  const currentModelInfo = selectedModelParts
    ? models.find((item) => aiAppModelKey(item) === selectedModel)
    : models.find((item) => item.model === currentModelID);
  const launchPreview = cliLaunchPreview(app, currentModelID);

  useEffect(() => {
    if (!showTaskLogs) {
      drawerLogs.value = [];
      drawerStreaming.value = false;
      return;
    }

    drawerLogs.value = appStates.value[app.id]?.logLines || [];
    drawerStreaming.value = true;

    const ac = new AbortController();
    streamAIAppLogs(app.id, (line) => {
      const nextLogs = [...drawerLogs.value.slice(-199), line];
      drawerLogs.value = nextLogs;
      appStates.value = {
        ...appStates.value,
        [app.id]: {
          ...appStates.value[app.id],
          logLines: nextLogs,
        },
      };
      if (logRef.current) {
        requestAnimationFrame(() => {
          if (logRef.current) {
            logRef.current.scrollTop = logRef.current.scrollHeight;
          }
        });
      }
    }, ac.signal);

    return () => {
      drawerStreaming.value = false;
      ac.abort();
    };
  }, [app.id, showTaskLogs]);

  useEffect(() => {
    if (!canSelectModel) {
      setModels([]);
      setModelsLoading(false);
      return;
    }

    let disposed = false;
    setModelsLoading(true);

    // Only refresh on first load, subsequent opens use cached data
    getTags({ refresh: false })
      .then((items) => {
        if (disposed) return;
        // If we got models, use them. If empty, try with refresh.
        if (items.length > 0) {
          setModels(normalizeAIAppModels(items));
          return;
        }
        return getTags({ refresh: true }).then((refreshed) => {
          if (disposed) return;
          setModels(normalizeAIAppModels(refreshed));
        });
      })
      .catch(() => {
        if (disposed) return;
        setModels([]);
      })
      .finally(() => {
        if (!disposed) {
          setModelsLoading(false);
        }
      });

    return () => {
      disposed = true;
    };
  }, [app.id, canSelectModel]);

  useEffect(() => {
    if (!canSelectModel) {
      setCloudAuth(null);
      return;
    }

    let disposed = false;
    getCloudAuthStatus()
      .then((status) => {
        if (!disposed) {
          setCloudAuth(status);
        }
      })
      .catch(() => {
        /* ignore */
      });

    return () => {
      disposed = true;
    };
  }, [app.id, canSelectModel]);

  useEffect(() => {
    if (!canSelectModel) {
      setSelectedModel("");
      return;
    }

    setSelectedModel((current) => {
      if (current && models.some((item) => aiAppModelKey(item) === current)) {
        return current;
      }
      if (state.modelID && models.some((item) => item.model === state.modelID)) {
        const model = models.find((item) => item.model === state.modelID);
        return model ? aiAppModelKey(model) : state.modelID;
      }
      if (state.modelID) {
        return state.modelID;
      }
      return models[0] ? aiAppModelKey(models[0]) : "";
    });
  }, [app.id, canSelectModel, state.modelID, models]);

  useEffect(() => {
    return () => {
      if (copyResetRef.current !== null) {
        window.clearTimeout(copyResetRef.current);
      }
    };
  }, []);

  useEffect(() => {
    setSelectedModel("");
    setCopiedModel(false);
    if (copyResetRef.current !== null) {
      window.clearTimeout(copyResetRef.current);
      copyResetRef.current = null;
    }
  }, [app.id]);

  const handleCopyModel = async () => {
    if (!currentModelID) {
      return;
    }
    try {
      await navigator.clipboard.writeText(currentModelID);
      setCopiedModel(true);
      if (copyResetRef.current !== null) {
        window.clearTimeout(copyResetRef.current);
      }
      copyResetRef.current = window.setTimeout(() => {
        setCopiedModel(false);
        copyResetRef.current = null;
      }, 1500);
    } catch {
      // Ignore clipboard failures so the drawer keeps working.
    }
  };

  const refreshCloudAuth = async (): Promise<CloudAuthStatus> => {
    const status = await getCloudAuthStatus();
    setCloudAuth(status);
    return status;
  };

  const openCloudAuthDialog = async (message = "") => {
    setCloudAuthError(message);
    setShowCloudAuthDialog(true);
    try {
      await refreshCloudAuth();
    } catch {
      /* ignore */
    }
  };

  const ensureCloudAuthForModel = async (model: ModelInfo | undefined): Promise<boolean> => {
    if (model?.source !== "cloud") {
      return true;
    }

    try {
      const status = cloudAuth || await refreshCloudAuth();
      if (hasCloudAuth(status)) {
        return true;
      }
    } catch {
      /* fall through to API key dialog */
    }

    await openCloudAuthDialog(t("chat.cloudApiKeyRequired"));
    return false;
  };

  const handleModelChange = (value: string) => {
    setSelectedModel(value);
    const nextModel = models.find((item) => aiAppModelKey(item) === value);
    if (nextModel) {
      void saveAIAppModel(app.id, nextModel.model, nextModel.source);
    }
    void ensureCloudAuthForModel(nextModel);
  };

  const handleOpenCurrentApp = async () => {
    if (!(await ensureCloudAuthForModel(currentModelInfo))) {
      return;
    }
    onOpenChat(currentModelID || undefined, currentModelInfo?.source);
  };

  const handleSaveCloudToken = async () => {
    const token = cloudTokenInput.trim();
    if (!token) {
      setCloudAuthError(t("chat.cloudApiKeyEmpty"));
      return;
    }

    setIsSavingCloudToken(true);
    setCloudAuthError("");
    try {
      const status = await saveCloudToken(token);
      setCloudAuth(status);
      if (!hasCloudAuth(status)) {
        setCloudAuthError(t("chat.cloudApiKeyInvalid"));
        return;
      }
      try {
        setModels(normalizeAIAppModels(await getTags({ refresh: true })));
      } catch {
        /* ignore */
      }
      setCloudTokenInput("");
      setShowCloudAuthDialog(false);
    } catch (error) {
      setCloudAuthError((error as Error).message || t("chat.failedResp"));
    } finally {
      setIsSavingCloudToken(false);
    }
  };

  return (
    <div class="fixed inset-0 z-50 bg-black/35 backdrop-blur-[1px] flex justify-end" onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div class="w-full max-w-2xl h-full bg-white shadow-2xl flex flex-col" onClick={(e) => e.stopPropagation()}>
        <div class="px-6 py-5 border-b border-gray-100 flex items-start justify-between gap-4">
          <div class="flex items-start gap-3 min-w-0">
            <img src={app.icon} alt={`${app.name} icon`} class="w-12 h-12 rounded-xl border border-gray-100 bg-white object-cover flex-shrink-0" />
            <div class="min-w-0">
              <div class="flex items-center gap-2 flex-wrap">
                <h2 class="text-lg font-bold text-gray-900">{app.name}</h2>
                <span class="inline-flex items-center rounded-full bg-indigo-50 px-2 py-0.5 text-xs font-medium text-indigo-600">
                  {mode === "install" ? t("aiApps.installPreview") : t("aiApps.setupDetails")}
                </span>
              </div>
              <p class="text-sm text-gray-500 mt-1">{getLocalizedText(app.description, locale.value)}</p>
              <a
                href={app.website}
                target="_blank"
                rel="noreferrer"
                class="inline-flex items-center text-xs text-indigo-600 hover:text-indigo-700 hover:underline mt-2"
              >
                {t("aiApps.visitWebsite")}
              </a>
            </div>
          </div>
        </div>

        <div class="flex-1 overflow-auto px-6 py-5 space-y-5">
          <div class={`rounded-xl px-4 py-3 text-sm ${
            state.disabled
              ? "border border-gray-200 bg-gray-50 text-gray-700"
              : state.phase === "uninstall_failed" || state.status === "failed"
                ? "border border-red-200 bg-red-50 text-red-700"
                : state.status === "uninstalling"
                  ? "border border-amber-200 bg-amber-50 text-amber-700"
                : "border border-indigo-200 bg-indigo-50 text-indigo-700"
          }`}>
            {drawerNotice(state)}
          </div>

          <div class={`grid grid-cols-1 ${showProgressSummary || showRuntimeSummary ? "sm:grid-cols-5" : "sm:grid-cols-4"} gap-3`}>
            <SummaryTile label={t("aiApps.installMode")} value={installModeLabel(app.installMode)} />
            {showProgressSummary && (
              <SummaryTile label={t("aiApps.progressMode")} value={renderProgressValue(state.progressMode, state.progress)} />
            )}
            <SummaryTile label={t("aiApps.currentStatus")} value={statusLabel(state)} />
            {showRuntimeSummary && (
              <SummaryTile label={t("aiApps.runtimeStatus")} value={runtimeStatusLabel(state)} />
            )}
            <SummaryTile label={t("aiApps.currentVersion")} value={state.version || "—"} />
            <SummaryTile label={t("aiApps.latestVersion")} value={state.latestVersion || "—"} />
            <SummaryTile label={t("aiApps.updateStatus")} value={updateStatusLabel(state)} />
          </div>

          {state.installPath && (
            <section class="space-y-2">
              <h3 class="text-sm font-semibold text-gray-900">{t("aiApps.installPath")}</h3>
              <div class="rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 text-sm text-gray-600 font-mono break-all">
                {state.installPath}
              </div>
            </section>
          )}

          {showTaskLogs && state.logPath && (
            <section class="space-y-2">
              <h3 class="text-sm font-semibold text-gray-900">{t("aiApps.logFile")}</h3>
              <div class="rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 text-sm text-gray-600 font-mono break-all">
                {state.logPath}
              </div>
            </section>
          )}

          {state.lastError && (
            <section class="space-y-2">
              <h3 class="text-sm font-semibold text-gray-900">{t("aiApps.lastError")}</h3>
              <div class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 whitespace-pre-wrap">
                {state.lastError}
              </div>
            </section>
          )}

          {(canSelectModel || currentModelID) && (
            <section class="space-y-2">
              <h3 class="text-sm font-semibold text-gray-900">{t("aiApps.currentModelId")}</h3>
              <div class="flex items-center gap-3 flex-wrap">
                <div class="min-w-0 flex-1">
                  {canSelectModel ? (
                    <div class="relative">
                      <select
                        value={selectedModel || currentModelID}
                        onChange={(e) => {
                          handleModelChange((e.currentTarget as HTMLSelectElement).value);
                        }}
                        disabled={modelsLoading || models.length === 0}
                        class={`appearance-none w-full rounded-xl border bg-white pl-3 pr-9 py-3 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent ${
                          modelsLoading || models.length === 0
                            ? "border-gray-200 text-gray-400"
                            : "border-gray-200 text-gray-700"
                        }`}
                        aria-label={t("aiApps.currentModelId")}
                      >
                        {modelsLoading ? (
                          <option value="">{t("aiApps.modelLoading")}</option>
                        ) : models.length === 0 ? (
                          <option value={currentModelID}>{currentModelID || t("aiApps.modelDefault")}</option>
                        ) : (
                          <>
                            {currentModelID && !models.some((item) => item.model === currentModelID) && (
                              <option value={currentModelID}>{currentModelID}</option>
                            )}
                            {models.map((model) => (
                              <option key={aiAppModelKey(model)} value={aiAppModelKey(model)}>
                                {formatAIAppModelLabel(model)}
                              </option>
                            ))}
                          </>
                        )}
                      </select>
                      <svg class="absolute right-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400 pointer-events-none" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
                      </svg>
                    </div>
                  ) : (
                    <div class="rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 text-sm text-gray-700 font-mono break-all">
                      {currentModelID}
                    </div>
                  )}
                </div>
                <button
                  onClick={() => void handleCopyModel()}
                  disabled={!currentModelID}
                  class={`rounded-lg border px-3 py-2 text-sm transition-colors ${
                    !currentModelID
                      ? "border-gray-200 text-gray-300 cursor-not-allowed"
                      : copiedModel
                        ? "border-emerald-200 text-emerald-600"
                        : "border-gray-200 text-gray-600 hover:bg-gray-50"
                  }`}
                  title={t("chat.copyModel")}
                >
                  {copiedModel ? t("dash.copied") : t("chat.copyModel")}
                </button>
              </div>
              <p class="text-sm text-gray-500">{t("aiApps.currentModelHint")}</p>
            </section>
          )}

          {launchPreview && (
            <section class="space-y-2">
              <h3 class="text-sm font-semibold text-gray-900">{t("aiApps.cliLaunch")}</h3>
              <pre class="rounded-xl bg-gray-900 text-gray-100 p-4 text-xs leading-5 overflow-x-auto whitespace-pre-wrap break-all font-mono">
                {launchPreview}
              </pre>
              <p class="text-sm text-gray-500">{t("aiApps.cliLaunchHint")}</p>
            </section>
          )}

          <section class="space-y-2">
            <div class="flex items-center justify-between gap-3">
              <h3 class="text-sm font-semibold text-gray-900">{t("aiApps.installCommand")}</h3>
              <a
                href={app.detailsUrl}
                target="_blank"
                rel="noreferrer"
                class="text-xs text-indigo-600 hover:text-indigo-700 hover:underline"
              >
                {t("aiApps.openDocs")}
              </a>
            </div>
            <pre class="rounded-xl bg-gray-900 text-gray-100 p-4 text-xs leading-5 overflow-x-auto whitespace-pre-wrap break-all font-mono">
              {app.commandPreview}
            </pre>
            <p class="text-sm text-gray-500">{getLocalizedText(app.installHint, locale.value)}</p>
          </section>

          <section class="space-y-2">
            <h3 class="text-sm font-semibold text-gray-900">{t("aiApps.cnHint")}</h3>
            <div class="rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 text-sm text-gray-600">
              {getLocalizedText(app.cnInstallHint, locale.value)}
            </div>
          </section>

          <section class="space-y-3">
            <h3 class="text-sm font-semibold text-gray-900">{t("aiApps.plannedSteps")}</h3>
            <div class="space-y-3">
              {app.plannedSteps.map((step, index) => (
                <div key={`${app.id}-${index}`} class="flex items-start gap-3">
                  <span class="w-6 h-6 rounded-full bg-indigo-50 text-indigo-600 text-xs font-semibold inline-flex items-center justify-center flex-shrink-0">
                    {index + 1}
                  </span>
                  <p class="text-sm text-gray-600 pt-0.5">{getLocalizedText(step, locale.value)}</p>
                </div>
              ))}
            </div>
          </section>

          {showTaskLogs && (
            <section class="space-y-3">
              <div class="flex items-center justify-between gap-3">
                <h3 class="text-sm font-semibold text-gray-900">{t("aiApps.taskLogs")}</h3>
                <span class="inline-flex items-center gap-1.5 text-xs text-gray-500">
                  <span class={`w-2 h-2 rounded-full ${drawerStreaming.value ? "bg-indigo-400" : "bg-gray-300"}`} />
                  {drawerStreaming.value ? t("aiApps.streaming") : t("aiApps.paused")}
                </span>
              </div>
              <div ref={logRef} class="rounded-xl bg-gray-900 p-4 h-64 overflow-auto font-mono text-xs leading-5">
                {drawerLogs.value.length === 0 ? (
                  <span class="text-gray-500">{t("aiApps.waitLogs")}</span>
                ) : (
                  drawerLogs.value.map((line, index) => <LogLine key={`${app.id}-${index}`} line={line} />)
                )}
              </div>
              <p class="text-xs text-gray-500">{t("aiApps.logsScope")}</p>
            </section>
          )}
        </div>

        <div class="px-6 py-4 border-t border-gray-100 flex items-center justify-between gap-3">
          <div class="min-w-0">
            {showRuntimeSummary && (
              <span class="inline-flex items-center gap-2 text-sm text-gray-600">
                <RuntimeStatusDot state={state} className="h-2.5 w-2.5" />
                <span>{runtimeStatusLabel(state)}</span>
              </span>
            )}
          </div>
          <div class="flex items-center gap-3">
            {showRuntimeSummary && !isWorking && state.runtimeRunning && (
                <button
                  onClick={onStop}
                  disabled={pendingStop}
                  class={`inline-flex items-center justify-center rounded-lg border px-4 py-2 text-sm font-medium transition-colors ${
                    pendingStop
                      ? "border-gray-200 text-gray-300 cursor-not-allowed"
                      : "border-red-200 text-red-600 hover:bg-red-50"
                  }`}
                >
                  {pendingStop ? t("aiApps.stopping") : t("aiApps.stop")}
                </button>
            )}
            {canOpenChat && (
              <button
                onClick={() => void handleOpenCurrentApp()}
                disabled={pendingOpen}
                class={`inline-flex items-center justify-center rounded-lg border px-4 py-2 text-sm font-medium transition-colors ${
                  pendingOpen
                    ? "border-gray-200 text-gray-300 cursor-not-allowed"
                    : "border-indigo-200 text-indigo-600 hover:bg-indigo-50"
                }`}
              >
                {pendingOpen ? t("aiApps.opening") : t("aiApps.open")}
              </button>
            )}
            {!state.disabled && !isWorking && state.status === "installed" && state.managed && (
              <button
                onClick={onUninstall}
                disabled={requestPending}
                class={`inline-flex items-center justify-center rounded-lg border px-4 py-2 text-sm font-medium transition-colors ${
                  requestPending
                    ? "border-gray-200 text-gray-300 cursor-not-allowed"
                    : "border-red-200 text-red-600 hover:bg-red-50"
                }`}
              >
                {pendingUninstall ? t("aiApps.status.uninstalling") : t("aiApps.uninstall")}
              </button>
            )}
            {!state.disabled && !isWorking && (state.status !== "installed" || state.managed) && (
              <button
                onClick={onInstall}
                disabled={requestPending}
                class={`inline-flex items-center justify-center rounded-lg border px-4 py-2 text-sm font-medium transition-colors ${
                  requestPending
                    ? "border-gray-200 text-gray-300 cursor-not-allowed"
                    : "border-indigo-200 text-indigo-600 hover:bg-indigo-50"
                }`}
              >
                {actionLabel(state)}
              </button>
            )}
          </div>
        </div>
      </div>
      {showCloudAuthDialog && (
        <div class="fixed inset-0 z-[60] flex items-center justify-center bg-gray-900/40 px-4" onClick={(e) => e.stopPropagation()}>
          <div class="w-full max-w-lg rounded-2xl bg-white p-6 shadow-2xl">
            {currentModelInfo && (
              <div class="mb-4 flex items-center justify-between rounded-lg bg-indigo-50 px-3 py-2">
                <div class="flex items-center gap-2 min-w-0">
                  <svg class="w-4 h-4 text-indigo-600 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
                  </svg>
                  <span class="truncate text-sm font-medium text-indigo-700">{formatAIAppModelLabel(currentModelInfo)}</span>
                </div>
                <button
                  onClick={() => {
                    void navigator.clipboard.writeText(currentModelInfo.model || currentModelInfo.name || "");
                  }}
                  class="rounded p-1 text-indigo-600 hover:bg-indigo-100 transition-colors"
                  title={t("chat.copyModel")}
                >
                  <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                  </svg>
                </button>
              </div>
            )}
            <div class="flex items-start justify-between gap-4">
              <div>
                <h3 class="text-lg font-semibold text-gray-900">{t("chat.cloudApiKeyTitle")}</h3>
                <p class="mt-2 text-sm leading-6 text-gray-500">{t("chat.cloudApiKeyDesc")}</p>
              </div>
              <button
                onClick={() => {
                  setShowCloudAuthDialog(false);
                  setCloudAuthError("");
                }}
                class="rounded-lg p-1 text-gray-400 hover:text-gray-600"
                aria-label={t("chat.cloudCancel")}
              >
                <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>

            <div class="mt-5 rounded-xl border border-gray-200 bg-gray-50 px-4 py-3">
              <div class="text-xs font-medium uppercase tracking-wide text-gray-400">{t("chat.cloudGatewayLabel")}</div>
              <div class="mt-1 text-sm font-medium text-gray-800">{t("chat.cloudGatewayValue")}</div>
            </div>

            {cloudAuthError && (
              <div class="mt-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
                {cloudAuthError}
              </div>
            )}

            <div class="mt-5">
              <div class="mb-2 flex items-center justify-between gap-3">
                <label class="block text-sm font-medium text-gray-700">{t("chat.cloudApiKeyLabel")}</label>
                <a
                  href="https://opencsg.com/settings/api-keys"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="text-sm font-medium text-indigo-600 hover:text-indigo-700"
                >
                  {t("chat.cloudApiKeyHelp")}
                </a>
              </div>
              <input
                type="password"
                autoComplete="off"
                spellcheck={false}
                class="w-full rounded-lg border border-gray-200 px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder={t("chat.cloudApiKeyPlaceholder")}
                value={cloudTokenInput}
                onInput={(e) => setCloudTokenInput((e.currentTarget as HTMLInputElement).value)}
              />
              <p class="mt-2 text-xs leading-5 text-gray-500">{t("chat.cloudApiKeyHint")}</p>
            </div>

            <div class="mt-5 flex justify-end gap-2">
              <button
                onClick={() => {
                  setShowCloudAuthDialog(false);
                  setCloudAuthError("");
                }}
                class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50 transition-colors"
              >
                {t("chat.cloudCancel")}
              </button>
              <button
                onClick={() => void handleSaveCloudToken()}
                disabled={isSavingCloudToken}
                class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white hover:bg-indigo-700 disabled:opacity-60 transition-colors"
              >
                {isSavingCloudToken ? t("chat.cloudApiKeySaving") : t("chat.cloudApiKeySave")}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function PercentProgress({ value, label }: { value: number; label: string }) {
  return (
    <div>
      <div class="flex items-center justify-between gap-2 mb-1">
        <span class="text-[11px] text-gray-500 truncate">{label}</span>
        <span class="text-[11px] font-medium text-gray-600">{value}%</span>
      </div>
      <div class="h-1.5 rounded-full bg-gray-200 overflow-hidden">
        <div class="h-full rounded-full bg-indigo-500 transition-all duration-300" style={{ width: `${Math.max(value, 4)}%` }} />
      </div>
    </div>
  );
}

function SpinnerProgress({ label }: { label: string }) {
  return (
    <div class="flex items-center gap-2 text-[11px] text-gray-500">
      <span class="inline-block w-3.5 h-3.5 border-2 border-indigo-500 border-t-transparent rounded-full animate-spin" />
      <span class="truncate">{label}</span>
    </div>
  );
}

function SummaryTile({ label, value }: { label: string; value: ComponentChildren }) {
  return (
    <div class="rounded-xl border border-gray-200 bg-gray-50 px-4 py-3">
      <div class="text-[11px] uppercase tracking-wide text-gray-400 font-medium">{label}</div>
      <div class="text-sm font-semibold text-gray-900 mt-1 break-all">{value}</div>
    </div>
  );
}

function LogLine({ line }: { line: string }) {
  let color = "text-gray-300";
  if (line.includes("INFO:")) color = "text-green-400";
  else if (line.includes("WARN:")) color = "text-yellow-400";
  else if (line.includes("ERROR:")) color = "text-red-400";
  else if (line.includes("REQUEST:")) color = "text-blue-400";
  return <div class={color}>{line}</div>;
}

function InstalledCorner() {
  return (
    <div class="absolute top-0 right-0">
      <div class="w-0 h-0 border-l-[34px] border-l-transparent border-t-[34px] border-t-indigo-400" />
      <CheckIcon className="w-3.5 h-3.5 text-white absolute top-1.5 right-1.5" />
    </div>
  );
}

function RuntimeStatusCorner({ state }: { state: AIAppRuntimeState }) {
  const label = runtimeStatusLabel(state);

  return (
    <span
      class="absolute top-3 right-3 inline-flex h-3 w-3"
      title={label}
      aria-label={label}
    >
      <RuntimeStatusDot state={state} className="h-3 w-3" />
    </span>
  );
}

function RuntimeStatusDot({ state, className }: { state: AIAppRuntimeState; className: string }) {
  const isRunning = state.runtimeRunning || state.runtimeStatus === "running";

  return (
    <span class={`relative inline-flex ${className}`}>
      {isRunning && <span class="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />}
      <span class={`relative inline-flex rounded-full shadow-sm ring-2 ring-white ${className} ${runtimeStatusDotClass(state)}`} />
    </span>
  );
}

function mergeAppStates(remoteApps: RemoteAIAppInfo[]) {
  const next = { ...appStates.value };
  const fallback = createAIAppStateSnapshot();

  for (const remote of remoteApps) {
    const prev = next[remote.id] || fallback[remote.id];
    next[remote.id] = {
      ...prev,
      status: remote.status,
      phase: remote.phase || prev.phase,
      progressMode: remote.progress_mode,
      progress: typeof remote.progress === "number" ? remote.progress : prev.progress,
      managed: remote.managed,
      supported: remote.supported,
      disabled: remote.disabled,
      installPath: remote.install_path,
      version: remote.version,
      latestVersion: remote.latest_version,
      updateAvailable: Boolean(remote.update_available),
      modelID: remote.model_id || "",
      runtimeSupported: Boolean(remote.runtime_supported),
      runtimeRunning: Boolean(remote.runtime_running),
      runtimeStatus: remote.runtime_status || (remote.runtime_supported ? "stopped" : undefined),
      logPath: remote.log_path,
      lastError: remote.last_error,
    };
  }

  appStates.value = next;
}

function getCategoryLabel(category: AIAppCategory, currentLocale: Locale): string {
  const option = aiAppCategoryOptions.find((item) => item.id === category);
  return option ? getLocalizedText(option.label, currentLocale) : category;
}

function categoryClassName(category: AIAppCategory): string {
  if (category === "coding") {
    return "bg-sky-50 text-sky-700";
  }
  if (category === "automation") {
    return "bg-indigo-50 text-indigo-700";
  }
  return "bg-violet-50 text-violet-700";
}

function renderProgressValue(progressMode: AIAppProgressMode, progress: number | undefined): ComponentChildren {
  if (progressMode === "percent" && typeof progress === "number") {
    return `${progress}%`;
  }
  const label = t("aiApps.indeterminate");
  return (
    <span class="inline-flex h-5 items-center" title={label} aria-label={label}>
      <span class="inline-block w-4 h-4 border-2 border-indigo-500 border-t-transparent rounded-full animate-spin" />
      <span class="sr-only">{label}</span>
    </span>
  );
}

function installModeLabel(mode: AIAppInstallMode): string {
  if (mode === "npm") return t("aiApps.modeNpm");
  if (mode === "docker") return t("aiApps.modeDocker");
  return t("aiApps.modeScript");
}

function statusLabel(state: AIAppRuntimeState): string {
  if (state.status === "installing" || state.status === "uninstalling") {
    return phaseLabel(state.phase);
  }
  return t(`aiApps.status.${state.status}`);
}

function updateStatusLabel(state: AIAppRuntimeState): string {
  if (!state.latestVersion) return t("aiApps.latestUnknown");
  if (state.status !== "installed") return t("aiApps.latestKnown");
  return state.updateAvailable ? t("aiApps.updateAvailable") : t("aiApps.upToDate");
}

function phaseLabel(phase: string): string {
  const key = `aiApps.phase.${phase}`;
  const translated = t(key);
  return translated === key ? phase : translated;
}

function statusColorClass(status: AIAppRuntimeState["status"]): string {
  if (status === "installed") return "text-indigo-600";
  if (status === "uninstalling") return "text-amber-600";
  if (status === "failed") return "text-red-600";
  if (status === "disabled") return "text-gray-500";
  return "text-gray-600";
}

function actionLabel(state: AIAppRuntimeState): string {
  if (state.disabled) return t("aiApps.disabled");
  if (state.status === "installed") return t("aiApps.reinstallLatest");
  if (state.status === "failed") return t("aiApps.retryInstall");
  return t("aiApps.install");
}

function drawerNotice(state: AIAppRuntimeState): string {
  if (state.disabled) {
    return t("aiApps.disabledDockerNotice");
  }
  if (state.phase === "uninstall_failed") {
    return state.lastError ? `${t("aiApps.uninstallFailed")}: ${state.lastError}` : t("aiApps.uninstallFailed");
  }
  if (state.status === "failed" && state.lastError) {
    return `${t("aiApps.installFailed")}: ${state.lastError}`;
  }
  if (state.status === "failed") {
    return t("aiApps.installFailed");
  }
  if (state.status === "uninstalling") {
    return `${t("aiApps.uninstallRunning")}: ${phaseLabel(state.phase)}`;
  }
  if (state.status === "installing") {
    return `${t("aiApps.installRunning")}: ${phaseLabel(state.phase)}`;
  }
  if (state.status === "installed" && !state.managed) {
    return t("aiApps.installedExternalReady");
  }
  if (state.status === "installed" && state.updateAvailable && state.latestVersion) {
    return t("aiApps.updateAvailableNotice", state.latestVersion);
  }
  if (state.status === "installed") {
    return t("aiApps.installedReady");
  }
  return t("aiApps.readyToInstall");
}

function canOpenAIApp(app: AIAppCatalogEntry, state: AIAppRuntimeState): boolean {
  return ["openclaw", "csgclaw", "claude-code", "open-code", "codex", "pi"].includes(app.id) &&
    state.status === "installed" &&
    !state.disabled;
}

function canControlAIAppRuntime(state: AIAppRuntimeState): boolean {
  return state.runtimeSupported &&
    state.status === "installed" &&
    !state.disabled;
}

function runtimeStatusLabel(state: AIAppRuntimeState): string {
  return state.runtimeRunning || state.runtimeStatus === "running"
    ? t("aiApps.runtime.running")
    : t("aiApps.runtime.stopped");
}

function runtimeStatusDotClass(state: AIAppRuntimeState): string {
  return state.runtimeRunning || state.runtimeStatus === "running"
    ? "bg-emerald-500"
    : "bg-red-500";
}

function cliLaunchPreview(app: AIAppCatalogEntry, modelID: string): string {
  const launchName = cliLaunchAppName(app.id);
  if (!launchName) {
    return "";
  }
  const launchWithModel = modelID
    ? `csghub-lite launch ${launchName} --model "${modelID}"`
    : `csghub-lite launch ${launchName} --model "<model-id>"`;
  return [
    `csghub-lite launch ${launchName}`,
    launchWithModel,
    `${launchWithModel} -- --help`,
  ].join("\n");
}

function cliLaunchAppName(appID: string): string {
  switch (appID) {
    case "claude-code":
      return "claude";
    case "open-code":
      return "opencode";
    case "codex":
      return "codex";
    case "pi":
      return "pi";
    case "openclaw":
      return "openclaw";
    case "csgclaw":
      return "csgclaw";
    default:
      return "";
  }
}

function canSelectAIAppModel(app: AIAppCatalogEntry): boolean {
  return ["claude-code", "open-code", "codex", "pi", "openclaw", "csgclaw"].includes(app.id);
}

function normalizeAIAppModels(models: ModelInfo[]): ModelInfo[] {
  const seen = new Set<string>();
  const out: ModelInfo[] = [];
  for (const model of models) {
    const modelId = model.model?.trim();
    const key = aiAppModelKey(model);
    if (!modelId || seen.has(key)) {
      continue;
    }
    seen.add(key);
    out.push(model);
  }
  return out;
}

function localizeAIAppErrorMessage(message: string, fallback: string): string {
  const trimmed = message.trim();
  if (!trimmed) {
    return fallback;
  }

  const normalized = trimmed.toLowerCase();
  if (normalized.includes("no local models were found") && normalized.includes("access token")) {
    return t("aiApps.error.noLocalModelsOpenCSG");
  }
  if (normalized.includes("no local or opencsg models were found")) {
    return t("aiApps.error.noLocalOrCloudModels");
  }
  if (normalized.includes("no models were found. pull a model first")) {
    return t("aiApps.error.noModelsFound");
  }
  if (normalized.includes("is not available for ai apps") && normalized.includes("access token first")) {
    const requestedModel = extractAIAppRequestedModel(trimmed);
    return requestedModel
      ? t("aiApps.error.modelUnavailableWithCloudHint", requestedModel)
      : t("aiApps.error.modelUnavailableCloudHint");
  }
  if (normalized.includes("is not available for ai apps")) {
    const requestedModel = extractAIAppRequestedModel(trimmed);
    return requestedModel
      ? t("aiApps.error.modelUnavailable", requestedModel)
      : fallback;
  }
  return trimmed;
}

function extractAIAppRequestedModel(message: string): string {
  const quotedMatch = message.match(/^model\s+"(.+?)"\s+is not available for ai apps/i);
  if (quotedMatch?.[1]) {
    return quotedMatch[1];
  }
  const plainMatch = message.match(/^model\s+(.+?)\s+is not available for ai apps/i);
  return plainMatch?.[1]?.trim() || "";
}

function formatAIAppModelLabel(model: ModelInfo): string {
  const name = model.display_name || model.model;
  const src = model.source || "local";
  if (src === "cloud") {
    return `${name} [${t("aiApps.modelSourceCloud")}]`;
  }
  if (src.startsWith("provider:")) {
    return name;
  }
  return `${name} [${t("aiApps.modelSourceLocal")}]`;
}

function CheckIcon({ className }: { className: string }) {
  return (
    <svg class={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
    </svg>
  );
}

function InstallIcon({ className }: { className: string }) {
  return (
    <svg class={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M12 5v10m0 0l-4-4m4 4l4-4M5 19h14" />
    </svg>
  );
}

function OpenIcon({ className }: { className: string }) {
  return (
    <svg class={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M13 7h4m0 0v4m0-4l-7 7" />
      <path stroke-linecap="round" stroke-linejoin="round" d="M7 7h2a2 2 0 012 2v8a2 2 0 01-2 2H7a2 2 0 01-2-2V9a2 2 0 012-2z" />
    </svg>
  );
}

function openAppPopup(): Window | null {
  if (typeof window === "undefined") {
    return null;
  }
  const popup = window.open("", "_blank");
  if (!popup) {
    return null;
  }
  popup.opener = null;
  popup.document.title = t("aiApps.opening");
  popup.document.body.style.margin = "0";
  popup.document.body.style.fontFamily = "ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif";
  popup.document.body.style.backgroundColor = "#f9fafb";
  popup.document.body.style.color = "#111827";
  popup.document.body.innerHTML = `
    <div style="min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px;">
      <div style="max-width:420px;width:100%;background:#ffffff;border:1px solid #e5e7eb;border-radius:16px;padding:24px;box-shadow:0 10px 30px rgba(15,23,42,0.08);">
        <div style="display:flex;align-items:center;gap:12px;">
          <div style="width:12px;height:12px;border-radius:9999px;background:#818cf8;"></div>
          <div style="font-size:14px;font-weight:600;">${escapeHTML(t("aiApps.opening"))}</div>
        </div>
        <p style="margin:12px 0 0 24px;font-size:13px;line-height:1.6;color:#6b7280;">${escapeHTML(t("aiApps.openingWindowHint"))}</p>
      </div>
    </div>
  `;
  return popup;
}

function escapeHTML(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}
