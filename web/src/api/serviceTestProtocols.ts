import type { ProviderPool } from "./client";

export type ServiceTestProtocol = "chat/completions" | "responses" | "messages" | "embeddings" | "decisions" | "images/generations" | "images/edits" | "audio/transcriptions" | "audio/speech";
const allProtocols: ServiceTestProtocol[] = ["chat/completions","responses","messages","embeddings","decisions","images/generations","images/edits","audio/transcriptions","audio/speech"];
export function serviceTestProtocols(service: Pick<ProviderPool,"category"|"inference_protocols">): ServiceTestProtocol[] {
  if(service.inference_protocols) return allProtocols.filter(protocol=>service.inference_protocols!.includes(protocol));
  switch(service.category) {
    case "embedding":return ["embeddings"];
    case "image":return ["images/generations","images/edits"];
    case "asr":return ["audio/transcriptions"];
    case "tts":return ["audio/speech"];
    case "decision":return ["decisions"];
    default:return ["chat/completions","responses","messages"];
  }
}

