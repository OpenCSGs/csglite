import { afterEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

import {
  activateProviderPoolRouterProfile,
  getProviderPoolRouterStatus,
  previewProviderPoolRouterEvaluation,
} from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("provider pool router client", () => {
  it("scopes status requests to the encoded pool ID", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      qualified_query_count: 30,
      new_query_count: 0,
      semantic_differentiation: false,
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(getProviderPoolRouterStatus("pool/a")).resolves.toEqual({
      qualified_query_count: 30,
      new_query_count: 0,
      semantic_differentiation: false,
    });
    expect(fetchMock).toHaveBeenCalledWith("/api/provider-pools/pool%2Fa/router/status", expect.anything());
  });

  it("sends the complete bounded preview configuration", async () => {
    const response = {
      eligible_snapshot_count: 2,
      selected_snapshot_count: 2,
      direct_candidate_calls: 4,
      judge_calls: 4,
      max_judge_calls: 12,
      max_total_calls: 16,
      max_token_exposure: 100,
      known_estimated_cost: 0.1,
      currency: "USD",
      unknown_price_members: [],
      judge_price_known: true,
      limits: {
        max_queries: 100,
        max_repeats: 3,
        max_output_tokens: 4096,
        max_request_timeout_seconds: 600,
      },
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(response), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);
    const request = {
      judge_model: "judge",
      max_queries: 2,
      repeats: 1,
      max_output_tokens: 256,
      request_timeout_seconds: 30,
      budget_currency: "USD",
      budget_amount: 1,
      allow_unknown_pricing: false,
    };

    await expect(previewProviderPoolRouterEvaluation("pool", request)).resolves.toEqual(response);
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual(request);
  });

  it("adds an auditable local actor when activating", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      id: 1,
      to_profile_id: "profile",
      action: "activate",
      actor: "local-ui",
      reason: "manual review",
      created_at: "2026-01-01T00:00:00Z",
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await activateProviderPoolRouterProfile("pool", "profile", "manual review");
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({ actor: "local-ui", reason: "manual review" });
  });
});
