import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query';

import { apiClient, unwrap, unwrapEmpty } from '../../../shared/api';

// Who the invite is for. A dead link stays dead: 404 and 410 are not retried.
export const inviteQuery = (token: string) =>
  queryOptions({
    queryKey: ['invites', token],
    queryFn: ({ signal }) =>
      unwrap(apiClient.GET('/invites/{token}', { params: { path: { token } }, signal })),
    retry: false,
  });

// Accepting signs the person in: whatever the cache held belongs to no session.
export function useAcceptInvite(token: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (password: string) =>
      unwrapEmpty(
        apiClient.POST('/invites/{token}/accept', {
          params: { path: { token } },
          body: { password },
        }),
      ),
    onSuccess: () => {
      queryClient.clear();
    },
  });
}

// Whose password the reset link sets. A dead link stays dead: 404 and 410 are not retried.
export const passwordResetQuery = (token: string) =>
  queryOptions({
    queryKey: ['password-resets', token],
    queryFn: ({ signal }) =>
      unwrap(apiClient.GET('/password-resets/{token}', { params: { path: { token } }, signal })),
    retry: false,
  });

// Completing closes every other session and signs the person in anew: the cache belongs to none.
export function useCompletePasswordReset(token: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (password: string) =>
      unwrapEmpty(
        apiClient.POST('/password-resets/{token}/complete', {
          params: { path: { token } },
          body: { password },
        }),
      ),
    onSuccess: () => {
      queryClient.clear();
    },
  });
}
