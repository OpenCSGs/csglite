import { describe, expect, it } from "vitest";
import { buildClusterDeploymentRequest } from "./deploymentPayload";
import type { ClusterDeploymentSpec } from "./api/client";

const draft: ClusterDeploymentSpec = {
  name: "qwen", node_uuid: "auto", repository: " Qwen/Qwen2.5-1.5B-Instruct ",
  artifact_source: "huggingface", revision: "main", quant: "q4_k_m",
  backend: "", kind: "chat", num_ctx: 16384, num_parallel: 1, n_gpu_layers: -1,
};

describe("deployment payload", () => {
  it("clears a stale GGUF quant and pins the displayed target for auto SGLang", () => {
    const payload = buildClusterDeploymentRequest({
      draft: { ...draft, sglang_args: "--chunked-prefill-size 4096\n--kv-cache-dtype bf16" }, modelKind: "chat", effectiveSGLang: true,
      recommendedQuant: "q4_k_m", recommendedRequired: 9e9, targetUUID: "gpu-worker",
    });
    expect(payload.backend).toBe("sglang");
    expect(payload.quant).toBe("");
    expect(payload.sglang_args).toBe("--chunked-prefill-size 4096\n--kv-cache-dtype bf16");
    expect(payload.minimum_memory_bytes).toBeUndefined();
    expect(payload.node_uuid).toBe("gpu-worker");
    expect(payload.repository).toBe("Qwen/Qwen2.5-1.5B-Instruct");
  });

  it("keeps the llama.cpp memory estimate when quantization is automatic", () => {
    const payload = buildClusterDeploymentRequest({
      draft: { ...draft, quant: "" }, modelKind: "chat", effectiveSGLang: false,
      recommendedQuant: "q5_k_m", recommendedRequired: 8.2e9, targetUUID: "llama-node",
    });
    expect(payload.backend).toBe("");
    expect(payload.quant).toBe("q5_k_m");
    expect(payload.minimum_memory_bytes).toBe(8.2e9);
  });

  it("never sends SGLang for embedding", () => {
    const payload = buildClusterDeploymentRequest({
      draft: { ...draft, kind: "embedding", quant: "" }, modelKind: "embedding",
      effectiveSGLang: false, recommendedQuant: "q8_0", targetUUID: "worker",
    });
    expect(payload.kind).toBe("embedding");
    expect(payload.backend).toBe("");
    expect(payload.quant).toBe("q8_0");
  });

  it("leaves a legacy unknown target auto-resolved", () => {
    const payload = buildClusterDeploymentRequest({
      draft: { ...draft, node_uuid: "auto", quant: "" }, modelKind: "chat", effectiveSGLang: false,
    });
    expect(payload.node_uuid).toBe("auto");
    expect(payload.backend).toBe("");
    expect(payload.quant).toBe("");
  });
});
