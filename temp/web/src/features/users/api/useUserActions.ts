import { useMutation, useQueryClient } from '@tanstack/react-query';

import { apiClient, unwrap, unwrapEmpty } from '../../../shared/api';
import { usersKeys } from './queries';

// Success or failure, the list is asked again: someone else may have changed the user meanwhile.
function useRefreshUsers() {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: usersKeys.all });
}

export function useReissueInvite() {
  const onSettled = useRefreshUsers();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(apiClient.POST('/users/{id}/invite', { params: { path: { id } } })),
    onSettled,
  });
}

export function useRevokeInvite() {
  const onSettled = useRefreshUsers();
  return useMutation({
    mutationFn: (id: string) =>
      unwrapEmpty(apiClient.DELETE('/users/{id}', { params: { path: { id } } })),
    onSettled,
  });
}

export function useIssuePasswordReset() {
  const onSettled = useRefreshUsers();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(apiClient.POST('/users/{id}/password-reset', { params: { path: { id } } })),
    onSettled,
  });
}
