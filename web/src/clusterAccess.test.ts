import { afterEach, describe, expect, it, vi } from "vitest";
vi.mock("./api/client", () => ({ getCluster: vi.fn(), isClusterDisabledError: () => false, isFeatureNotLicensedError: () => false }));
import { getCluster } from "./api/client";
import type { ClusterView } from "./api/client";
import { clusterMode, refreshClusterMode, isControllerPage, isWorkerHiddenNav } from "./clusterAccess";
afterEach(() => { vi.clearAllMocks(); clusterMode.value = null; });
describe("worker navigation", () => {
  it("covers direct links and legacy chat without hiding local diagnostics", () => {
    expect(["/edge", "/edge/keys", "/ai-gateway", "/chat"].every(isControllerPage)).toBe(true);
    expect(["/cluster", "/observability", "/library", "/edge-case"].some(isControllerPage)).toBe(false);
    expect(["edge", "ai-gateway"].every(isWorkerHiddenNav)).toBe(true);
    expect(isWorkerHiddenNav("cluster")).toBe(false);
  });
  it("keeps worker restrictions during disconnect and restores entries after leaving", async () => {
    vi.mocked(getCluster).mockResolvedValue({mode:"worker",cluster:{uuid:"cluster"}} as ClusterView);
    await refreshClusterMode(); expect(clusterMode.value).toBe("worker");
    vi.mocked(getCluster).mockRejectedValue(new Error("offline"));
    await refreshClusterMode(); expect(clusterMode.value).toBe("worker");
    vi.mocked(getCluster).mockResolvedValue({mode:"worker",cluster:null} as ClusterView);
    await refreshClusterMode(); expect(clusterMode.value).toBe("lan");
  });
});
