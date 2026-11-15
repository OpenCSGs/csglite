import { afterEach, expect, it, vi } from "vitest";

afterEach(() => vi.unstubAllGlobals());

it.each(["desktop", "local", "lan", "token", null] as const)("handles a 401 according to access mode %s", async (mode) => {
  vi.resetModules();
  const replace = vi.fn();
  vi.stubGlobal("window", { location: { href: "http://127.0.0.1:43123/", replace } });
  vi.stubGlobal("document", { cookie: "" });
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 401 })));
  const { consoleAccessMode } = await import("./utils/consoleAuth");
  consoleAccessMode.value = mode;
  const { refreshConsoleSession, consoleSessionError } = await import("./consoleAccess");
  await refreshConsoleSession();
  if (mode === "token") expect(replace).toHaveBeenCalledWith("/login");
  else {
    expect(replace).not.toHaveBeenCalled();
    expect(consoleSessionError.value).toBe(true);
  }
});

it("updates access mode from the server and recovers after a failed connection", async () => {
  vi.resetModules();
  const session = { authenticated: true, access_mode: "lan", role: "administrator", instance_name: "test" };
  vi.stubGlobal("fetch", vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(new Response(JSON.stringify(session))));
  const { refreshConsoleSession, consoleSessionError, consoleSession } = await import("./consoleAccess");
  const { consoleAccessMode } = await import("./utils/consoleAuth");
  await refreshConsoleSession();
  expect(consoleSessionError.value).toBe(true);
  await refreshConsoleSession();
  expect(consoleSession.value).toEqual(session);
  expect(consoleAccessMode.value).toBe("lan");
  expect(consoleSessionError.value).toBe(false);
});
