import type { ProviderPoolRouterStatus } from "../api/client";

export function routerOverviewCounts(status?: ProviderPoolRouterStatus): {
  qualifiedQueryCount: number;
  newQueryCount: number;
} {
  return {
    qualifiedQueryCount: status?.qualified_query_count ?? 0,
    newQueryCount: status?.new_query_count ?? 0,
  };
}
