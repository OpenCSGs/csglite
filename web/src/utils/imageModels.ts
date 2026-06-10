export function isTextToImageModel(model?: { pipeline_tag?: string } | null): boolean {
  return model?.pipeline_tag === "text-to-image";
}

export function isImageToImageModel(model?: { pipeline_tag?: string } | null): boolean {
  return model?.pipeline_tag === "image-to-image";
}

export function isImageGenerationModel(model?: { pipeline_tag?: string } | null): boolean {
  const tag = model?.pipeline_tag;
  return tag === "text-to-image" || tag === "image-to-image";
}

export function stripDataURL(value: string): string {
  const trimmed = value.trim();
  if (trimmed.startsWith("data:")) {
    const comma = trimmed.indexOf(",");
    if (comma >= 0) {
      return trimmed.slice(comma + 1);
    }
  }
  return trimmed;
}
