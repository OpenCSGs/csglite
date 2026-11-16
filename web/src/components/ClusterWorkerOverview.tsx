import { useEffect, useState } from "preact/hooks";
import { getPs } from "../api/client";
import type { ClusterView, RunningModel } from "../api/client";
import { fmtGB } from "../cluster";
import { DeploymentMessage } from "./DeploymentMessage";
import { t } from "../i18n";

export function ClusterWorkerOverview({ view }: { view: ClusterView }) {
  const [running, setRunning] = useState<RunningModel[] | null>(null);
  const [runningError, setRunningError] = useState("");
  useEffect(() => {
    let active = true, pending = false;
    setRunning(null); setRunningError("");
    const poll = async () => {
      if (pending) return;
      pending = true;
      try {
        const models = await getPs();
        if (active) { setRunning(models); setRunningError(""); }
      } catch (error) {
        if (active) setRunningError(error instanceof Error ? error.message : String(error));
      } finally {
        pending = false;
      }
    };
    void poll();
    const timer = setInterval(() => void poll(), 3000);
    return () => { active = false; clearInterval(timer); };
  }, [view.node.uuid]);
  const st = view.members.find(node => node.local)?.status;
  return (
    <section class="overflow-hidden rounded-xl border border-gray-200 bg-white" data-worker-running>
      <div class="flex flex-wrap items-center justify-between gap-2 border-b border-gray-100 px-5 py-4 sm:px-6">
        <h2 class="flex items-center gap-2 text-sm font-semibold text-gray-900">{t("cluster.workerRunningTitle")}
          {running !== null && <span class="rounded-md bg-gray-100 px-2 py-0.5 text-xs font-medium tabular-nums text-gray-600">{running.length}</span>}
        </h2>
        <span class="text-xs text-gray-400">{t("cluster.workerAutoRefresh")}</span>
      </div>
      {runningError && <DeploymentMessage alert class="m-5 rounded-lg bg-amber-50 p-3 text-sm text-amber-800" message={`${t(running === null ? "cluster.workerRunningLoadError" : "cluster.workerRunningError")} ${runningError}`} />}
      {running === null ? !runningError && <p class="px-5 py-16 text-center text-sm text-gray-400">{t("cluster.loading")}</p>
        : !running.length ? <p class="px-5 py-16 text-center text-sm text-gray-500">{t("cluster.workerRunningEmpty")}</p>
        : <ul class="divide-y divide-gray-100">{running.map(model => {
          const id = model.model || model.name;
          const managed = id.startsWith("deployment/");
          const metadata = st?.models.find(item => item.id === id);
          const repository = model.repository || metadata?.repo;
          const category = metadata?.category === "language_model" ? "chat" : metadata?.category;
          const kind = category && ["chat", "embedding", "image", "asr", "tts"].includes(category) ? t(`deployments.${category}`) : model.format;
          return <li key={id} class="flex flex-col items-start gap-3 px-4 py-5 sm:flex-row sm:gap-4 sm:px-6">
            <div aria-hidden="true" class="hidden h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-indigo-50 text-indigo-500 sm:flex">
              <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path stroke-linejoin="round" d="m12 3 8 4.5v9L12 21l-8-4.5v-9L12 3Zm0 9 8-4.5M12 12 4 7.5M12 12v9" /></svg>
            </div>
            <div class="min-w-0 w-full sm:flex-1"><p class="break-words text-sm font-medium text-gray-900">{model.display_name || (managed ? repository?.split("/").pop() || t("cluster.workerManagedModel") : model.name)}</p>
              <p class="mt-1 break-all text-xs text-gray-500">{[repository, kind, model.size > 0 ? `${fmtGB(model.size)} GB` : ""].filter(Boolean).join(" · ")}</p>
              <p class="mt-2 text-xs text-gray-400">{t(managed ? "cluster.workerManagedModel" : "cluster.workerLocalModel")}{!managed && view.settings.shared_models?.includes(id) ? ` · ${t("cluster.workerSharedModel")}` : ""}</p>
            </div>
            <span class={`shrink-0 rounded-full px-2.5 py-1 text-xs font-medium ${model.status === "loading" ? "bg-indigo-50 text-indigo-700" : "bg-green-50 text-green-700"}`}>{t(model.status === "loading" ? "deployments.phase.loading" : "cluster.workerRuntimeRunning")}</span>
          </li>;
        })}</ul>}
    </section>
  );
}
