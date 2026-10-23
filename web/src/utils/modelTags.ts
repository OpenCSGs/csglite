// Model card tags that make a local model answer /v1/decisions.
const decisionTags = new Set(["jev", "typed-decisions"]);

export function isDecisionTag(tag: string): boolean {
  return decisionTags.has(tag.trim().toLowerCase());
}

/** Decision tags first, the rest in card order. */
export function orderModelTags(tags: string[]): string[] {
  return [...tags.filter(isDecisionTag), ...tags.filter((tag) => !isDecisionTag(tag))];
}
