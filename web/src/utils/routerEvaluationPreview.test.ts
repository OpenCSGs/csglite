import { describe, expect, it } from "vitest";
import {
  isValidatedCollapsedProfile,
  previewResponseIsCurrent,
  routerActivationReasonKey,
  routerEvaluationModeKey,
  routerJobCallCounts,
  routerProfileKind,
  routerUnknownPricingNeedsConsent,
  snapshotRouterEvaluationRequest,
} from "./routerEvaluationPreview";

describe("router evaluation preview helpers", () => {
  it("takes an independent request snapshot", () => {
    const request = {
      judge_model: "judge",
      max_queries: 10,
      repeats: 1,
      max_output_tokens: 100,
      request_timeout_seconds: 60,
      budget_currency: "USD",
      budget_amount: 1,
      allow_unknown_pricing: false,
    };
    const snapshot = snapshotRouterEvaluationRequest(request);
    request.max_queries = 20;
    expect(snapshot.max_queries).toBe(10);
  });

  it("rejects an out-of-order preview generation", () => {
    expect(previewResponseIsCurrent(1, 2)).toBe(false);
    expect(previewResponseIsCurrent(2, 2)).toBe(true);
  });

  it("keeps V1 and V2 modes and call counts distinguishable", () => {
    expect(routerEvaluationModeKey("absolute_v1")).toContain("absolute_v1");
    expect(routerEvaluationModeKey("listwise_v2")).toContain("listwise_v2");
    expect(routerJobCallCounts({ evaluation_mode: "absolute_v1", total: 4 } as any, 2)).toEqual({
      candidateCalls: 4, judgeCalls: 4, maxJudgeCalls: 12,
    });
    expect(routerJobCallCounts({
      evaluation_mode: "listwise_v2",
      total: 8,
      direct_candidate_calls: 6,
      judge_calls: 2,
      max_judge_calls: 6,
    } as any, 3)).toEqual({ candidateCalls: 6, judgeCalls: 2, maxJudgeCalls: 6 });
  });

  it("classifies profile rendering and conservative activation semantics", () => {
    expect(routerProfileKind({ schema_version: 1 } as any)).toBe("semantic_cluster_v1");
    expect(routerProfileKind({ schema_version: 2, v2: { model_type: "pairwise_forest_v1" } } as any)).toBe("pairwise_forest");
    expect(routerProfileKind({ schema_version: 2, v2: { model_type: "similarity_weighted_bt_v1" } } as any)).toBe("similarity_bt");
    expect(routerActivationReasonKey({
      activation_allowed: false,
      activation_blocked_reason: "missing_safe_fallback",
    })).toBe("settings.routerBlocked.missing_safe_fallback");
    expect(isValidatedCollapsedProfile({
      schema_version: 2,
      collapsed_single_member: true,
      collapsed_quality_passed: true,
    })).toBe(true);
    expect(isValidatedCollapsedProfile({
      schema_version: 2,
      collapsed_single_member: true,
      collapsed_quality_passed: false,
    })).toBe(false);
  });

  it("requires explicit consent only for unknown-priced previews", () => {
    expect(routerUnknownPricingNeedsConsent(
      { requires_unknown_pricing_consent: true },
      { allow_unknown_pricing: false },
    )).toBe(true);
    expect(routerUnknownPricingNeedsConsent(
      { requires_unknown_pricing_consent: true },
      { allow_unknown_pricing: true },
    )).toBe(false);
  });
});
