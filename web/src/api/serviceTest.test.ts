import { afterEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: { getItem: () => null, setItem: () => undefined } });
});
import { serviceTestText, testModelService, serviceTestProtocols } from "./serviceTest";

function environment() {
  vi.stubGlobal("window", { location: { href: "https://gateway.example/ai-gateway", origin: "https://gateway.example" } });
  vi.stubGlobal("document", { cookie: "__Host-csglite_csrf=csrf-test" });
}
afterEach(() => vi.unstubAllGlobals());

describe("per-model API testing", () => {
  it("uses the server's actual protocols and keeps legacy category fallback", () => {
    expect(serviceTestProtocols({ category: "chat", inference_protocols: ["decisions"] })).toEqual(["decisions"]);
    expect(serviceTestProtocols({ category: "image", inference_protocols: ["images/edits"] })).toEqual(["images/edits"]);
    expect(serviceTestProtocols({ category: "tts", inference_protocols: [] })).toEqual([]);
    expect(serviceTestProtocols({ category: "embedding" })).toEqual(["embeddings"]);
    expect(serviceTestProtocols({ category: "asr" })).toEqual(["audio/transcriptions"]);
    expect(serviceTestProtocols({ category: "tts" })).toEqual(["audio/speech"]);
  });
  it("defaults a JEV model with both decision and chat capabilities to the Decisions API", async () => {
    environment();
    const fetcher = vi.fn().mockResolvedValue(new Response('{"answers":[]}'));
    vi.stubGlobal("fetch", fetcher);
    const supported = serviceTestProtocols({
      category: "chat",
      typed_decisions: true,
      decision_only: false,
      inference_protocols: ["chat/completions", "responses", "messages", "decisions"],
    });
    expect(supported).toEqual(["decisions", "chat/completions", "responses", "messages"]);
    await testModelService("autotrust/JEV-9B", supported[0], "I was charged twice", new AbortController().signal, {
      question: "Is a refund requested?",
    });
    expect(fetcher.mock.calls[0][0]).toBe("/ai-gateway/v1/decisions");
    const body = JSON.parse(fetcher.mock.calls[0][1].body);
    expect(body.model).toBe("autotrust/JEV-9B");
    expect(body.questions[0].type).toBe("predicate");
    expect(body).not.toHaveProperty("messages");
  });
  it.each([
    ["decision", "decisions"],
    ["embedding", "embeddings"],
    ["image", "images/generations"],
    ["asr", "audio/transcriptions"],
    ["tts", "audio/speech"],
  ])("defaults the %s task to its own API even when chat is also available", (category, expected) => {
    expect(serviceTestProtocols({ category, inference_protocols: ["chat/completions", expected] })[0]).toBe(expected);
  });
  it("uses legacy decision flags without advertising unsupported server protocols", () => {
    expect(serviceTestProtocols({ category: "chat", typed_decisions: true })).toEqual(["decisions", "chat/completions", "responses", "messages"]);
    expect(serviceTestProtocols({ category: "chat", decision_only: true })).toEqual(["decisions"]);
    expect(serviceTestProtocols({ category: "chat", typed_decisions: true, inference_protocols: [] })).toEqual([]);
    expect(serviceTestProtocols({ category: "chat", typed_decisions: true, inference_protocols: ["messages", "unknown"] })).toEqual(["messages"]);
    expect(serviceTestProtocols({ category: "chat", typed_decisions: false })).toEqual(["chat/completions", "responses", "messages"]);
  });
  it.each(["chat/completions", "responses", "messages", "embeddings"] as const)("sends the correct %s request with the fixed model and console authentication", async protocol => {
    environment();
    const fetcher = vi.fn().mockResolvedValue(new Response('{"ok":true}', { status: 200 }));
    vi.stubGlobal("fetch", fetcher);
    const abort = new AbortController();
    const result = await testModelService("qwen-service", protocol, "Hello", abort.signal);
    const [url, options] = fetcher.mock.calls[0];
    expect(url).toBe(`/ai-gateway/v1/${protocol}`);
    expect(options.signal).toBe(abort.signal);
    expect(options.headers.get("X-CSGLite-CSRF")).toBe("csrf-test");
    const body = JSON.parse(options.body);
    expect(body.model).toBe("qwen-service");
    expect(body).not.toHaveProperty("source");
    if (protocol === "embeddings") {
      expect(body).toEqual({ model: "qwen-service", input: "Hello", encoding_format: "float" });
    } else if (protocol === "responses") {
      expect(body).toEqual({ model: "qwen-service", input: "Hello", max_output_tokens: 256, stream: false });
    } else {
      expect(body).toEqual({ model: "qwen-service", messages: [{ role: "user", content: "Hello" }], max_tokens: 256, stream: false });
    }
    if (protocol === "messages") expect(options.headers.get("anthropic-version")).toBe("2023-06-01");
    expect(result.status).toBe(200);
  });
  it("surfaces API errors and request cancellation", async () => {
    environment();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response('{"error":{"message":"model unavailable"}}', { status: 503 })));
    await expect(testModelService("m", "messages", "Hello", new AbortController().signal)).rejects.toThrow("HTTP 503: model unavailable");
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new DOMException("Aborted", "AbortError")));
    await expect(testModelService("m", "responses", "Hello", new AbortController().signal)).rejects.toMatchObject({ name: "AbortError" });
  });
  it.each(["predicate", "choice", "score"] as const)("sends a typed %s decision instead of chat", async questionType => {
    environment();
    const fetcher = vi.fn().mockResolvedValue(new Response('{"answers":[]}'));
    vi.stubGlobal("fetch", fetcher);
    await testModelService("laya", "decisions", "I was charged twice", new AbortController().signal, { questionType, question: "Is a refund requested?", choices: "billing\ntechnical" });
    const [url, options] = fetcher.mock.calls[0];
    const body = JSON.parse(options.body);
    expect(url).toBe("/ai-gateway/v1/decisions");
    expect(body.model).toBe("laya");
    expect(body.input).toBe("I was charged twice");
    expect(body).not.toHaveProperty("messages");
    expect(body).not.toHaveProperty("source");
    expect(body.questions[0]).toMatchObject({ type: questionType, instructions: "Is a refund requested?" });
    if (questionType === "choice") expect(body.questions[0].choices).toEqual([{ value: "billing" }, { value: "technical" }]);
    if (questionType === "score") expect(body.questions[0].levels).toEqual([{ label: "billing" }, { label: "technical" }]);
  });
  it("generates images using the Images API", async () => {
    environment();
    const fetcher = vi.fn().mockResolvedValue(new Response('{"data":[{"b64_json":"image"}]}'));
    vi.stubGlobal("fetch", fetcher);
    const result = await testModelService("image", "images/generations", "a cat", new AbortController().signal);
    expect(fetcher.mock.calls[0][0]).toBe("/ai-gateway/v1/images/generations");
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ model: "image", prompt: "a cat", n: 1, response_format: "b64_json" });
    expect(result.data.data[0].b64_json).toBe("image");
  });
  it.each(["images/edits", "audio/transcriptions"] as const)("uploads binary files for %s without overriding the browser boundary", async protocol => {
    environment();
    const fetcher = vi.fn().mockResolvedValue(new Response('{"text":"transcribed"}'));
    vi.stubGlobal("fetch", fetcher);
    const file = new File([new Uint8Array([0, 255, 13, 10])], "sample.bin", { type: "application/octet-stream" });
    await testModelService("file-model", protocol, "add color", new AbortController().signal, { file });
    const [url, options] = fetcher.mock.calls[0];
    expect(url).toBe(`/ai-gateway/v1/${protocol}`);
    expect(options.body).toBeInstanceOf(FormData);
    expect(options.headers.has("Content-Type")).toBe(false);
    expect(options.headers.get("X-CSGLite-CSRF")).toBe("csrf-test");
    expect(options.body.get("model")).toBe("file-model");
    const upload = options.body.get(protocol === "images/edits" ? "image" : "file") as File;
    expect(new Uint8Array(await upload.arrayBuffer())).toEqual(new Uint8Array([0, 255, 13, 10]));
    if (protocol === "images/edits") expect(options.body.get("prompt")).toBe("add color");
    expect(options.body.has("source")).toBe(false);
  });
  it("returns playable speech bytes and reports JSON errors instead of playing them", async () => {
    environment();
    const fetcher = vi.fn().mockResolvedValue(new Response(new Uint8Array([82, 73, 70, 70, 0, 255]), { headers: { "content-type": "audio/wav" } }));
    vi.stubGlobal("fetch", fetcher);
    const result = await testModelService("speech", "audio/speech", "hello", new AbortController().signal, { voice: "speaker-a" });
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ model: "speech", input: "hello", voice: "speaker-a", response_format: "wav" });
    expect(result.blob?.type).toBe("audio/wav");
    expect(new Uint8Array(await result.blob!.arrayBuffer())).toEqual(new Uint8Array([82, 73, 70, 70, 0, 255]));
    fetcher.mockResolvedValue(new Response('{"error":{"message":"unknown voice"}}', { status: 400, headers: { "content-type": "application/json" } }));
    await expect(testModelService("speech", "audio/speech", "hello", new AbortController().signal)).rejects.toThrow("unknown voice");
  });
  it.each(["chat/completions", "responses", "messages"] as const)("includes image input using the %s vision schema", async protocol => {
    environment();
    vi.stubGlobal("FileReader", class { result = "data:image/png;base64,AQID"; onload?: () => void; readAsDataURL() { this.onload?.(); } });
    const fetcher = vi.fn().mockResolvedValue(new Response('{"ok":true}'));
    vi.stubGlobal("fetch", fetcher);
    await testModelService("vision", protocol, "describe", new AbortController().signal, { file: new File(["image"], "input.png", { type: "image/png" }) });
    const body = JSON.parse(fetcher.mock.calls[0][1].body);
    if (protocol === "chat/completions") expect(body.messages[0].content[1]).toEqual({ type: "image_url", image_url: { url: "data:image/png;base64,AQID" } });
    if (protocol === "responses") expect(body.input[0].content[1]).toEqual({ type: "input_image", image_url: "data:image/png;base64,AQID" });
    if (protocol === "messages") expect(body.messages[0].content[1]).toEqual({ type: "image", source: { type: "base64", media_type: "image/png", data: "AQID" } });
  });
  it("displays text for each API and summarizes embedding vectors without losing the raw response", () => {
    expect(serviceTestText({ choices: [{ message: { content: "chat" } }] }, "chat/completions")).toBe("chat");
    expect(serviceTestText({ output: [{ content: [{ type: "output_text", text: "response" }] }] }, "responses")).toBe("response");
    expect(serviceTestText({ content: [{ type: "text", text: "message" }] }, "messages")).toBe("message");
    const response = { data: [{ index: 0, embedding: Array.from({ length: 12 }, (_, i) => i) }] };
    expect(JSON.parse(serviceTestText(response, "embeddings"))[0]).toEqual({ index: 0, dimensions: 12, preview: [0, 1, 2, 3, 4, 5, 6, 7] });
    expect(response.data[0].embedding).toHaveLength(12);
  });
});
