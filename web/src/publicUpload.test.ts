import { afterEach, expect, it, vi } from "vitest";

afterEach(() => vi.unstubAllGlobals());

it("adds public session CSRF to model upload chunks as well as fetch requests", async () => {
  vi.resetModules();
  vi.stubGlobal("localStorage", { getItem: () => null, setItem: () => {}, removeItem: () => {} });
  const nativeFetch = vi.fn().mockImplementation(async () => new Response(JSON.stringify({ upload_id: "review-upload" }), { status: 200, headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("window", { fetch: nativeFetch, location: { href: "https://gateway.example/", origin: "https://gateway.example", replace: vi.fn() } });
  vi.stubGlobal("document", { cookie: "__Host-csglite_csrf=review-csrf" });
  const chunkHeaders = new Map<string, string>();
  vi.stubGlobal("XMLHttpRequest", class {
    upload = {};
    status = 200;
    responseText = JSON.stringify({ next_offset: 4, complete: true });
    onload = () => {};
    open() {}
    setRequestHeader(name: string, value: string) { chunkHeaders.set(name.toLowerCase(), value); }
    send() { this.onload(); }
  });
  await import("./utils/publicSession");
  vi.stubGlobal("fetch", window.fetch);
  const { uploadLocalModel } = await import("./api/client");
  await uploadLocalModel({ mode: "files", files: [{ file: new File(["GGUF"], "model.gguf"), path: "model.gguf" }] });
  expect(new Headers(nativeFetch.mock.calls[0][1].headers).get("X-CSGLite-CSRF")).toBe("review-csrf");
  expect(chunkHeaders.get("x-csglite-csrf")).toBe("review-csrf");
});
