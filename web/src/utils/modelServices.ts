import type { ModelInfo, ProviderPool } from "../api/client";

export function serviceConfigured(pool: ProviderPool): boolean {
  const members = pool.policy === "fixed" ? pool.members.slice(0, 1) : pool.members;
  return pool.enabled && members.some(member => !member.disabled && (!pool.source_states || pool.source_states[member.id] === "configured"));
}
export function candidateCategory(model: ModelInfo): string {
  if (model.category === "embedding" || ["feature-extraction", "sentence-similarity", "text-embedding", "embedding"].includes(model.pipeline_tag || "")) return "embedding";
  if (model.decision_only || model.local_inference?.architecture === "ModernBertDecisionModel") return "unsupported";
  if (model.category && !["chat", "language_model"].includes(model.category)) return "unsupported";
  if (model.pipeline_tag && !["text-generation", "image-text-to-text", "text2text-generation"].includes(model.pipeline_tag)) return "unsupported";
  return "chat";
}

