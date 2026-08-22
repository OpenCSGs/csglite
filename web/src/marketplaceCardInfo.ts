import type { ArtifactSource, MarketplaceDataset, MarketplaceModel, MarketplaceTag } from "./api/client";
import { datasetMarketplaceMetadata } from "./datasetMetadata";
import { modelOwnerIdentity } from "./modelSources";

export type ProviderModelCardInfo = {
  source: ArtifactSource;
  title: string;
  path: string;
  author: string;
  task: string;
  library: string;
  license: string;
  params: string;
  architecture: string;
  modelType: string;
  gated: boolean;
  private: boolean;
};

export type ProviderDatasetCardInfo = {
  source: ArtifactSource;
  title: string;
  path: string;
  author: string;
  type: string;
  sizeCategory: string;
  tasks: string[];
  formats: string[];
  license: string;
  gated: boolean;
  private: boolean;
};

export function isProviderMarketplaceSource(source?: string): source is "huggingface" | "modelscope" {
  return source === "huggingface" || source === "modelscope";
}

export function providerModelCardInfo(model: MarketplaceModel): ProviderModelCardInfo {
  const huggingFace = model.provider?.huggingface;
  const modelScope = model.provider?.modelscope;
  return {
    source: model.artifact_source || "opencsg",
    title: firstText(modelScope?.display_name, model.nickname, model.name, model.path),
    path: model.path,
    author: firstText(huggingFace?.author, modelOwnerIdentity(model.path).owner),
    task: firstText(huggingFace?.pipeline_tag, modelScope?.tasks?.[0], tagValue(model.tags, "task")),
    library: firstText(huggingFace?.library_name, modelScope?.libraries?.[0], tagValue(model.tags, "runtime_framework")),
    license: firstText(model.license, tagValue(model.tags, "license")),
    params: formatCardParams(model.metadata?.model_params),
    architecture: firstText(model.metadata?.architecture, model.metadata?.class_name),
    modelType: firstText(modelScope?.model_type, model.metadata?.model_type),
    gated: Boolean(huggingFace?.gated || modelScope?.gated),
    private: Boolean(model.private),
  };
}

export function providerDatasetCardInfo(dataset: MarketplaceDataset): ProviderDatasetCardInfo {
  const huggingFace = dataset.provider?.huggingface;
  const modelScope = dataset.provider?.modelscope;
  const metadata = datasetMarketplaceMetadata(dataset);
  return {
    source: dataset.artifact_source || "opencsg",
    title: firstText(huggingFace?.pretty_name, modelScope?.display_name, dataset.nickname, dataset.name, dataset.path),
    path: dataset.path,
    author: firstText(huggingFace?.author, modelOwnerIdentity(dataset.path).owner),
    type: metadata.type || "",
    sizeCategory: metadata.sizeCategory || tagValue(dataset.tags, "size"),
    tasks: metadata.tasks,
    formats: metadata.formats,
    license: firstText(dataset.license, tagValue(dataset.tags, "license")),
    gated: Boolean(huggingFace?.gated || modelScope?.gated),
    private: Boolean(dataset.private),
  };
}

export function formatCardParams(value?: number): string {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) {
    return "";
  }
  if (value >= 1000) {
    return `${trimFloat(value / 1000)}T`;
  }
  return `${trimFloat(value)}B`;
}

function tagValue(tags: MarketplaceTag[] | undefined, category: string): string {
  return tags?.find((tag) => tag.category === category)?.show_name?.trim()
    || tags?.find((tag) => tag.category === category)?.name.trim()
    || "";
}

function firstText(...values: Array<string | undefined>): string {
  return values.map((value) => value?.trim() || "").find(Boolean) || "";
}

function trimFloat(value: number): string {
  return value.toFixed(value >= 10 || Number.isInteger(value) ? 0 : 1).replace(/\.0$/, "");
}
