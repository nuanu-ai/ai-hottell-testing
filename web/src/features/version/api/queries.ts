import { queryOptions } from '@tanstack/react-query';

import { apiClient, unwrap } from '../../../shared/api';

export const versionKeys = {
  all: ['version'] as const,
};

// The build version never changes while the page is open.
export const versionQuery = queryOptions({
  queryKey: versionKeys.all,
  queryFn: ({ signal }) => unwrap(apiClient.GET('/version', { signal })),
  staleTime: Infinity,
});
