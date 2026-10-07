import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query';

import { isUnauthorized } from '../../shared/api';

/** onUnauthorized runs on every API 401, from a query or a mutation alike. */
export function createQueryClient(onUnauthorized?: () => void) {
  const onError = (error: unknown) => {
    if (isUnauthorized(error)) {
      onUnauthorized?.();
    }
  };
  return new QueryClient({
    queryCache: new QueryCache({ onError }),
    mutationCache: new MutationCache({ onError }),
    defaultOptions: {
      queries: { retry: 1, staleTime: 30_000 },
      mutations: { retry: 0 },
    },
  });
}
