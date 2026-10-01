import { useMutation, useQueryClient } from '@tanstack/react-query';

import { apiClient, meQueryOptions, unwrapEmpty } from '../../shared/api';

type Credentials = { email: string; password: string };

// A new session changes who «me» is: the cached answer is dropped before moving on.
export function useLogin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Credentials) => unwrapEmpty(apiClient.POST('/auth/login', { body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: meQueryOptions.queryKey }),
  });
}
