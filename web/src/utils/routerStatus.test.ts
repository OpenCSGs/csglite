import { describe, expect, it } from "vitest";

import type { ProviderPoolRouterStatus } from "../api/client";
import { routerOverviewCounts } from "./routerStatus";

describe("routerOverviewCounts", () => {
  it("uses live top-level counts without a pending suggestion", () => {
    expect(routerOverviewCounts({
      qualified_query_count: 30,
      new_query_count: 0,
      semantic_differentiation: false,
    })).toEqual({ qualifiedQueryCount: 30, newQueryCount: 0 });
  });

  it("never substitutes stale suggestion snapshot counts", () => {
    const status: ProviderPoolRouterStatus = {
      qualified_query_count: 31,
      new_query_count: 1,
      semantic_differentiation: false,
      pending_suggestion: {
        id: "suggestion",
        reason: "new_qualified_traces",
        qualified_query_count: 20,
        new_query_count: 20,
        member_compatible: true,
        status: "pending",
        created_at: "2026-08-27T00:00:00Z",
        updated_at: "2026-08-27T00:00:00Z",
      },
    };

    expect(routerOverviewCounts(status)).toEqual({
      qualifiedQueryCount: 31,
      newQueryCount: 1,
    });
  });
});
