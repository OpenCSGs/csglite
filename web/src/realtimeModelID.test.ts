import { describe, expect, it, vi } from "vitest";

// The module graph reaches i18n, which reads the saved locale on import.
vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

import { realtimeModelID, type RealtimeVoiceModel } from "./components/RealtimeVoiceDialog";
import { modelOptionID, modelOptionKey, realtimeVoiceOptions } from "./utils/modelOptions";

// The dropdown key and the model id are different strings. Sending the key as
// a model id asked the server for "local:Fun-ASR-Nano-2512", which it reported
// as not downloaded even though the model was there.
describe("realtime model identifiers", () => {
  it("keeps the option key separate from the id the server knows", () => {
    const model = { model: "Fun-ASR-Nano-2512", name: "Fun-ASR-Nano-2512", source: "local" };
    expect(modelOptionKey(model)).toBe("local:Fun-ASR-Nano-2512");
    expect(modelOptionID(model)).toBe("Fun-ASR-Nano-2512");
  });

  it("resolves a selected option to the model id", () => {
    const models: RealtimeVoiceModel[] = [
      { key: "local:Fun-ASR-Nano-2512", id: "Fun-ASR-Nano-2512", label: "Fun-ASR-Nano-2512" },
      { key: "local:modelscope/iic/SenseVoiceSmall", id: "modelscope/iic/SenseVoiceSmall", label: "SenseVoiceSmall" },
    ];
    expect(realtimeModelID(models, "local:Fun-ASR-Nano-2512")).toBe("Fun-ASR-Nano-2512");
    expect(realtimeModelID(models, "local:modelscope/iic/SenseVoiceSmall")).toBe("modelscope/iic/SenseVoiceSmall");
  });

  // Nothing selected, and an option that has gone away, both mean "no model"
  // rather than a model id that happens to look like a key.
  it("reports no model for an empty or unknown selection", () => {
    const models: RealtimeVoiceModel[] = [{ key: "local:a", id: "a", label: "a" }];
    expect(realtimeModelID(models, "")).toBe("");
    expect(realtimeModelID(models, "local:gone")).toBe("");
    expect(realtimeModelID([], "local:a")).toBe("");
  });

  // A model id can itself contain a colon (a GGUF quant suffix), so the id
  // cannot be recovered by splitting the key.
  it("survives a model id that contains a colon", () => {
    const model = { model: "Qwen/Qwen-Image-2512:s-qwen-image-6byf", source: "local" };
    const models: RealtimeVoiceModel[] = [
      { key: modelOptionKey(model), id: modelOptionID(model), label: "x" },
    ];
    expect(realtimeModelID(models, modelOptionKey(model))).toBe("Qwen/Qwen-Image-2512:s-qwen-image-6byf");
  });
});

describe("realtime voice options", () => {
  const label = (m: { model?: string; name?: string }) => m.model || m.name || "";
  const isASR = (m: { pipeline_tag?: string }) => m.pipeline_tag === "automatic-speech-recognition";

  it("offers local models with both identifiers", () => {
    const models = [
      { model: "Fun-ASR-Nano-2512", pipeline_tag: "automatic-speech-recognition", source: "local" },
      { model: "modelscope/iic/SenseVoiceSmall", pipeline_tag: "automatic-speech-recognition" },
    ] as any[];
    expect(realtimeVoiceOptions(models, isASR, label)).toEqual([
      { key: "local:Fun-ASR-Nano-2512", id: "Fun-ASR-Nano-2512", label: "Fun-ASR-Nano-2512" },
      {
        key: "local:modelscope/iic/SenseVoiceSmall",
        id: "modelscope/iic/SenseVoiceSmall",
        label: "modelscope/iic/SenseVoiceSmall",
      },
    ]);
  });

  // The call runs on the local Python runtimes, so a cloud or provider model
  // could only fail once the user pressed start.
  it("leaves out models this runtime cannot serve", () => {
    const models = [
      { model: "cloud/asr", pipeline_tag: "automatic-speech-recognition", source: "cloud" },
      { model: "pool/asr", pipeline_tag: "automatic-speech-recognition", source: "provider" },
      { model: "local/asr", pipeline_tag: "automatic-speech-recognition", source: "local" },
      { model: "local/chat", pipeline_tag: "text-generation", source: "local" },
      { model: "", pipeline_tag: "automatic-speech-recognition", source: "local" },
    ] as any[];
    expect(realtimeVoiceOptions(models, isASR, label).map((o) => o.id)).toEqual(["local/asr"]);
  });
});
