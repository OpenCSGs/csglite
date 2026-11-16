import type { ClusterView } from "../api/client";
import { shortVersion, stateKey, workerNodeState } from "../cluster";
import { t } from "../i18n";

export function ClusterWorkerHeader({ view, settings, onSettings, onOverview }: {
  view: ClusterView; settings: boolean; onSettings: () => void; onOverview: () => void;
}) {
  const master = view.members.find(node => node.uuid === view.cluster?.gateway_uuid);
  const local = view.members.find(node => node.local)?.status;
  const state = workerNodeState(view.settings);
  return <header class="space-y-4" data-worker-header>
    <div class="flex flex-col items-start justify-between gap-4 sm:flex-row">
      <div class="min-w-0 w-full sm:flex-1">
        <p class="text-sm text-gray-500">{t("cluster.workerTitle")}</p>
        <h1 class="mt-1 break-words text-xl font-semibold tracking-tight text-gray-950 sm:text-3xl">{view.node.name}</h1>
        <p class="mt-2 text-xs text-gray-500">{[local?.os, local?.arch, shortVersion(local?.version || view.node.version)].filter(Boolean).join(" · ")}</p>
      </div>
      <button type="button" onClick={settings ? onOverview : onSettings}
        class="inline-flex shrink-0 items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500">
        {settings ? <svg aria-hidden="true" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path stroke-linecap="round" stroke-linejoin="round" d="m12 19-7-7 7-7M5 12h14" /></svg>
          : <svg aria-hidden="true" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path stroke-linecap="round" d="M4 7h9m4 0h3M4 17h3m4 0h9" /><circle cx="15" cy="7" r="2" /><circle cx="9" cy="17" r="2" /></svg>}
        {t(settings ? "cluster.workerBackToModels" : "cluster.workerSettingsTab")}
      </button>
    </div>
    <div class="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-gray-200 pt-4 text-xs">
      <span class={`inline-flex shrink-0 items-center gap-2 font-medium ${master?.online ? "text-emerald-700" : "text-amber-700"}`}>
        <span aria-hidden="true" class={`h-1.5 w-1.5 rounded-full ${master?.online ? "bg-emerald-500" : "bg-amber-500"}`} />
        {t(master?.online ? "cluster.masterOnline" : "cluster.masterOffline")}
      </span>
      <span class="min-w-0 break-all text-gray-500">{[view.cluster?.name, view.gateway_url].filter(Boolean).join(" · ")}</span>
      {state !== "active" && <span class="rounded-md bg-amber-50 px-2 py-1 font-medium text-amber-700">{t(stateKey(state))}</span>}
    </div>
    {(!master?.online || (local && !local.licensed) || state !== "active") && <div role="status" class="space-y-1 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
      {!master?.online && <p>{t("cluster.workerDisconnected")}</p>}
      {local && !local.licensed && <p>{t("cluster.workerQuotaBlocked")}</p>}
      {state !== "active" && <p>{t("cluster.workerPaused")}</p>}
    </div>}
  </header>;
}
