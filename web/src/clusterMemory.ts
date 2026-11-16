import type { ClusterNodeView } from "./api/client";

/** Model placement and cluster resource views must describe the same memory pool.
 * Shared GPU allocations are part of host RAM, not a second free memory budget. */
export interface NodeMemory {
  kind: "vram" | "unified" | "ram";
  total: number;
  free: number | null;
  gpu: boolean;
}

export function nodeMemory(node?: ClusterNodeView, cpuOnly = false): NodeMemory {
  const status = node?.status;
  const devices = status?.gpus || [];
  const unified = status?.ram?.unified || devices.some(device => device.shared_memory);
  const gpu = !cpuOnly && devices.length > 0;
  if (cpuOnly || unified || !devices.length) {
    const total = status?.ram?.total || 0;
    return { kind: unified && !cpuOnly ? "unified" : "ram", total,
      free: total > 0 ? Math.max(0, total - (status?.ram?.used || 0)) : null, gpu };
  }
  const reported = devices.filter(device => device.usage_known && device.vram_total > 0);
  return { kind: "vram", gpu, total: devices.reduce((sum, device) => sum + device.vram_total, 0),
    free: reported.length ? reported.reduce((sum, device) => sum + Math.max(0, device.vram_total - device.vram_used), 0) : null };
}

/** Aggregate each online accelerator's memory pool once; CPU-only RAM is separate. */
export function clusterAcceleratorMemory(nodes: ClusterNodeView[]): { total: number; free: number | null } {
  let total = 0, free = 0, known = true;
  for (const node of nodes) {
    if (!node.online || !node.status?.gpus.length) continue;
    const memory = nodeMemory(node);
    total += memory.total;
    if (memory.free === null) known = false;
    else free += memory.free;
  }
  return { total, free: known ? free : null };
}

const amount = (bytes: number) => (Math.max(0, bytes) / 1024 ** 3).toLocaleString(undefined, { minimumFractionDigits: 1, maximumFractionDigits: 1 });
export const formatMemory = (bytes: number) => `${amount(bytes)} GiB`;
export const formatMemoryPair = (value: number | null, total: number) => `${value === null ? "—" : amount(value)} / ${amount(total)} GiB`;
