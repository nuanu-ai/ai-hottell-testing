import { queryOptions } from '@tanstack/react-query';

import { apiClient, isUnauthorized, unwrap } from './client';

// The one source of the signed-in user for the shell, the routes and the profile.
export const meQueryOptions = queryOptions({
  queryKey: ['me'],
  queryFn: ({ signal }) => unwrap(apiClient.GET('/me', { signal })),
  staleTime: 5 * 60_000,
  // 401 means no session: asking again cannot change the answer.
  retry: (failureCount, error) => !isUnauthorized(error) && failureCount < 1,
});
