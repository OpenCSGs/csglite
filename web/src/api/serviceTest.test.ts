import { afterEach, describe, expect, it, vi } from "vitest";
import { serviceTestText, testModelService } from "./serviceTest";

function environment() {
  vi.stubGlobal("window", { location: { href: "https://gateway.example/ai-gateway", origin: "https://gateway.example" } });
  vi.stubGlobal("document", { cookie: "__Host-csglite_csrf=csrf-test" });
}
afterEach(() => vi.unstubAllGlobals());

describe("per-model API testing", () => {
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
  it("displays text for each API and summarizes embedding vectors without losing the raw response", () => {
    expect(serviceTestText({ choices: [{ message: { content: "chat" } }] }, "chat/completions")).toBe("chat");
    expect(serviceTestText({ output: [{ content: [{ type: "output_text", text: "response" }] }] }, "responses")).toBe("response");
    expect(serviceTestText({ content: [{ type: "text", text: "message" }] }, "messages")).toBe("message");
    const response = { data: [{ index: 0, embedding: Array.from({ length: 12 }, (_, i) => i) }] };
    expect(JSON.parse(serviceTestText(response, "embeddings"))[0]).toEqual({ index: 0, dimensions: 12, preview: [0, 1, 2, 3, 4, 5, 6, 7] });
    expect(response.data[0].embedding).toHaveLength(12);
  });
});
