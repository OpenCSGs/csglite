import type { ArtifactSource } from "./api/client";

export function normalizeMarketplaceModelSource(value?: string): ArtifactSource {
  if (value === "huggingface" || value === "modelscope") return value;
  return "opencsg";
}

export function modelOwnerIdentity(modelPath: string): { owner: string; initial: string; palette: number } {
  const owner = modelPath.split("/").map((part) => part.trim()).find(Boolean) || "?";
  const initial = Array.from(owner)[0]?.toLocaleUpperCase() || "?";
  let hash = 0;
  for (const character of owner) {
    hash = (hash * 31 + character.codePointAt(0)!) >>> 0;
  }
  return { owner, initial, palette: hash % 6 };
}
