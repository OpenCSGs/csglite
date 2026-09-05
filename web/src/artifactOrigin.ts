export function modelOriginKey(origin?: string, artifactSource?: string): string {
  if (origin === "upload") return "lib.originUpload";
  if (origin === "marketplace") {
    if (artifactSource === "huggingface") return "lib.originHuggingFace";
    if (artifactSource === "modelscope") return "lib.originModelScope";
    return "lib.originOpenCSG";
  }
  return "lib.notAvailable";
}

export function downloadRowOriginKey(artifactSource?: string): string {
  return modelOriginKey("marketplace", artifactSource || "opencsg");
}
