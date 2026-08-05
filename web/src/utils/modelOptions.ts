import { getTags, type ModelInfo } from "../api/client";
import { t } from "../i18n";

export function modelOptionKey(model: { model?: string; name?: string; source?: string }): string {
  return `${model.source || "local"}:${model.model || model.name}`;
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
