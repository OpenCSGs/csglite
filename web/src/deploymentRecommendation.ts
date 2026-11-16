import type { ClusterNodeView, ClusterView, MarketplaceModel, MarketplaceModelDetailResponse } from "./api/client";
import { deploymentTargets } from "./deploymentModels";

const GiB = 1024 ** 3;
export interface DeploymentMemory {
  kind: "vram" | "unified" | "ram";
  total: number;
  free: number | null;
  gpu: boolean;
}

export function deploymentNodeMemory(node?: ClusterNodeView, cpuOnly = false): DeploymentMemory {
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

/** Match the controller's Auto placement, including its deterministic tie break. */
export function automaticDeploymentTarget(view: ClusterView, cpuOnly = false): ClusterNodeView | undefined {
  return deploymentTargets(view).map(node => ({ node, memory: deploymentNodeMemory(node, cpuOnly) }))
    .filter(item => item.memory.free !== 0)
    .sort((a, b) => Number(b.memory.gpu) - Number(a.memory.gpu)
      || Number(b.memory.free !== null) - Number(a.memory.free !== null)
      || (b.memory.free || 0) - (a.memory.free || 0)
      || (a.node.status?.inflight || 0) - (b.node.status?.inflight || 0)
      || (a.node.uuid < b.node.uuid ? -1 : a.node.uuid > b.node.uuid ? 1 : 0))[0]?.node;
}

export interface DeploymentQuantOption { quant: string; size?: number; estimated: boolean }
const conversionBytesPerParameter: Record<string, number> = { f16: 2, q8_0: 1.1, q5_k_m: 0.75, q4_k_m: 0.65 };

export function deploymentQuantOptions(model?: MarketplaceModel | null, detail?: MarketplaceModelDetailResponse | null): DeploymentQuantOption[] {
  const params = model?.metadata?.model_params;
  const parameters = params && Number.isFinite(params) && params > 0 ? params * 1e9 : 0;
  const estimate = (quant: string) => parameters && conversionBytesPerParameter[quant] ? parameters * conversionBytesPerParameter[quant] : undefined;
  if (detail?.quantizations?.length) {
    // The API already orders variants by precision and sums split GGUF shards.
    return detail.quantizations.map(item => {
      const quant = item.name.toLowerCase();
      const knownSize = Number.isFinite(item.size) && item.size! > 0;
      return { quant, size: knownSize ? item.size : estimate(quant), estimated: !knownSize };
    });
  }
  if (model?.tags?.some(tag => tag.name.toLowerCase() === "gguf") || detail?.local_inference?.mode === "direct") return [];
  return Object.keys(conversionBytesPerParameter).map(quant => ({ quant, size: estimate(quant), estimated: true }));
}

export interface DeploymentQuantRecommendation {
  state: "recommended" | "no-fit" | "unknown";
  quant?: string;
  required?: number;
  available?: number;
  estimated?: boolean;
}

export function recommendDeploymentQuant(options: DeploymentQuantOption[], memory: DeploymentMemory, numCtx: number, numParallel: number): DeploymentQuantRecommendation {
  if (memory.free === null || !Number.isFinite(numCtx) || numCtx <= 0 || !Number.isFinite(numParallel) || numParallel <= 0) return { state: "unknown" };
  let inspected = false;
  for (const option of options) {
    if (!option.size || !Number.isFinite(option.size) || option.size <= 0) continue;
    inspected = true;
    // A planning estimate, not a runtime fit guarantee: reserve weight/compute
    // overhead plus a context allowance that grows with context and concurrency.
    // Exact KV/compute allocation depends on architecture and is determined by
    // llama.cpp at startup using the downloaded model's actual tensors.
    const context = Math.max(0.5 * GiB, option.size * 0.1) * Math.max(1, numCtx / 4096) * numParallel;
    const required = option.size * 1.2 + context;
    if (required <= memory.free) return { state: "recommended", quant: option.quant, required, available: memory.free, estimated: option.estimated };
  }
  return { state: inspected ? "no-fit" : "unknown", available: memory.free };
}
