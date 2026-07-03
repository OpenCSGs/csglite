import type { LocalInferenceSupport } from "../api/client";

export type LocalInferenceMode = "direct" | "convert" | "image" | "asr" | "embedding" | "none";

export function localInferenceModeFromSupport(support?: LocalInferenceSupport | null): LocalInferenceMode {
  if (!support?.supported) {
    return "none";
  }
  switch (support.mode) {
    case "direct":
      return "direct";
    case "convert":
      return "convert";
    case "image":
      return "image";
    case "asr":
      return "asr";
    case "embedding":
      return "embedding";
    default:
      return "none";
  }
}

export function supportsLocalInference(mode: LocalInferenceMode): boolean {
  return mode !== "none";
}

export function localInferenceLabelKey(prefix: "mp" | "lib"): string {
  return `${prefix}.localInference`;
}

export function localInferenceBadgeKey(mode: LocalInferenceMode, prefix: "mp" | "lib"): string {
  return supportsLocalInference(mode)
    ? `${prefix}.localInferenceSupported`
    : `${prefix}.localInferenceNotSupported`;
}

export function localInferenceValueKey(mode: LocalInferenceMode, prefix: "mp" | "lib"): string {
  switch (mode) {
    case "direct":
      return `${prefix}.localInferenceDirect`;
    case "convert":
      return `${prefix}.localInferenceConvert`;
    case "image":
      return `${prefix}.localInferenceImage`;
    case "asr":
      return `${prefix}.localInferenceASR`;
    case "embedding":
      return `${prefix}.localInferenceEmbedding`;
    default:
      return `${prefix}.localInferenceNone`;
  }
}
