import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query';

import { apiClient, unwrap } from '../../../shared/api';
import type { components } from '../../../shared/api';

export type Proposal = components['schemas']['Proposal'];
export type ProposalAxesValue = components['schemas']['ProposalAxes'];
export type UserRow = components['schemas']['UserListItem'];

// GET /proposals: every user's registry, or one person's when user is set.
export function proposalsQuery(user: string | null) {
  return queryOptions({
    queryKey: ['proposals', user] as const,
    queryFn: ({ signal }) =>
      unwrap(
        apiClient.GET('/proposals', { params: { query: { user: user ?? undefined } }, signal }),
      ),
  });
}

// The people of the «Человек» filter.
export const usersQuery = queryOptions({
  queryKey: ['users'] as const,
  queryFn: ({ signal }) => unwrap(apiClient.GET('/users', { signal })),
});

export type Decision = components['schemas']['DecisionRequest']['status'];

// POST /proposals/{userId}/{proposalId}/decisions: the owner's decision. The answer is the
// proposal as the registry now projects it; every registry list is refetched so the axis
// «Решение» changes without a reload.
export function useDecideProposal() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ proposal, status }: { proposal: Proposal; status: Decision }) =>
      unwrap(
        apiClient.POST('/proposals/{userId}/{proposalId}/decisions', {
          params: { path: { userId: proposal.user_id, proposalId: proposal.id } },
          body: { status },
        }),
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['proposals'] }),
  });
}

export type Journal = components['schemas']['Journal'];
export type JournalEntry = components['schemas']['JournalEntry'];
export type ImprovementCycle = components['schemas']['ImprovementCycle'];

export type JournalKey = { user?: string | null; from?: string; to?: string };

// GET /journal: the folded entries, the newest first, and the counters of steps 4–6.
export function journalQuery({ user, from, to }: JournalKey) {
  return queryOptions({
    queryKey: ['journal', user ?? null, from ?? null, to ?? null] as const,
    queryFn: ({ signal }) =>
      unwrap(
        apiClient.GET('/journal', {
          params: { query: { user: user ?? undefined, from, to } },
          signal,
        }),
      ),
  });
}
