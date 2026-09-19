import { afterEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

import { ApiError, inviteClusterNode, isClusterDisabledError, isFeatureNotLicensedError, joinCluster, syncClusterModel } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

describe("cluster client errors", () => {
  it("keeps the node-cap fields of a 403 feature_not_licensed body", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({
      error: "cluster node limit reached",
      errorCode: "feature_not_licensed",
      code: "feature_not_licensed",
      limit: 2,
      current: 2,
    }, 403)));

    const err = await joinCluster("tok").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(isFeatureNotLicensedError(err)).toBe(true);
    expect(isClusterDisabledError(err)).toBe(false);
    const apiErr = err as ApiError;
    expect(apiErr.status).toBe(403);
    expect(apiErr.limit).toBe(2);
    expect(apiErr.current).toBe(2);
    expect(apiErr.message).toBe("cluster node limit reached");
  });

  it("recognises the 503 cluster_disabled code", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "cluster disabled", code: "cluster_disabled" }, 503)));
    const err = await inviteClusterNode("12345678").catch((e: unknown) => e);
    expect(isClusterDisabledError(err)).toBe(true);
    expect(isFeatureNotLicensedError(err)).toBe(false);
  });

  it("does not treat a plain 401 as a licence problem", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "wrong token" }, 401)));
    const err = await joinCluster("bad").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(401);
    expect(isFeatureNotLicensedError(err)).toBe(false);
  });

  it("posts the sync body as JSON with the node selector", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ model: "m", results: [] }, 202));
    vi.stubGlobal("fetch", fetchMock);
    await expect(syncClusterModel("m", "all")).resolves.toEqual({ model: "m", results: [] });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/cluster/models/sync");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({ model: "m", nodes: "all" });
    expect(new Headers(init.headers).get("Content-Type")).toBe("application/json");
  });
});
