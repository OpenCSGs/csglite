import type { ModelInfo } from "./api/client";

export function displayLocalModelID(model: Pick<ModelInfo, "name" | "repository" | "artifact_source">): string {
  const repository = model.repository?.trim();
  if (repository) return repository;

  const name = model.name.trim();
  const source = model.artifact_source;
  if (source && source !== "opencsg" && name.startsWith(`${source}/`)) {
    return name.slice(source.length + 1);
  }
  return name;
}
