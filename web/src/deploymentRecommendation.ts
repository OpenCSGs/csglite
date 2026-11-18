import type { ClusterNodeView, ClusterView, MarketplaceModel, MarketplaceModelDetailResponse } from "./api/client";
import { deploymentTargets } from "./deploymentModels";
import { nodeMemory as deploymentNodeMemory } from "./clusterMemory";
import type { NodeMemory as DeploymentMemory } from "./clusterMemory";
export { deploymentNodeMemory };
export type { DeploymentMemory };

const GiB = 1024 ** 3;
/** Match the controller's Auto placement, including its deterministic tie break. */
export function automaticDeploymentTarget(view: ClusterView, cpuOnly = false, backend?: "llama" | "sglang" | ""): ClusterNodeView | undefined {
  return deploymentTargets(view, backend).map(node => ({ node, memory: deploymentNodeMemory(node, cpuOnly) }))
    .filter(item => item.memory.free !== 0)
    .sort((a, b) => Number(engineReady(b.node, backend)) - Number(engineReady(a.node, backend))
      || Number(b.memory.gpu) - Number(a.memory.gpu)
      || Number(b.memory.free !== null) - Number(a.memory.free !== null)
      || (b.memory.free || 0) - (a.memory.free || 0)
      || (a.node.status?.inflight || 0) - (b.node.status?.inflight || 0)
      || (a.node.uuid < b.node.uuid ? -1 : a.node.uuid > b.node.uuid ? 1 : 0))[0]?.node;
}

function engineReady(node: ClusterNodeView, backend?: "llama" | "sglang" | ""): boolean {
  return Boolean(node.status?.engines?.some(engine => engine.known && (!backend || engine.name === backend) && engine.available));
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
