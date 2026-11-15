import { publicSessionHeaders } from "../utils/consoleAuth";

export type ServiceTestProtocol = "chat/completions" | "responses" | "messages" | "embeddings";

export async function testModelService(model: string, protocol: ServiceTestProtocol, input: string, signal: AbortSignal) {
  const endpoint = `/ai-gateway/v1/${protocol}`;
  const body = protocol === "embeddings"
    ? { model, input, encoding_format: "float" }
    : protocol === "responses"
      ? { model, input, max_output_tokens: 256, stream: false }
      : { model, messages: [{ role: "user", content: input }], max_tokens: 256, stream: false };
  const start = performance.now();
  const response = await fetch(endpoint, {
    method: "POST",
    headers: publicSessionHeaders(endpoint, "POST", {
      "Content-Type": "application/json",
      ...(protocol === "messages" ? { "anthropic-version": "2023-06-01" } : {}),
    }),
    body: JSON.stringify(body), signal,
  });
  const data = await response.json();
  if (!response.ok || data.error) {
    const message = typeof data.error === "string" ? data.error : data.error?.message;
    throw new Error(`HTTP ${response.status}: ${message || response.statusText}`);
  }
  return { data, status: response.status, durationMS: Math.round(performance.now() - start) };
}

export function serviceTestText(data: any, protocol: ServiceTestProtocol): string {
  if (protocol === "chat/completions") return data.choices?.[0]?.message?.content || JSON.stringify(data, null, 2);
  if (protocol === "responses") return data.output_text || data.output?.flatMap((item: any) => item.content || []).map((item: any) => item.text || "").join("\n") || JSON.stringify(data, null, 2);
  if (protocol === "messages") return data.content?.map((item: any) => item.text || "").join("\n") || JSON.stringify(data, null, 2);
  return JSON.stringify(data.data?.map((item: any) => ({ index: item.index, dimensions: item.embedding?.length, preview: item.embedding?.slice(0, 8) })) || data, null, 2);
}
