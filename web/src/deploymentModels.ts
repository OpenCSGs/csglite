import { runtimeKindForTask, runtimeKindFromSupport } from "./utils/modelRuntime";
import type { ModelRuntimeKind } from "./utils/modelRuntime";
export { contextWindowApplies as isLlamaDeployment } from "./utils/modelRuntime";
import type { ArtifactSource, ClusterDeployment, ClusterView, LocalInferenceSupport, MarketplaceModel } from "./api/client";

const deploymentPhases = new Set(["pending", "downloading", "loading", "ready", "idle", "stopping", "stopped", "failed", "blocked", "unreachable", "deleting", "deleted"]);
export function deploymentPhaseKey(phase: string): string {
  return `deployments.phase.${deploymentPhases.has(phase) ? phase : "pending"}`;
}

export function workerDeploymentAssignments(assignments: ClusterDeployment[], node: string, master?: string): ClusterDeployment[] {
  if (!master) return [];
  return assignments.filter(task => task.status.phase !== "deleted" && task.node_uuid === node && task.controller_uuid === master);
}

export type DeploymentModelKind = ModelRuntimeKind;

export function deploymentModelKind(model: MarketplaceModel, support?: LocalInferenceSupport): DeploymentModelKind {
  // Classify by machine-readable task metadata, never by repository names or
  // translated display labels. Unknown tasks require an explicit choice.
  const task = (model.provider?.huggingface?.pipeline_tag
    || model.provider?.modelscope?.tasks?.[0]
    || model.tags?.find(tag => tag.category === "task")?.name || "").trim().toLowerCase();
  const declared = runtimeKindForTask(task);
  return declared !== "unknown" ? declared : runtimeKindFromSupport(support);
}

export function deploymentRevision(model: MarketplaceModel | undefined, source: ArtifactSource): string {
  // A metadata response may contain a resolved commit in revision. New UI
  // deployments follow the default branch, not that historical snapshot.
  return model?.default_branch?.trim() || (source === "modelscope" ? "master" : "main");
}

export function recommendedDeploymentBackend(
  kind: DeploymentModelKind, format: "gguf" | "safetensors" | "unknown",
  engines: import("./api/client").ClusterEngineStatus[],
): "llama" | "sglang" | "unknown" | "none" {
  const llama = engines.find(engine => engine.name === "llama");
  const sglang = engines.find(engine => engine.name === "sglang");
  const llamaResult = (): "unknown" | "llama" | "none" => !llama?.known ? "unknown" : llama.available ? "llama" : "none";
  if (kind === "embedding" || (kind === "chat" && format === "gguf")) return llamaResult();
  if (kind !== "chat" || format !== "safetensors" || !sglang?.known) return "unknown";
  if (sglang.available) return "sglang";
  // An installed runtime that failed detection must not become an automatic
  // GGUF conversion. The user can explicitly choose llama.cpp if desired.
  if (sglang.installed) return "none";
  return llamaResult();
}

export function deploymentTargets(view: ClusterView, backend?: "llama" | "sglang" | "") {
  const eligible = view.members.filter(n => n.online && n.status?.deployment_capable && n.status.licensed
    && n.status.accept_work && n.status.state === "active"
    && (n.uuid === view.node.uuid || n.status.managed_by === view.node.uuid));
  if (!backend) return eligible;
  const withEngine = eligible.filter(n => n.status?.engines?.some(engine => engine.known && engine.name === backend && engine.available));
  if (withEngine.length > 0) return withEngine;
  // Legacy or still-probing nodes report no known engine. They are unknown
  // rather than proven unusable, so keep them as an explicit fallback.
  return eligible.filter(n => !n.status?.engines?.some(engine => engine.known));
}

export function deploymentName(model: string, existing: string[]): string {
  const base = (model.split("/").pop() || model).slice(0, 100);
  const names = new Set(existing.map(name => name.toLowerCase()));
  let candidate = base;
  for (let i = 2; names.has(candidate.toLowerCase()); i++) {
    const suffix = `-${i}`;
    candidate = base.slice(0, 100 - suffix.length) + suffix;
  }
  return candidate;
}

/** Only execution receipts on this machine and from its current controller. */
export function localDeploymentAssignments(assignments: ClusterDeployment[], view: ClusterView): ClusterDeployment[] {
  if (!view.cluster) return [];
  const controllers = view.mode === "worker"
    ? [view.cluster.gateway_uuid].filter(Boolean)
    : [view.node.uuid, view.settings.managed_by].filter(Boolean);
  return assignments.filter(task => task.node_uuid === view.node.uuid && task.status.phase !== "deleted" && controllers.includes(task.controller_uuid));
}

export function deploymentProgress(status: ClusterDeployment["status"]): { completed: number; total: number | null; percent: number | null } {
  const completed = Number.isFinite(status.completed_bytes) ? Math.max(0, status.completed_bytes!) : 0;
  const total = Number.isFinite(status.total_bytes) && status.total_bytes! > 0 ? status.total_bytes! : null;
  return { completed, total, percent: total === null ? null : Math.min(100, Math.floor(completed / total * 1000) / 10) };
}
