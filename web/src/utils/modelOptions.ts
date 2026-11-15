import { serviceConfigured } from "./modelServices";
import { getProviderPools, getTags, type ProviderPool, type ModelInfo } from "../api/client";
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

// realtimeChatOptions lists the conversation models the realtime session may
// answer with. Unlike the speech halves this is not limited to local models:
// the reply is written through the same chat engines as /v1/chat/completions,
// so a cloud model serves it as well as a local one does.
export function realtimeChatOptions(
  models: ModelInfo[],
  matches: (model: ModelInfo) => boolean,
  label: (model: ModelInfo) => string,
): { key: string; id: string; label: string }[] {
  return models
    .filter((model) => matches(model) && (!model.source || model.source === "local" || model.source === "cloud") && modelOptionID(model) !== "")
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

export async function loadModelOptions(options?: { refresh?: boolean; preserveKey?: string }): Promise<ModelInfo[]> {
  const [models, pools] = await Promise.all([getTags(options), getProviderPools()]);
  return preferModelServices(normalizeModelOptions(models), pools, options?.preserveKey);
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

// Native <select> options only render text, so featured models are marked with
// an emoji instead of an SVG icon.
export function modelHotIcon(model: { hot?: boolean }): string {
  return model.hot ? "🔥 " : "";
}

// sortHotModelsFirst keeps hot models at the top without changing the order of
// the remaining models.
export function sortHotModelsFirst(models: ModelInfo[]): ModelInfo[] {
  const services: ModelInfo[] = [];
  const hot: ModelInfo[] = [];
  const rest: ModelInfo[] = [];
  for (const model of models) {
    if (model.source?.startsWith("pool:")) services.push(model);
    else if (model.hot) hot.push(model);
    else rest.push(model);
  }
  return services.concat(hot, rest);
}

// Hide only exact, enabled source/model bindings; unrelated names stay distinct.
// The API catalog itself retains every source for existing callers.
export function preferModelServices(models: ModelInfo[], pools: ProviderPool[], preserveKey?: string): ModelInfo[] {
 const published = pools.filter(serviceConfigured);
 const poolIDs = new Set(published.map(pool => `pool:${pool.id}`));
 const bound = new Set(published.flatMap(pool => pool.members.filter(member => !member.disabled && (!pool.source_states || pool.source_states[member.id] === "configured")).map(modelOptionKey)));
 return [
  ...models.filter(model => poolIDs.has(model.source || "")),
  ...models.filter(model => {
   if (model.source?.startsWith("pool:")) return false;
   if (modelOptionKey(model) === preserveKey) return true;
   const remoteAlias = model.origin && (model.source === "cloud" || model.source?.startsWith("provider:"));
   return !bound.has(modelOptionKey(model)) && !(remoteAlias && bound.has(modelOptionKey({ source: model.source, model: model.origin })));
  }),
 ];
}
