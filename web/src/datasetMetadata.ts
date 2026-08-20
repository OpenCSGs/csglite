import type { MarketplaceDataset, MarketplaceTag } from "./api/client";

export type DatasetMarketplaceMetadata = {
  tasks: string[];
  languages: string[];
  formats: string[];
  modalities: string[];
  libraries: string[];
  topics: string[];
  sizeCategory?: string;
  type?: string;
};

export function datasetMarketplaceMetadata(dataset: MarketplaceDataset): DatasetMarketplaceMetadata {
  const tags = dataset.tags || [];
  const originalTags = [
    ...(dataset.provider?.huggingface?.original_tags || []),
    ...(dataset.provider?.modelscope?.original_tags || []),
  ];
  const tasks = [
    ...(dataset.provider?.huggingface?.task_categories || []),
    ...(dataset.provider?.modelscope?.tasks || []),
    ...tagValues(tags, "task"),
  ];
  const languages = [
    ...(dataset.provider?.huggingface?.languages || []),
    ...(dataset.provider?.modelscope?.languages || []),
    ...tagValues(tags, "language"),
  ];

  return {
    tasks: unique(tasks),
    languages: unique(languages),
    formats: prefixedValues(tags, originalTags, "format:"),
    modalities: prefixedValues(tags, originalTags, "modality:"),
    libraries: prefixedValues(tags, originalTags, "library:"),
    topics: datasetTopics(tags, originalTags),
    sizeCategory: firstPrefixedValue(tags, originalTags, "size_categories:"),
    type: datasetType(tags, originalTags),
  };
}

function datasetTopics(tags: MarketplaceTag[], originalTags: string[]): string[] {
  const categorized = tags
    .filter((tag) => tag.category === "tag")
    .map((tag) => tag.show_name?.trim() || tag.name.trim())
    .filter((value) => value && !value.includes(":"));
  const custom = originalTags
    .filter((tag) => tag.toLowerCase().startsWith("custom_tag:"))
    .map((tag) => tag.slice("custom_tag:".length).trim());
  return unique([...categorized, ...custom]);
}

function tagValues(tags: MarketplaceTag[], category: string): string[] {
  return tags
    .filter((tag) => tag.category === category)
    .map((tag) => tag.show_name?.trim() || tag.name.trim())
    .filter(Boolean);
}

function prefixedValues(tags: MarketplaceTag[], originalTags: string[], prefix: string): string[] {
  return unique([...tags.map((tag) => tag.name), ...originalTags]
    .filter((tag) => tag.toLowerCase().startsWith(prefix))
    .map((tag) => tag.slice(prefix.length).trim())
    .filter(Boolean));
}

function firstPrefixedValue(tags: MarketplaceTag[], originalTags: string[], prefix: string): string | undefined {
  return prefixedValues(tags, originalTags, prefix)[0];
}

function datasetType(tags: MarketplaceTag[], originalTags: string[]): string | undefined {
  const values = [...tags.map((tag) => tag.name), ...originalTags].map((tag) => tag.toLowerCase());
  if (values.some((tag) => tag === "benchmark" || tag.includes("benchmark"))) return "Benchmark";
  if (values.some((tag) => tag === "preview")) return "Preview";
  if (values.some((tag) => tag === "traces" || tag.includes("trace"))) return "Traces";
  if (values.some((tag) => tag === "viewer" || tag === "library:datasets")) return "Viewer";
  return undefined;
}

function unique(values: string[]): string[] {
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))];
}
