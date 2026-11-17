import type { LocalInferenceSupport, ModelInfo } from "../api/client";

export type ModelRuntimeKind = "chat" | "embedding" | "image" | "asr" | "tts" | "unknown";
type RuntimeModel = Pick<ModelInfo, "category" | "pipeline_tag" | "input_modalities" | "output_modalities" | "local_inference">;

/** Shared task vocabulary for downloaded models and registry model details. */
export function runtimeKindForTask(task: string): ModelRuntimeKind {
  switch (task.trim().toLowerCase()) {
    case "chat": case "language_model": case "text-generation": case "text2text-generation": case "image-text-to-text": return "chat";
    case "embedding": case "sentence-similarity": case "feature-extraction": case "text-embedding": return "embedding";
    case "image": case "text-to-image": case "image-to-image": return "image";
    case "asr": case "automatic-speech-recognition": return "asr";
    case "tts": case "text-to-speech": case "text-to-audio": return "tts";
    default: return "unknown";
  }
}

export function runtimeKindFromSupport(support?: LocalInferenceSupport): ModelRuntimeKind {
  if (!support?.supported) return "unknown";
  switch (support.mode) {
    case "direct": case "convert": return "chat";
    case "embedding": case "image": case "asr": case "tts": return support.mode;
    default: return "unknown";
  }
}

export function modelRuntimeKind(model: RuntimeModel): ModelRuntimeKind {
  const task = runtimeKindForTask(model.pipeline_tag || "");
  if (task !== "unknown") return task;
  const category = runtimeKindForTask(model.category || "");
  if (category !== "unknown") return category;
  const runtime = runtimeKindFromSupport(model.local_inference);
  if (runtime !== "unknown") return runtime;
  // Older local manifests may only describe modalities. Untagged local LLMs
  // retain their existing llama.cpp settings rather than losing saved options.
  if (model.output_modalities?.includes("speech")) return "tts";
  if (model.input_modalities?.includes("audio") || model.output_modalities?.includes("transcription")) return "asr";
  return "chat";
}

export function contextWindowApplies(kind: ModelRuntimeKind): kind is "chat" | "embedding" {
  return kind === "chat" || kind === "embedding";
}

export function modelContextWindowApplies(model: RuntimeModel): boolean {
  return contextWindowApplies(modelRuntimeKind(model));
}
export const isEmbeddingModel = (model: RuntimeModel) => modelRuntimeKind(model) === "embedding";
export const isImageModel = (model: RuntimeModel) => modelRuntimeKind(model) === "image";
export const isASRModel = (model: RuntimeModel) => modelRuntimeKind(model) === "asr";
export const isTTSModel = (model: RuntimeModel) => modelRuntimeKind(model) === "tts";
