import { queryOptions } from '@tanstack/react-query';

import { apiClient, unwrap } from '../../../shared/api';
import { analyticsKeys } from './queries';

// How sending works for a person, by what the server knows: the head's send status reads it,
// and the start page asks it whether the signed-in person has a binary connected at all.
export const deliveryQueryOptions = (user?: string) =>
  queryOptions({
    queryKey: [...analyticsKeys.all, 'delivery', user ?? 'me'],
    queryFn: ({ signal }) =>
      unwrap(
        apiClient.GET('/analytics/delivery', {
          params: { query: user ? { user } : {} },
          signal,
        }),
      ),
    staleTime: 30_000,
  });

// The head's send line: the signed-in person's status once a minute, not in a hidden tab.
export const sendStatusQuery = {
  ...deliveryQueryOptions(),
  refetchInterval: 60_000,
  refetchIntervalInBackground: false,
};
