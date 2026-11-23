import type { ClusterDeploymentCreateRequest, ClusterDeploymentSpec } from "./api/client";
import type { DeploymentModelKind } from "./deploymentModels";

export interface DeploymentPayloadInput {
  draft: ClusterDeploymentSpec;
  modelKind: DeploymentModelKind;
  /** True when the displayed/auto plan resolves to SGLang. */
  effectiveSGLang: boolean;
  recommendedQuant?: string;
  recommendedRequired?: number;
  targetUUID?: string;
}

/**
 * Builds the exact payload sent to POST /api/cluster/deployments. The preview
 * and the request must agree: an auto-SGLang plan must not carry a llama.cpp
 * quantization or a GGUF memory estimate, and must pin the displayed target.
 */
export function buildClusterDeploymentRequest(input: DeploymentPayloadInput): ClusterDeploymentCreateRequest {
  const { draft, modelKind, effectiveSGLang, recommendedQuant, recommendedRequired, targetUUID } = input;
  return {
    ...draft,
    keep_alive: draft.keep_alive?.trim() || (effectiveSGLang ? "-1" : undefined),
    sglang_args: effectiveSGLang ? draft.sglang_args?.trim() || undefined : undefined,
    repository: draft.repository.trim(),
    kind: modelKind === "embedding" ? "embedding" : "chat",
    node_uuid: targetUUID || draft.node_uuid,
    quant: effectiveSGLang ? "" : draft.quant || recommendedQuant || "",
    minimum_memory_bytes: !effectiveSGLang && !draft.quant && recommendedRequired
      ? Math.ceil(recommendedRequired)
      : undefined,
    backend: effectiveSGLang ? "sglang" : draft.backend || "",
  };
}
