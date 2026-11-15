import type { ClusterDeployment, ClusterModelDistribution, ClusterModelSync, ClusterNodeModelStatus, ClusterNodeView, ClusterView, ProviderPool } from "./api/client";
import { candidateCategory } from "./utils/modelServices";
import { deploymentPhaseKey } from "./deploymentModels";

export interface ModelInstanceRow {
  key: string;
  node: ClusterNodeView;
  model: ClusterNodeModelStatus;
  sync?: ClusterModelSync;
  deployment?: ClusterDeployment;
  synthetic?: boolean;
}

// Use each node's exact model IDs. Repository aggregation merges registry copies
// and cannot identify the runtime instance whose performance is being shown.
export function modelInstanceRows(nodes: ClusterNodeView[], distribution: ClusterModelDistribution[], deployments: ClusterDeployment[] = []): ModelInstanceRow[] {
  const rows: ModelInstanceRow[] = [];
  for (const node of nodes) {
    for (const model of node.status?.models ?? []) {
      rows.push({ key: JSON.stringify([node.uuid, model.id]), node, model });
    }
    for (const model of distribution) {
      const sync = model.syncs?.find(s => s.uuid === node.uuid);
      if (!sync || rows.some(row => row.node.uuid === node.uuid && (row.model.id === model.id || (model.repository && row.model.repo === model.repository)))) continue;
      rows.push({ key: JSON.stringify([node.uuid, "sync", model.id]), node, sync,
        model: { id: model.id, repo: model.repository, size: model.size, format: model.format, loaded: false, loading: false, active: 0, native_tool_streaming: false } });
    }
  }
  for (const deployment of deployments) {
    const id = `deployment/${deployment.id}`;
    const existing = rows.find(row => row.node.uuid === deployment.node_uuid && row.model.id === id);
    if (deployment.status.phase === "deleted") {
      if (existing) rows.splice(rows.indexOf(existing), 1);
      continue;
    }
    if (existing) { existing.deployment = deployment; continue; }
    // Keep deployment records visible after their target leaves membership.
    // The UI supplies an unknown-node label; the exact target ID stays internal.
    const node = nodes.find(node => node.uuid === deployment.node_uuid) ?? {
      uuid: deployment.node_uuid, name: "", local: false, online: false, health: "down" as const,
    };
    rows.push({ key: JSON.stringify([node.uuid, id]), node, deployment, synthetic: true, model: {
      id, deployment_id: deployment.id, repo: deployment.repository, source: deployment.artifact_source,
      size: deployment.status.size ?? 0, category: deployment.kind, loaded: node.online && deployment.status.phase === "ready",
      loading: deployment.status.phase === "loading", active: 0, native_tool_streaming: true,
    } });
  }
  const rank = (row: ModelInstanceRow) => ({ running: 0, preparing: 1, failed: 2, inactive: 3, offline: 4, all: 5 })[modelInstanceFilter(row)];
  return rows.sort((a, b) => rank(a) - rank(b) || a.model.id.localeCompare(b.model.id) || a.node.name.localeCompare(b.node.name));
}

export function mergeClusterDeployments(owned: ClusterDeployment[], assignments: ClusterDeployment[]): ClusterDeployment[] {
  const result = new Map(owned.map(task => [task.id, task]));
  for (const task of assignments) if (!result.has(task.id)) result.set(task.id, task);
  return [...result.values()];
}

export function modelInstanceCategory(row: ModelInstanceRow): string {
  return candidateCategory(row.model);
}

export function isManagedModelInstance(row: ModelInstanceRow): boolean {
  return !!row.deployment || !!row.model.deployment_id || row.model.id.startsWith("deployment/");
}

export function canPublishModelInstance(row: ModelInstanceRow, view: ClusterView): boolean {
  if (view.mode === "worker" || !row.node.online || !row.node.status?.accept_work || row.node.status.state !== "active"
    || row.model.loading || !!row.sync || modelInstanceCategory(row) === "unsupported") return false;
  const task = row.deployment;
  if (isManagedModelInstance(row) && !task) return false;
  return !task || (task.controller_uuid === view.node.uuid && task.desired === "running" && task.status.phase === "ready" && row.model.loaded);
}

// Match runtime IDs and their source scope, never just a repository name.
export function modelPublication(row: ModelInstanceRow, view: ClusterView, pools: ProviderPool[] | null) {
  if (!pools) return { state: "unknown" as const, services: [] as ProviderPool[] };
  const matches = (source: string, model: string) => model === row.model.id && (source === `node:${row.node.uuid}`
    || source === "cluster" || (source === "local" && row.node.uuid === view.node.uuid));
  const services = pools.filter(pool => pool.members.some(member => matches(member.source, member.model)));
  const enabled = services.some(pool => pool.enabled && pool.members.some((member, index) => matches(member.source, member.model)
    && !member.disabled && (pool.policy !== "fixed" || index === 0)
    && (!pool.source_states?.[member.id] || pool.source_states[member.id] === "configured")));
  return { state: !services.length ? "unpublished" as const : enabled ? "published" as const : "disabled" as const, services };
}

export function modelActionAvailability(row: ModelInstanceRow, view: ClusterView, stale = false, rows: ModelInstanceRow[] = []) {
  const d = row.deployment;
  const owned = view.mode !== "worker" && (d ? d.controller_uuid === view.node.uuid
    : !isManagedModelInstance(row) && (row.node.local || row.node.status?.managed_by === view.node.uuid));
  const phase = d?.status.phase;
  const transitioning = !!d && ((d.desired === "deleted" && phase !== "failed")
    || (d.desired === "stopped" && phase !== "stopped" && phase !== "failed") || phase === "stopping" || phase === "deleting");
  const available = owned && row.node.online && !stale && !transitioning;
  const ordinaryReady = !!row.node.status?.model_management && !!row.model.repo && !!row.model.source
    && !row.model.loading && row.model.active === 0 && !row.sync;
  const referenced = rows.some(other => other.node.uuid === row.node.uuid && other.deployment
    && other.deployment.status.phase !== "deleted" && other.deployment.repository === row.model.repo
    && other.deployment.artifact_source === row.model.source);
  return {
    start: available && !!d && d.desired !== "deleted" && (phase === "stopped" || phase === "failed")
      && row.node.status?.state === "active" && !!row.node.status.accept_work && row.node.status.licensed !== false,
    unload: available && (d ? d.desired === "running" && phase !== "stopped" && phase !== "deleted" : ordinaryReady && row.model.loaded),
    delete: available && (d ? phase !== "deleted" : ordinaryReady && !referenced),
    sync: view.mode !== "worker" && (!view.mode || view.mode === "lan") && row.node.online && !stale
      && !isManagedModelInstance(row) && !row.model.loading && !row.sync && !!row.model.repo,
    reason: stale ? "cluster.modelStatusStale" : !owned ? (isManagedModelInstance(row) && !d ? "cluster.modelReceiptPending" : "cluster.modelReadOnly")
      : !row.node.online ? "cluster.healthOffline" : transitioning ? "cluster.modelApplying" : "cluster.modelActionUnavailable",
  };
}

export type ModelInstanceFilter = "all" | "running" | "preparing" | "inactive" | "failed" | "offline";
export function modelInstanceFilter(row: ModelInstanceRow): ModelInstanceFilter {
  const state = modelRowState(row);
  if (state === "cluster.healthOffline") return "offline";
  if (["deployments.phase.failed", "deployments.phase.blocked", "deployments.phase.unreachable"].includes(state)) return "failed";
  if (state === "cluster.instanceRunning") return "running";
  if (["cluster.instanceSleeping", "deployments.phase.stopped"].includes(state)) return "inactive";
  return "preparing";
}

export function filterModelInstances(rows: ModelInstanceRow[], query: string, node: string, state: ModelInstanceFilter): ModelInstanceRow[] {
  const search = query.trim().toLowerCase();
  return rows.filter(row => (!node || row.node.uuid === node) && (state === "all" || modelInstanceFilter(row) === state)
    && (!search || [row.model.id, row.model.repo, row.model.source, row.node.name, row.deployment?.name].filter(Boolean).join(" ").toLowerCase().includes(search)));
}
export function modelRowState(row: ModelInstanceRow): string {
  if (!row.node.online) return "cluster.healthOffline";
  if (row.deployment) {
    const d = row.deployment;
    if (d.desired === "deleted" && d.status.phase !== "failed") return "deployments.phase.deleting";
    if (d.desired === "stopped" && ["ready", "pending"].includes(d.status.phase)) return "deployments.phase.stopping";
    if (d.status.phase !== "ready") return deploymentPhaseKey(d.status.phase);
  }
  if (row.model.loading) return "cluster.instanceLoading";
  if (row.model.loaded) return "cluster.instanceRunning";
  if (row.sync) return "cluster.cellSyncing";
  return "cluster.instanceSleeping";
}
export function measuredRate(row: ModelInstanceRow): string {
  const rate = row.model.perf?.decode_tps;
  return (!row.deployment || (row.deployment.desired === "running" && row.deployment.status.phase === "ready")) && row.node.online && row.model.loaded && (row.model.perf?.samples ?? 0) > 0 && rate && Number.isFinite(rate) && rate > 0 ? rate.toFixed(1) : "—";
}

export function modelNodeState(row: ModelInstanceRow): string {
  if (!row.node.online) return "";
  if (row.node.status?.state === "drain") return "cluster.nodeDraining";
  if (row.node.status?.state === "maintenance" || row.node.status?.accept_work === false) return "cluster.nodeMaintenance";
  return "";
}
