import { useEffect, useState } from "preact/hooks";
import { getCluster, getClusterDeployments, isClusterDisabledError, isFeatureNotLicensedError } from "../api/client";
import type { ClusterDeployment } from "../api/client";
import { localDeploymentAssignments, deploymentProgress, deploymentPhaseKey } from "../deploymentModels";
import { locale, t } from "../i18n";
import { formatSpeed } from "../utils/format";
import { DeploymentMessage } from "./DeploymentMessage";

// Read the node's durable deployment receipts, not browser-local pull history.
// Managed runtimes share ordinary library artifacts; lifecycle stays on Master.
export function useLocalDeployments() {
  const [tasks, setTasks] = useState<ClusterDeployment[] | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true, pending = false;
    const poll = async () => {
      if (pending) return;
      pending = true;
      try {
        const view = await getCluster();
        if (!active) return;
        if (!view.cluster) { setTasks([]); setError(""); return; }
        const result = await getClusterDeployments();
        if (active) { setTasks(localDeploymentAssignments(result.assignments, view)); setError(""); }
      } catch (e) {
        if (!active) return;
        if (isClusterDisabledError(e) || isFeatureNotLicensedError(e)) { setTasks([]); setError(""); }
        else setError((e as Error).message);
      } finally { pending = false; }
    };
    void poll();
    const timer = window.setInterval(() => void poll(), 2000);
    return () => { active = false; window.clearInterval(timer); };
  }, []);
  return { tasks: tasks ?? [], error };
}

export function DeploymentTasksPanel({ tasks, error, title, description }: {
  tasks: ClusterDeployment[] | null; error: string; title: string; description: string;
}) {
  void locale.value;
  return <section class="overflow-hidden rounded-xl border border-gray-200 bg-white">
    <div class="border-b border-gray-100 p-5"><h2 class="font-semibold text-gray-900">{title}</h2><p class="mt-1 text-sm text-gray-500">{description}</p></div>
    {error && <p role="alert" class="m-5 rounded-lg bg-amber-50 p-3 text-sm text-amber-800">{t("cluster.workerTasksError")} {error}</p>}
    {tasks === null ? !error && <p class="p-8 text-center text-sm text-gray-400">{t("cluster.loading")}</p>
      : !tasks.length ? <p class="p-8 text-center text-sm text-gray-500">{t("cluster.workerTasksEmpty")}</p>
      : <ul class="divide-y divide-gray-100">{tasks.map(task => {
        const progress = deploymentProgress(task.status);
        const downloading = task.status.phase === "downloading";
        const phase = task.desired === "deleted" && task.status.phase !== "failed" ? "deleting" : task.status.phase;
        const updated = task.status.updated_at ? new Date(task.status.updated_at) : null;
        return <li key={task.id} class="p-5" data-deployment-id={task.id}>
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0"><p class="break-words text-sm font-medium text-gray-900">{task.name}</p>
              <p class="mt-1 break-all text-xs text-gray-500">{task.repository} · {task.artifact_source} · {t(`deployments.${task.kind}`)}</p>
            </div>
            <span class={`rounded-md px-2 py-1 text-xs ${phase === "ready" ? "bg-green-50 text-green-700" : phase === "failed" ? "bg-red-50 text-red-700" : "bg-gray-100 text-gray-600"}`}>{t(deploymentPhaseKey(phase))}</span>
          </div>
          <p class="mt-2 text-xs text-gray-400">{t(task.desired === "deleted" ? "cluster.workerDesiredDeleted" : task.desired === "running" ? "cluster.workerDesiredRunning" : "cluster.workerDesiredStopped")}{updated && !Number.isNaN(updated.getTime()) ? ` · ${updated.toLocaleString(locale.value)}` : ""}</p>
          {downloading && <div class="mt-3 space-y-2">
            <div class="flex items-center justify-between gap-3 text-xs text-gray-500">
              <span>{formatDeploymentBytes(progress.completed)} / {progress.total === null ? t("lib.managedUnknownSize") : formatDeploymentBytes(progress.total)}</span>
              <span class="flex items-baseline gap-2 tabular-nums"><span class="text-gray-400">{formatSpeed(task.status.speed_bytes_per_sec)}</span><span>{progress.percent === null ? t("downloads.downloading") : `${progress.percent}%`}</span></span>
            </div>
            <progress aria-label={t("cluster.workerDownloadProgress")} class="block h-1.5 w-full accent-indigo-600" {...(progress.percent === null ? {} : { value: progress.percent, max: 100 })} />
          </div>}
          {task.status.error && <DeploymentMessage message={task.status.error} alert class="mt-2 text-sm text-red-600" />}
          {task.status.detail && !task.status.error && <DeploymentMessage message={`${downloading ? `${t("lib.managedCurrentFile")}: ` : ""}${task.status.detail}`} class="mt-2 text-xs text-gray-500" />}
        </li>;
      })}</ul>}
  </section>;
}

function formatDeploymentBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  return `${(bytes / 1024 ** 3).toFixed(2)} GB`;
}
