import type { ProviderPool } from "./client";

export type ServiceTestProtocol = "chat/completions" | "responses" | "messages" | "embeddings" | "decisions" | "images/generations" | "images/edits" | "audio/transcriptions" | "audio/speech";
const allProtocols: ServiceTestProtocol[] = ["chat/completions","responses","messages","embeddings","decisions","images/generations","images/edits","audio/transcriptions","audio/speech"];
const taskProtocols: Record<string, ServiceTestProtocol[]> = {
  chat: ["chat/completions", "responses", "messages"],
  embedding: ["embeddings"],
  image: ["images/generations", "images/edits"],
  asr: ["audio/transcriptions"],
  tts: ["audio/speech"],
  decision: ["decisions"],
};
export function serviceTestProtocols(service: Pick<ProviderPool, "category" | "inference_protocols" | "typed_decisions" | "decision_only">): ServiceTestProtocol[] {
  // JEV can chat as well as decide. Prefer its Decisions API, as API examples
  // do, while keeping the server's protocol list authoritative for support.
  const preferred: ServiceTestProtocol[] = service.decision_only ? ["decisions"] : [
    ...(service.typed_decisions ? ["decisions" as const] : []),
    ...(taskProtocols[service.category || "chat"] || taskProtocols.chat),
  ];
  const available = service.inference_protocols ?? preferred;
  return [...new Set([...preferred, ...allProtocols])].filter(protocol => available.includes(protocol));
}

