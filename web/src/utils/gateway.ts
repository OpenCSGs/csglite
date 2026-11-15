export const gatewayBasePath = "/ai-gateway";

// Published services share one endpoint. Direct model tools retain their source
// explicitly, including two providers exposing the same upstream model name.
export function gatewayInferencePath(endpoint: string, source?: string): string {
 if (!["chat/completions", "embeddings", "responses", "messages", "messages/count_tokens"].includes(endpoint)) return `${gatewayBasePath}/v1/${endpoint}`;
 const target = (source || "local").trim();
 if (target.startsWith("pool:")) return `${gatewayBasePath}/v1/${endpoint}`;
 const provider = target === "cloud" ? "csghub" : target.startsWith("provider:") ? target.slice(9) : target;
 return `${gatewayBasePath}/providers/${encodeURIComponent(provider)}/v1/${endpoint}`;
}
