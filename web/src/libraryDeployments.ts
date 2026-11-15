import type { ArtifactSource, ClusterDeployment, ModelInfo, RunningModel } from "./api/client";
import type { DownloadTask } from "./downloads";
import { deploymentProgress } from "./deploymentModels";

export interface LibraryModelRow {
  model: ModelInfo;
  task?: DownloadTask;
  downloadOnly: boolean;
  deployment?: ClusterDeployment;
}

// Adapt node receipts into the existing library rows without creating pull jobs.
// Artifact identity includes the registry; runtime instances share that artifact.
export function deploymentLibraryRows(deployments: ClusterDeployment[], running: RunningModel[], query: string, format: string, cached: ModelInfo[] = []): LibraryModelRow[] {
  const search = query.trim().toLowerCase();
  const rank = (d: ClusterDeployment) => d.desired === "running" && d.status.phase === "ready" ? 0 : d.desired === "running" && ["pending", "loading", "downloading"].includes(d.status.phase) ? 1 : 2;
  return deployments.filter(d => d.status.phase !== "deleted").sort((a,b) => rank(a) - rank(b)).flatMap(d => {
    const id = `deployment/${d.id}`;
    const runtime = running.find(m => (m.model || m.name) === id);
    const local = cached.find(model => (model.artifact_source || "opencsg") === d.artifact_source && model.repository === d.repository);
    const modelFormat = local?.format || runtime?.format || "";
    if (search && ![d.repository, d.name, id].some(value => value.toLowerCase().includes(search))) return [];
    if (format !== "all" && modelFormat !== format) return [];
    const progress = deploymentProgress(d.status);
    const phase = d.status.phase;
    const complete = phase === "ready" || phase === "loading" || ((d.status.size ?? 0) > 0 && phase === "stopped");
    const status = phase === "failed" ? "error" : complete ? "success" : phase === "downloading" ? "downloading" : phase === "pending" ? "queued" : "paused";
    const source = d.artifact_source as ArtifactSource;
    return [{
      model: { name: d.artifact_source === "opencsg" ? d.repository : `${d.artifact_source}/${d.repository}`, model: d.artifact_source === "opencsg" ? d.repository : `${d.artifact_source}/${d.repository}`, repository: d.repository, artifact_source: source, origin: "marketplace", format: modelFormat,
        size: runtime?.size || d.status.size || progress.total || progress.completed,
        modified_at: d.created_at || d.status.updated_at, category: d.kind },
      task: { key: id, kind: "model", name: d.repository, status, percent: complete ? 100 : Math.min(99.9, progress.percent ?? 0),
        statusText: phase, currentFile: d.status.detail, completedBytes: progress.completed, totalBytes: progress.total ?? 0,
        error: d.status.error, artifactSource: source, revision: d.revision, createdAt: d.created_at, updatedAt: d.status.updated_at, files: {} },
      downloadOnly: !complete, deployment: d,
    }];
  });
}

/** One library row per stored artifact, including all managed instances. */
export function mergeDeploymentLibraryRows(local: LibraryModelRow[], managed: LibraryModelRow[]): LibraryModelRow[] {
  const key = (model: ModelInfo) => `${model.artifact_source || "opencsg"}:${model.repository || model.id || model.name}`;
  const result = [...local];
  for (const row of managed) {
    const index = result.findIndex(current => key(current.model) === key(row.model));
    if (index < 0) result.push(row);
    else if (!result[index].deployment) {
      const original = result[index];
      result[index] = { ...row, model: original.downloadOnly ? row.model : original.model, downloadOnly: original.downloadOnly && row.downloadOnly };
    }
  }
  return result;
}
