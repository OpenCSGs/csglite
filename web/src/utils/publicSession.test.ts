import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const origin = "https://gateway.example";
let nativeFetch: ReturnType<typeof vi.fn>;
let replace: ReturnType<typeof vi.fn>;

beforeEach(async () => {
  vi.resetModules();
  nativeFetch = vi.fn();
  replace = vi.fn();
  vi.stubGlobal("window", { fetch: nativeFetch, location: { href: origin + "/", origin, replace } });
  vi.stubGlobal("document", { cookie: "__Host-csglite_csrf=test-csrf" });
  await import("./publicSession");
});

afterEach(() => vi.unstubAllGlobals());

describe("console session and cloud account isolation", () => {
  it("keeps console access when the cloud balance credential expires", async () => {
    const unauthorized = new Response(null, { status: 401 });
    nativeFetch.mockResolvedValueOnce(unauthorized).mockResolvedValueOnce(new Response(null, { status: 200 }));
    expect(await window.fetch("/api/cloud/account/balance")).toBe(unauthorized);
    expect(nativeFetch).toHaveBeenLastCalledWith("/auth/session", { credentials: "same-origin", cache: "no-store" });
    expect(replace).not.toHaveBeenCalled();
  });

  it("returns to console access when its own session expires", async () => {
    nativeFetch.mockResolvedValue(new Response(null, { status: 401 }));
    await window.fetch("/api/settings");
    expect(replace).toHaveBeenCalledWith("/login");
  });

  it("does not treat network failure as console logout", async () => {
    nativeFetch.mockResolvedValueOnce(new Response(null, { status: 401 })).mockRejectedValueOnce(new Error("offline"));
    await window.fetch("/api/cloud/account/balance");
    expect(replace).not.toHaveBeenCalled();
  });

  it("adds CSRF only to same-origin writes", async () => {
    nativeFetch.mockResolvedValue(new Response());
    await window.fetch("/api/cloud/auth/token", { method: "DELETE" });
    expect(new Headers(nativeFetch.mock.calls[0][1].headers).get("X-CSGLite-CSRF")).toBe("test-csrf");
    await window.fetch("https://cloud.example/api", { method: "POST" });
    expect(nativeFetch.mock.calls[1][1]).toEqual({ method: "POST" });
  });

  it("does not change private console access", async () => {
    vi.stubGlobal("document", { cookie: "" });
    nativeFetch.mockResolvedValue(new Response(null, { status: 401 }));
    await window.fetch("/api/cloud/account/balance");
    expect(nativeFetch).toHaveBeenCalledTimes(1);
    expect(replace).not.toHaveBeenCalled();
  });

  it("does not intercept external service authentication failures", async () => {
    nativeFetch.mockResolvedValue(new Response(null, { status: 401 }));
    await window.fetch("https://cloud.example/api");
    expect(nativeFetch).toHaveBeenCalledTimes(1);
    expect(replace).not.toHaveBeenCalled();
  });
});
