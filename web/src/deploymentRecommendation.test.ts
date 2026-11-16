import { describe, expect, it } from "vitest";
import type { ClusterNodeView, ClusterView, MarketplaceModel, MarketplaceModelDetailResponse } from "./api/client";
import { automaticDeploymentTarget, deploymentNodeMemory, deploymentQuantOptions, recommendDeploymentQuant } from "./deploymentRecommendation";

const GiB = 1024 ** 3;
function node(id: string, free: number, gpu = true): ClusterNodeView {
  return { uuid: id, online: true, status: { deployment_capable: true, licensed: true, accept_work: true,
    state: "active", managed_by: "master", inflight: 0, ram: { total: 64 * GiB, used: 8 * GiB },
    gpus: gpu ? [{ vram_total: 32 * GiB, vram_used: (32 - free) * GiB, usage_known: true }] : [],
  } } as ClusterNodeView;
}
const model = { metadata: { model_params: 9 }, tags: [] } as unknown as MarketplaceModel;

describe("deployment memory and Auto placement", () => {
  it("prefers more available GPU memory and preserves eligibility rules", () => {
    const small = node("small", 12), large = node("large", 24), foreign = node("foreign", 32);
    foreign.status!.managed_by = "another-master";
    const offline = { ...node("offline", 32), online: false };
    const paused = node("paused", 32); paused.status!.state = "maintenance";
    const view = { node: { uuid: "master" }, members: [small, foreign, offline, paused, node("cpu", 0, false), large] } as ClusterView;
    expect(automaticDeploymentTarget(view)?.uuid).toBe("large");
    expect(automaticDeploymentTarget(view, true)?.uuid).toBe("cpu");
    expect(view.members[0]).toBe(small);
  });
  it("does not count shared memory per GPU or infer free VRAM from unknown usage", () => {
    const shared = node("mac", 24);
    shared.status!.ram.unified = true;
    shared.status!.gpus.push({ ...shared.status!.gpus[0], index: 1 });
    expect(deploymentNodeMemory(shared)).toMatchObject({ kind: "unified", total: 64 * GiB, free: 56 * GiB });
    shared.status!.ram.unified = false;
    shared.status!.gpus.forEach(gpu => gpu.usage_known = false);
    expect(deploymentNodeMemory(shared).free).toBeNull();
    expect(deploymentNodeMemory(undefined).free).toBeNull();
  });
  it("excludes exhausted nodes and deterministically breaks memory ties by load then UUID", () => {
    const a = node("a", 24), b = node("b", 24);
    a.status!.inflight = 1;
    const view = { node: { uuid: "master" }, members: [a, b, node("empty", 0)] } as ClusterView;
    expect(automaticDeploymentTarget(view)?.uuid).toBe("b");
    a.status!.inflight = 0;
    expect(automaticDeploymentTarget(view)?.uuid).toBe("a");
    expect(automaticDeploymentTarget({ ...view, members: [node("empty", 0)] })).toBeUndefined();
  });
});

describe("deployment quantization recommendation", () => {
  const memory = (free: number) => ({ kind: "vram" as const, total: 32 * GiB, free: free * GiB, gpu: true });
  const options = deploymentQuantOptions(model);
  it("recommends progressively smaller conversions as available memory falls", () => {
    expect(recommendDeploymentQuant(options, memory(32), 4096, 1).quant).toBe("f16");
    expect(recommendDeploymentQuant(options, memory(16), 4096, 1).quant).toBe("q8_0");
    expect(recommendDeploymentQuant(options, memory(9), 4096, 1).quant).toBe("q5_k_m");
    expect(recommendDeploymentQuant(options, memory(8), 4096, 1).quant).toBe("q4_k_m");
    expect(recommendDeploymentQuant(options, memory(4), 4096, 1).state).toBe("no-fit");
  });
  it("reserves more memory for longer contexts and more concurrent requests", () => {
    expect(recommendDeploymentQuant(options, memory(16), 4096, 1).quant).toBe("q8_0");
    expect(recommendDeploymentQuant(options, memory(16), 16384, 2).quant).toBe("q5_k_m");
  });
  it("uses actual GGUF sizes and never recommends absent variants", () => {
    const detail = { quantizations: [
      { name: "Q8_0", size: 12 * GiB }, { name: "Q3_K_M", size: 5 * GiB },
    ] } as MarketplaceModelDetailResponse;
    const available = deploymentQuantOptions(model, detail);
    expect(available.map(option => option.quant)).toEqual(["q8_0", "q3_k_m"]);
    expect(recommendDeploymentQuant(available, memory(10), 4096, 1)).toMatchObject({ quant: "q3_k_m", estimated: false });
    expect(deploymentQuantOptions({ ...model, tags: [{ name: "gguf", category: "framework" }] } as MarketplaceModel)).toEqual([]);
  });
  it("does not pretend missing model or memory telemetry is a fit", () => {
    expect(recommendDeploymentQuant(deploymentQuantOptions({} as MarketplaceModel), memory(32), 4096, 1).state).toBe("unknown");
    expect(recommendDeploymentQuant(options, { ...memory(32), free: null }, 4096, 1).state).toBe("unknown");
    expect(recommendDeploymentQuant(options, memory(32), Number.NaN, 1).state).toBe("unknown");
  });
});
