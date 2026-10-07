import { queryOptions } from '@tanstack/react-query';

import { ApiError, apiClient, unwrap } from '../../../shared/api';
import type { components } from '../../../shared/api';

export type DeepReport = components['schemas']['DeepReport'];
export type DeepCandidate = components['schemas']['DeepCandidate'];
type Agent = components['schemas']['AnalyticsAgent'];

export type DeepReportKey = { sessionId: string; user?: string; agent?: Agent };

// GET /deep/reports/{sessionId}: the published report and the unchecked candidate. A session
// with neither answers 404 not_found, which the screen shows as «no report yet», so it is null here.
export function deepReportQuery({ sessionId, user, agent }: DeepReportKey) {
  return queryOptions({
    queryKey: ['deep', 'report', sessionId, user ?? null, agent ?? null] as const,
    queryFn: async ({ signal }): Promise<DeepReport | null> => {
      try {
        return await unwrap(
          apiClient.GET('/deep/reports/{sessionId}', {
            params: { path: { sessionId }, query: { user, agent } },
            signal,
          }),
        );
      } catch (error) {
        if (error instanceof ApiError && error.status === 404) return null;
        throw error;
      }
    },
  });
}

// The candidate matters only when it is newer than the published version.
export function pendingCandidate(report: DeepReport): DeepCandidate | undefined {
  const { candidate, published } = report;
  if (!candidate) return undefined;
  if (published && Date.parse(candidate.submitted_at) <= Date.parse(published.published_at)) {
    return undefined;
  }
  return candidate;
}

export type SkillOpportunitiesAnswer = components['schemas']['SkillOpportunities'];

// GET /skill-opportunities: the current report of a user (the signed-in one without user).
export function skillOpportunitiesQuery(user: string | null) {
  return queryOptions({
    queryKey: ['skill-opportunities', user] as const,
    queryFn: ({ signal }) =>
      unwrap(
        apiClient.GET('/skill-opportunities', {
          params: { query: { user: user ?? undefined } },
          signal,
        }),
      ),
  });
}
