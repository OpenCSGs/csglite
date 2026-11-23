import type { LoadModelOptions } from "./api/client";

// SGLang accepts only floating dtypes; GGUF quantization names belong to
// llama.cpp and must not be sent to a native SGLang process.
const SGLANG_DTYPE_NAMES = new Set([
  "", "auto", "f16", "fp16", "half", "float16", "bf16", "bfloat16",
  "f32", "fp32", "float", "float32",
]);

export const SGLANG_DTYPE_OPTIONS = ["f16", "bf16", "f32"];

export function isSGLangCompatibleDType(dtype?: string): boolean {
  return SGLANG_DTYPE_NAMES.has((dtype || "").trim().toLowerCase());
}

/** Remove llama.cpp-only load options when SGLang is the effective backend. */
export function sanitizeLoadOptionsForBackend(
  options: LoadModelOptions,
  effectiveBackend: "llama" | "sglang" | "",
): LoadModelOptions {
  if (effectiveBackend !== "sglang") return options;
  return {
    ...options,
    dtype: isSGLangCompatibleDType(options.dtype) ? options.dtype : undefined,
    n_gpu_layers: undefined,
    cache_type_k: undefined,
    cache_type_v: undefined,
    speculative: undefined,
  };
}

/** The dtype to persist after a backend change. */
export function persistedDTypeForBackend(dtype: string | undefined, effectiveBackend: "llama" | "sglang" | ""): string {
  if (effectiveBackend !== "sglang") return dtype || "";
  return isSGLangCompatibleDType(dtype) ? (dtype || "") : "";
}
