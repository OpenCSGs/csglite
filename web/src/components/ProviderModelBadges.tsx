import type { ModelInfo } from "../api/client";
import { t } from "../i18n";

export function defaultProviderModelDisplayName(model: ModelInfo): string {
  return model.display_name || model.label || model.model;
}

export function providerModelLabel(model: ModelInfo): string {
  return defaultProviderModelDisplayName(model);
}

function pipelineTagLabel(tag?: string): string {
  switch (tag) {
    case "text-to-image":
      return t("pipeline.imageGeneration");
    case "image-to-image":
      return t("pipeline.imageToImage");
    case "text-to-video":
      return t("pipeline.textToVideo");
    case "image-to-video":
      return t("pipeline.imageToVideo");
    case "video-text-to-text":
      return t("pipeline.videoUnderstanding");
    case "text-to-speech":
      return t("pipeline.textToSpeech");
    case "automatic-speech-recognition":
      return t("pipeline.speechRecognition");
    default:
      return t("pipeline.languageModel");
  }
}

function modalityLabel(modality: string): string {
  switch (modality) {
    case "text":
      return t("pipeline.modalityText");
    case "image":
      return t("pipeline.modalityImage");
    case "video":
      return t("pipeline.modalityVideo");
    case "audio":
    case "speech":
      return t("pipeline.modalityAudio");
    case "file":
      return t("pipeline.modalityFile");
    case "transcription":
      return t("pipeline.modalityText");
    default:
      return modality;
  }
}

function uniqueModalities(values: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of values) {
    const key = value.trim().toLowerCase();
    if (!key || seen.has(key)) continue;
    seen.add(key);
    out.push(key);
  }
  return out;
}

export function modelOutputModalities(model: ModelInfo): string[] {
  const outputs = uniqueModalities(model.output_modalities || []);
  if (outputs.length > 0) return outputs;
  switch (model.pipeline_tag) {
    case "text-to-image":
    case "image-to-image":
      return ["image"];
    case "text-to-video":
    case "image-to-video":
      return ["video"];
    case "text-to-speech":
      return ["audio"];
    case "automatic-speech-recognition":
      return ["text"];
    default:
      return ["text"];
  }
}

export function modelInputModalities(model: ModelInfo): string[] {
  return uniqueModalities(model.input_modalities || []).filter((item) => item !== "text");
}

export function ProviderModelModalityBadges({
  model,
  showPipelineTag = false,
  showInputs = false,
  showOutputs = true,
  compact = false,
}: {
  model: ModelInfo;
  showPipelineTag?: boolean;
  showInputs?: boolean;
  showOutputs?: boolean;
  compact?: boolean;
}) {
  const pill = compact ? "rounded px-1.5 py-0.5 text-[10px]" : "rounded-full px-2 py-0.5 text-[11px]";
  const inputs = showInputs ? modelInputModalities(model) : [];
  const outputs = showOutputs ? modelOutputModalities(model) : [];
  if (!showPipelineTag && inputs.length === 0 && outputs.length === 0) return null;
  return (
    <span class={`flex min-w-0 flex-wrap items-center gap-1 ${compact ? "" : "gap-1.5"}`}>
      {showPipelineTag && (
        <span class={`${pill} bg-gray-100 text-gray-600`}>
          {pipelineTagLabel(model.pipeline_tag)}
        </span>
      )}
      {inputs.map((item) => (
        <span key={`in-${item}`} class={`${pill} bg-blue-50 text-blue-700`}>
          {t("pipeline.inputCapability", modalityLabel(item))}
        </span>
      ))}
      {outputs.map((item) => (
        <span key={`out-${item}`} class={`${pill} bg-violet-50 text-violet-700`}>
          {t("pipeline.outputCapability", modalityLabel(item))}
        </span>
      ))}
    </span>
  );
}
