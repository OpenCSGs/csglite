import { describe, expect, it } from "vitest";
import { isSGLangCompatibleDType, persistedDTypeForBackend, sanitizeLoadOptionsForBackend } from "./modelRunOptions";

describe("model run options", () => {
  it("clears a GGUF quantization and llama-only options for SGLang", () => {
    const sanitized = sanitizeLoadOptionsForBackend({
      num_ctx: 8192,
      num_parallel: 2,
      n_gpu_layers: 40,
      cache_type_k: "q8_0",
      cache_type_v: "q8_0",
      dtype: "q4_k_m",
      speculative: { types: ["ngram-mod"] },
    }, "sglang");
    expect(sanitized.num_ctx).toBe(8192);
    expect(sanitized.num_parallel).toBe(2);
    expect(sanitized.dtype).toBeUndefined();
    expect(sanitized.n_gpu_layers).toBeUndefined();
    expect(sanitized.cache_type_k).toBeUndefined();
    expect(sanitized.cache_type_v).toBeUndefined();
    expect(sanitized.speculative).toBeUndefined();
    expect(persistedDTypeForBackend("q4_k_m", "sglang")).toBe("");
  });

  it("keeps an explicit floating dtype for SGLang", () => {
    const sanitized = sanitizeLoadOptionsForBackend({ dtype: "bf16", n_gpu_layers: 10 }, "sglang");
    expect(sanitized.dtype).toBe("bf16");
    expect(sanitized.n_gpu_layers).toBeUndefined();
    expect(persistedDTypeForBackend("bf16", "sglang")).toBe("bf16");
    expect(isSGLangCompatibleDType("f32")).toBe(true);
    expect(isSGLangCompatibleDType("q5_k_m")).toBe(false);
  });

  it("leaves llama.cpp options untouched", () => {
    const options = { dtype: "q4_k_m", n_gpu_layers: 40, cache_type_k: "q8_0" };
    expect(sanitizeLoadOptionsForBackend(options, "llama")).toEqual(options);
  });
});
