import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';

import { apiClient, unwrapEmpty } from '../../shared/api';

// Signing out drops every cached answer: none of it belongs to the next session.
export function useLogout() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: () => unwrapEmpty(apiClient.POST('/auth/logout')),
    onSuccess: async () => {
      queryClient.clear();
      await navigate({ to: '/login' });
    },
  });
}
