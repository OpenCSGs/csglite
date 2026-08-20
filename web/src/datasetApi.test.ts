import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

let clearPartialDatasetPull: typeof import("./api/client").clearPartialDatasetPull;
let createDatasetPullJob: typeof import("./api/client").createDatasetPullJob;
let getMarketplaceDatasetDetail: typeof import("./api/client").getMarketplaceDatasetDetail;
let getMarketplaceDatasetExtras: typeof import("./api/client").getMarketplaceDatasetExtras;
let getMarketplaceDatasets: typeof import("./api/client").getMarketplaceDatasets;

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

beforeAll(async () => {
  vi.stubGlobal("localStorage", {
    getItem: () => null,
    setItem: () => {},
    removeItem: () => {},
  });
  ({
    clearPartialDatasetPull,
    createDatasetPullJob,
    getMarketplaceDatasetDetail,
    getMarketplaceDatasetExtras,
    getMarketplaceDatasets,
  } = await import("./api/client"));
});

afterEach(() => vi.unstubAllGlobals());

describe("source-aware dataset API", () => {
  it("sends provider filters on marketplace list and detail requests", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ data: [], total: 0 }))
      .mockResolvedValueOnce(jsonResponse({ details: {}, local_dataset: { downloaded: false } }))
      .mockResolvedValueOnce(jsonResponse({ available: true, repo_size: 12, file_count: 2 }));
    vi.stubGlobal("fetch", fetchMock);

    await getMarketplaceDatasets({
      artifactSource: "huggingface",
      task: "text-classification",
      language: "en",
      license: "apache-2.0",
    });
    await getMarketplaceDatasetDetail("acme/data", {
      artifactSource: "modelscope",
      revision: "v1",
    });
    await getMarketplaceDatasetExtras("acme/data", {
      artifactSource: "modelscope",
      revision: "v1",
    });

    expect(fetchMock.mock.calls[0][0]).toContain("artifact_source=huggingface");
    expect(fetchMock.mock.calls[0][0]).toContain("task=text-classification");
    expect(fetchMock.mock.calls[0][0]).toContain("language=en");
    expect(fetchMock.mock.calls[0][0]).toContain("license=apache-2.0");
    expect(fetchMock.mock.calls[1][0]).toBe(
      "/api/marketplace/datasets/acme/data?artifact_source=modelscope&revision=v1",
    );
    expect(fetchMock.mock.calls[2][0]).toBe(
      "/api/marketplace/datasets/acme/data/extras?artifact_source=modelscope&revision=v1",
    );
  });

  it("carries source and revision through jobs and partial cleanup", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ id: "job" }))
      .mockResolvedValueOnce(jsonResponse({ status: "cleared", path: "/tmp/data" }));
    vi.stubGlobal("fetch", fetchMock);

    await createDatasetPullJob("acme/data", { artifactSource: "huggingface", revision: "main" });
    await clearPartialDatasetPull("acme/data", { artifactSource: "huggingface", revision: "main" });

    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toMatchObject({
      dataset: "acme/data",
      artifact_source: "huggingface",
      revision: "main",
    });
    expect(fetchMock.mock.calls[1][0]).toBe("/api/datasets/pull/partial");
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toMatchObject({
      dataset: "acme/data",
      artifact_source: "huggingface",
      revision: "main",
    });
  });
});
