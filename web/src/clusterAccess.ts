import { signal } from "@preact/signals";
import { getCluster, isClusterDisabledError, isFeatureNotLicensedError } from "./api/client";
import type { ClusterView } from "./api/client";

// Derived runtime role; never persist a second copy in settings.
export const clusterMode = signal<ClusterView["mode"] | null>(null);
let pending: Promise<void> | undefined;
export function refreshClusterMode(): Promise<void> {
  if (pending) return pending;
  pending = getCluster().then(view => { clusterMode.value = navigationMode(view); }).catch(error => {
    if (isClusterDisabledError(error) || isFeatureNotLicensedError(error)) clusterMode.value = "lan";
    // Keep the previous role on transient failures, including a disconnected Master.
  }).finally(() => { pending = undefined; });
  return pending;
}
export function isControllerPage(path: string): boolean {
  return ["/ai-gateway", "/edge", "/chat"].some(base => path === base || path.startsWith(base + "/"));
}
export function isWorkerHiddenNav(id: string): boolean {
  return id === "ai-gateway" || id === "edge";
}

export function navigationMode(view: Pick<ClusterView, "mode" | "cluster">): ClusterView["mode"] {
  return view.mode === "worker" && !view.cluster ? "lan" : view.mode ?? "lan";
}
