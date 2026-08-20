import type { ArtifactSource, DatasetInfo } from "./api/client";

export function datasetArtifactSource(dataset: Pick<DatasetInfo, "artifact_source">): ArtifactSource {
  const source = dataset.artifact_source;
  return source === "huggingface" || source === "modelscope" ? source : "opencsg";
}

export function displayLocalDatasetID(
  dataset: Pick<DatasetInfo, "name" | "repository" | "artifact_source">,
): string {
  const repository = dataset.repository?.trim();
  if (repository) return repository;
  const source = datasetArtifactSource(dataset);
  const name = dataset.name.trim();
  return source !== "opencsg" && name.startsWith(`${source}/`)
    ? name.slice(source.length + 1)
    : name;
}

export function localDatasetID(
  dataset: Pick<DatasetInfo, "name" | "repository" | "artifact_source">,
): string {
  const source = datasetArtifactSource(dataset);
  const repository = displayLocalDatasetID(dataset);
  return source === "opencsg" ? repository : `${source}/${repository}`;
}
