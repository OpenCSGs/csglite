import { getTags, type ModelInfo } from "../api/client";
import { t } from "../i18n";

export function modelOptionKey(model: { model?: string; name?: string; source?: string }): string {
  return `${model.source || "local"}:${model.model || model.name}`;
}

// modelOptionID is what the API calls the model. It is deliberately not
// modelOptionKey: that one prefixes the source to keep the dropdown's options
// unique, and sending it as a model id asks the server for a model that does
// not exist ("local:Fun-ASR-Nano-2512").
export function modelOptionID(model: { model?: string; name?: string }): string {
  return (model.model || model.name || "").trim();
}

// realtimeVoiceOptions builds the dropdown entries for the realtime voice
// dialog. Only local models are offered, because the call is served by the
// local Python runtimes and a cloud or provider model would fail on connect.
// Each entry carries both identifiers: the key keeps the option unique in the
// dropdown, the id is what the server is asked for.
export function realtimeVoiceOptions(
  models: ModelInfo[],
  matches: (model: ModelInfo) => boolean,
  label: (model: ModelInfo) => string,
): { key: string; id: string; label: string }[] {
  return models
    .filter((model) => matches(model) && (!model.source || model.source === "local") && modelOptionID(model) !== "")
    .map((model) => ({ key: modelOptionKey(model), id: modelOptionID(model), label: label(model) }));
}

export function normalizeModelOptions(models: ModelInfo[]): ModelInfo[] {
  const seen = new Set<string>();
  const out: ModelInfo[] = [];
  for (const model of models) {
    const modelID = (model.model || model.name || "").trim();
    const key = modelOptionKey(model);
    if (!modelID || seen.has(key)) continue;
    seen.add(key);
    out.push(model);
  }
  return out;
}

export async function loadModelOptions(options?: { refresh?: boolean }): Promise<ModelInfo[]> {
  return normalizeModelOptions(await getTags(options));
}

export function modelOptionProviderName(model: ModelInfo): string {
  const source = (model.source || "local").trim();
  if (!source || source === "local") return t("chat.local");
  return model.provider?.trim() || (source === "cloud" ? t("chat.cloud") : t("chat.provider"));
}

export function modelOptionDisplayName(model: ModelInfo): string {
  const name = (model.display_name || model.label || model.model || model.name || "").trim();
  const provider = modelOptionProviderName(model);
  const suffix = ` [${provider}]`;
  if (name.toLocaleLowerCase().endsWith(suffix.toLocaleLowerCase())) {
    return name.slice(0, -suffix.length).trimEnd();
  }
  return name;
}

export function formatModelOptionLabel(model: ModelInfo): string {
  return `${modelOptionDisplayName(model)} [${modelOptionProviderName(model)}]`;
}
