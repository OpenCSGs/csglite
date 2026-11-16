import { describe, expect, it } from "vitest";
import type { ClusterNodeView } from "./api/client";
import { clusterAcceleratorMemory, nodeMemory } from "./clusterMemory";
import { deploymentNodeMemory } from "./deploymentRecommendation";

const GiB = 1024 ** 3;
const mac = () => ({ online: true, status: {
  ram: { total: 16 * GiB, used: 7.5 * GiB, unified: true },
  gpus: [{ vram_total: 16 * GiB, vram_used: 0.5 * GiB, usage_known: true, shared_memory: true }],
} } as ClusterNodeView);

describe("cluster memory accounting", () => {
  it("counts system usage in unified memory, instead of only GPU allocations", () => {
    const node = mac();
    expect(nodeMemory(node)).toMatchObject({ kind: "unified", free: 8.5 * GiB, total: 16 * GiB });
    expect(deploymentNodeMemory(node)).toEqual(nodeMemory(node));
    expect(clusterAcceleratorMemory([node])).toEqual({ free: 8.5 * GiB, total: 16 * GiB });
    expect(node.status!.gpus[0].vram_used).toBe(0.5 * GiB);
  });

  it("counts a shared pool once, even with several GPU devices", () => {
    const node = mac();
    node.status!.ram.unified = false;
    node.status!.gpus.push({ ...node.status!.gpus[0], index: 1 });
    expect(clusterAcceleratorMemory([node])).toEqual({ free: 8.5 * GiB, total: 16 * GiB });
  });

  it("sums dedicated GPUs separately and uses RAM for CPU-only deployments", () => {
    const node = mac(); node.status!.ram.unified = false;
    node.status!.gpus = [
      { vram_total: 24 * GiB, vram_used: 4 * GiB, usage_known: true },
      { vram_total: 24 * GiB, vram_used: 8 * GiB, usage_known: true },
    ] as NonNullable<ClusterNodeView["status"]>["gpus"];
    expect(nodeMemory(node)).toMatchObject({ kind: "vram", free: 36 * GiB, total: 48 * GiB });
    expect(nodeMemory(node, true)).toMatchObject({ kind: "ram", free: 8.5 * GiB, total: 16 * GiB });
  });

  it("excludes offline nodes and CPU-only RAM from accelerator totals", () => {
    const cpu = mac(); cpu.status!.gpus = [];
    expect(clusterAcceleratorMemory([mac(), { ...mac(), online: false }, cpu])).toEqual({ free: 8.5 * GiB, total: 16 * GiB });
  });

  it("does not display unknown dedicated GPU usage as completely free", () => {
    const unknown = mac(); unknown.status!.ram.unified = false;
    unknown.status!.gpus = [{ vram_total: 24 * GiB, vram_used: 0, usage_known: false }] as NonNullable<ClusterNodeView["status"]>["gpus"];
    expect(nodeMemory(unknown)).toMatchObject({ free: null, total: 24 * GiB });
    expect(clusterAcceleratorMemory([mac(), unknown])).toEqual({ free: null, total: 40 * GiB });
  });

  it("clamps exhausted memory and preserves missing telemetry", () => {
    const node = mac(); node.status!.ram.used = 20 * GiB;
    expect(nodeMemory(node).free).toBe(0);
    expect(nodeMemory(undefined).free).toBeNull();
  });
});
