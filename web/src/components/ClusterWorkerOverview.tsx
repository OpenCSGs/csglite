import { DeploymentTasksPanel } from "./LocalDeploymentModels";
import { useEffect, useState } from "preact/hooks";
import { getClusterDeployments, getPs } from "../api/client";
import type { ClusterDeployment, ClusterView, RunningModel } from "../api/client";
import { clusterNodeRole, fmtGB, shortVersion, stateKey, workerNodeState } from "../cluster";
import { nodeMemory, formatMemory, formatMemoryPair } from "../clusterMemory";
import { workerDeploymentAssignments } from "../deploymentModels";
import { t } from "../i18n";

export function ClusterWorkerOverview({ view, onSettings }: { view: ClusterView; onSettings: () => void }) {
  const [tasks, setTasks] = useState<ClusterDeployment[] | null>(null);
  const [error, setError] = useState("");
  const [running, setRunning] = useState<RunningModel[] | null>(null);
  const [runningError, setRunningError] = useState("");
  useEffect(() => {
    let active = true, pending = false;
    setTasks(null); setError(""); setRunning(null); setRunningError("");
    const poll = async () => {
      if (pending) return;
      pending = true;
      const [assignments, runtimes] = await Promise.allSettled([getClusterDeployments(), getPs()]);
      if (active) {
        if (assignments.status === "fulfilled") {
          setTasks(workerDeploymentAssignments(assignments.value.assignments, view.node.uuid, view.cluster?.gateway_uuid)); setError("");
        } else setError(String(assignments.reason?.message || assignments.reason));
        if (runtimes.status === "fulfilled") { setRunning(runtimes.value); setRunningError(""); }
        else setRunningError(String(runtimes.reason?.message || runtimes.reason));
      }
      pending = false;
    };
    void poll();
    const timer = setInterval(() => void poll(), 3000);
    return () => { active = false; clearInterval(timer); };
  }, [view.node.uuid, view.cluster?.gateway_uuid]);
  const node = view.members.find(node => node.local);
  const st = node?.status;
  const gpus = st?.gpus ?? [];
  const memory = nodeMemory(node), ram = nodeMemory(node, true);
  const gpuValue = !st ? "—" : !gpus.length || !memory.total ? t("dash.na") : formatMemoryPair(memory.free, memory.total);
  const state = workerNodeState(view.settings);
  return <div class="space-y-5">
    <section class="rounded-xl border border-gray-200 bg-white p-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <div class="flex flex-wrap items-center gap-2"><h2 class="font-semibold text-gray-900">{view.node.name}</h2><span class="rounded-md bg-gray-100 px-2 py-1 text-xs text-gray-600">{t("cluster.role.slave")}</span></div>
          <p class="mt-1 text-xs text-gray-500">{[st?.os, st?.arch, shortVersion(st?.version || view.node.version)].filter(Boolean).join(" · ")}</p>
        </div>
        <button type="button" class="rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50" onClick={onSettings}>{t("cluster.workerSettingsTab")}</button>
      </div>
      <div class="mt-5 grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-5" data-worker-summary>
        <Metric label={t("cluster.workerState")} value={t(stateKey(state))} />
        <Metric label={t("cluster.workerInflight")} value={st ? String(st.inflight) : "—"} />
        <Metric label={t("cluster.statRole")} value={t(`cluster.role.${clusterNodeRole(view.node.uuid, view.cluster)}`)} />
        <Metric label={t("cluster.workerRunning")} value={running ? String(running.filter(model => model.status !== "loading").length) : "—"} />
        <Metric label={t("cluster.workerDisk")} value={st && st.disk.total > 0 ? `${fmtGB(st.disk.free)} GB` : "—"} />
      </div>
      <div class="mt-3 grid gap-3 sm:grid-cols-2">
        <Metric label={t("cluster.statVRAM")} value={gpuValue} hint={[...gpus.map(gpu => gpu.name), memory.kind === "unified" ? t("dash.unifiedMemory") : "", t("cluster.memoryFreeTotal")].filter(Boolean).join(" · ")} />
        <Metric label={t("cluster.workerRAM")} value={st && st.ram.total > 0 ? formatMemoryPair(Math.min(st.ram.used, st.ram.total), st.ram.total) : "—"} hint={st ? [t("cluster.workerCPU", st.cpu.cores), ram.free === null ? t("deployments.memoryFreeUnknown") : t("deployments.memoryFree", formatMemory(ram.free))].join(" · ") : undefined} />
      </div>
      {st && !st.licensed && <p role="status" class="mt-4 rounded-lg bg-amber-50 p-3 text-sm text-amber-800">{t("cluster.workerQuotaBlocked")}</p>}
      {state !== "active" && <p class="mt-4 text-sm text-amber-700">{t("cluster.workerPaused")}</p>}
    </section>
    <section class="overflow-hidden rounded-xl border border-gray-200 bg-white" data-worker-running>
      <div class="border-b border-gray-100 p-5"><h2 class="font-semibold text-gray-900">{t("cluster.workerRunningTitle")}</h2><p class="mt-1 text-sm text-gray-500">{t("cluster.workerRunningHint")}</p></div>
      {runningError && <p role="alert" class="m-5 rounded-lg bg-amber-50 p-3 text-sm text-amber-800">{t("cluster.workerRunningError")} {runningError}</p>}
      {running === null ? !runningError && <p class="p-8 text-center text-sm text-gray-400">{t("cluster.loading")}</p>
        : !running.length ? <p class="p-8 text-center text-sm text-gray-500">{t("cluster.workerRunningEmpty")}</p>
        : <ul class="divide-y divide-gray-100">{running.map(model => {
          const id = model.model || model.name;
          const managed = id.startsWith("deployment/");
          const task = tasks?.find(task => `deployment/${task.id}` === id);
          const category = task?.kind || st?.models.find(item => item.id === id)?.category;
          const kind = category && ["chat", "embedding", "image", "asr", "tts"].includes(category) ? t(`deployments.${category}`) : model.format;
          return <li key={id} class="flex flex-wrap items-start justify-between gap-3 p-5">
            <div class="min-w-0"><p class="break-all text-sm font-medium text-gray-900">{model.display_name || task?.name || (managed ? t("cluster.workerManagedModel") : model.name)}</p>
              <p class="mt-1 break-all text-xs text-gray-500">{[model.repository || task?.repository, kind, model.size > 0 ? `${fmtGB(model.size)} GB` : ""].filter(Boolean).join(" · ")}</p>
              <p class="mt-2 text-xs text-gray-400">{t(managed ? "cluster.workerManagedModel" : "cluster.workerLocalModel")}{!managed && view.settings.shared_models?.includes(id) ? ` · ${t("cluster.workerSharedModel")}` : ""}</p>
            </div>
            <span class={`rounded-md px-2 py-1 text-xs ${model.status === "loading" ? "bg-indigo-50 text-indigo-700" : "bg-green-50 text-green-700"}`}>{t(model.status === "loading" ? "deployments.phase.loading" : "cluster.workerRuntimeRunning")}</span>
          </li>;
        })}</ul>}
    </section>
    <DeploymentTasksPanel tasks={tasks} error={error} title={t("cluster.workerTasks")} description={t("cluster.workerTasksHint")} />
  </div>;
}

function Metric({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return <div class="rounded-xl bg-gray-50 px-4 py-3"><p class="text-xs text-gray-500">{label}</p><p class="mt-1 text-lg font-semibold text-gray-900">{value}</p>{hint && <p class="mt-1 text-xs text-gray-400">{hint}</p>}</div>;
}
