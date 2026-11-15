import type { ModelInfo, ProviderPool } from "../api/client";

export function serviceConfigured(pool: ProviderPool): boolean {
  const members = pool.policy === "fixed" ? pool.members.slice(0, 1) : pool.members;
  return pool.enabled && members.some(member => !member.disabled && (!pool.source_states || pool.source_states[member.id] === "configured"));
}
export function candidateCategory(model: Pick<ModelInfo, "category" | "pipeline_tag" | "decision_only" | "local_inference">): string {
  if (model.category === "embedding" || ["feature-extraction", "sentence-similarity", "text-embedding", "embedding"].includes(model.pipeline_tag || "")) return "embedding";
  if (model.decision_only || model.local_inference?.architecture === "ModernBertDecisionModel") return "decision";
  if (["text-to-image","image-to-image"].includes(model.pipeline_tag||"")) return "image";
  if (model.pipeline_tag==="automatic-speech-recognition") return "asr";
  if (["text-to-speech","text-to-audio"].includes(model.pipeline_tag||"")) return "tts";
  if (["decision","typed-decisions"].includes(model.pipeline_tag||"")) return "decision";
  if (model.category && !["chat", "language_model", "image", "asr", "tts", "decision"].includes(model.category)) return "unsupported";
  if (model.category && ["image","asr","tts","decision"].includes(model.category)) return model.category;
  if (model.pipeline_tag && !["text-generation", "image-text-to-text", "text2text-generation"].includes(model.pipeline_tag)) return "unsupported";
  return "chat";
}

export function servicePipelineTag(service: Pick<ProviderPool, "category" | "inference_protocols">): string {
  if (service.category === "image") return service.inference_protocols?.includes("images/edits") && !service.inference_protocols.includes("images/generations") ? "image-to-image" : "text-to-image";
  return ({ embedding: "feature-extraction", asr: "automatic-speech-recognition", tts: "text-to-speech", decision: "typed-decisions" } as Record<string, string>)[service.category || ""] || "text-generation";
}
