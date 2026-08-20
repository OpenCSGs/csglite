import { describe, expect, it } from "vitest";
import type { MarketplaceDataset } from "./api/client";
import { datasetMarketplaceMetadata } from "./datasetMetadata";

describe("dataset marketplace metadata", () => {
  it("normalizes official Hugging Face card metadata", () => {
    const dataset = {
      tags: [
        { name: "text-generation", category: "task", show_name: "Text Generation" },
        { name: "en", category: "language", show_name: "English" },
        { name: "format:parquet", category: "tag", show_name: "format:parquet" },
      ],
      provider: {
        huggingface: {
          original_tags: [
            "size_categories:10K<n<100K",
            "modality:text",
            "library:datasets",
            "benchmark",
          ],
        },
      },
    } as MarketplaceDataset;

    expect(datasetMarketplaceMetadata(dataset)).toEqual({
      tasks: ["Text Generation"],
      languages: ["English"],
      formats: ["parquet"],
      modalities: ["text"],
      libraries: ["datasets"],
      topics: [],
      sizeCategory: "10K<n<100K",
      type: "Benchmark",
    });
  });

  it("preserves ModelScope custom topics for cards", () => {
    const dataset = {
      tags: [
        { name: "image-segmentation", category: "task", show_name: "image-segmentation" },
        { name: "COCO segmentation", category: "tag", show_name: "COCO segmentation" },
      ],
      provider: {
        modelscope: {
          original_tags: ["custom_tag:COCO segmentation", "custom_tag:Alibaba"],
        },
      },
    } as MarketplaceDataset;

    expect(datasetMarketplaceMetadata(dataset).topics).toEqual(["COCO segmentation", "Alibaba"]);
  });
});
