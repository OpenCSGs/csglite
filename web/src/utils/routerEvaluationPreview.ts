import type {
  ProviderPoolRouterEvaluationPreview,
  ProviderPoolRouterEvaluationJob,
  ProviderPoolRouterEvaluationRequest,
  ProviderPoolRouterProfile,
} from "../api/client";

export function snapshotRouterEvaluationRequest(
  request: ProviderPoolRouterEvaluationRequest,
): ProviderPoolRouterEvaluationRequest {
  return structuredClone(request);
}

export function previewResponseIsCurrent(responseGeneration: number, currentGeneration: number): boolean {
  return responseGeneration === currentGeneration;
}

export function routerUnknownPricingNeedsConsent(
  preview: Pick<ProviderPoolRouterEvaluationPreview, "requires_unknown_pricing_consent">,
  request: Pick<ProviderPoolRouterEvaluationRequest, "allow_unknown_pricing">,
): boolean {
  return preview.requires_unknown_pricing_consent && !request.allow_unknown_pricing;
}

export function routerEvaluationModeKey(mode?: string): string {
  return mode === "absolute_v1"
    ? "settings.routerEvaluationMode.absolute_v1"
    : "settings.routerEvaluationMode.listwise_v2";
}

export function routerJobCallCounts(job: ProviderPoolRouterEvaluationJob, memberCount: number) {
  if (job.direct_candidate_calls !== undefined && job.judge_calls !== undefined) {
    return {
      candidateCalls: job.direct_candidate_calls,
      judgeCalls: job.judge_calls,
      maxJudgeCalls: job.max_judge_calls ?? job.judge_calls,
    };
  }
  if (job.evaluation_mode === "absolute_v1") {
    return { candidateCalls: job.total, judgeCalls: job.total, maxJudgeCalls: job.total * 3 };
  }
  const divisor = Math.max(1, memberCount + 1);
  const judgeCalls = Math.floor(job.total / divisor);
  return {
    candidateCalls: Math.max(0, job.total - judgeCalls),
    judgeCalls,
    maxJudgeCalls: judgeCalls * 3,
  };
}

export function routerProfileKind(profile: Pick<ProviderPoolRouterProfile, "schema_version" | "v2">) {
  if (profile.schema_version === 2) {
    return profile.v2?.model_type === "pairwise_forest_v1" ? "pairwise_forest" : "similarity_bt";
  }
  return "semantic_cluster_v1";
}

export function routerActivationReasonKey(
  profile: Pick<ProviderPoolRouterProfile, "activation_allowed" | "activation_blocked_reason">,
): string | undefined {
  if (profile.activation_allowed) return undefined;
  return `settings.routerBlocked.${profile.activation_blocked_reason || "unknown"}`;
}

export function isValidatedCollapsedProfile(
  profile: Pick<ProviderPoolRouterProfile, "schema_version" | "collapsed_single_member" | "collapsed_quality_passed">,
): boolean {
  return profile.schema_version === 2 && profile.collapsed_single_member && profile.collapsed_quality_passed;
}
