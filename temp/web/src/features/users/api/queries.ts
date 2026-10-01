import { queryOptions } from '@tanstack/react-query';

import { apiClient, unwrap } from '../../../shared/api';

export const usersKeys = {
  all: ['users'] as const,
};

// Every user, invited ones included; the invite and row actions refresh it by this key.
export const usersQuery = queryOptions({
  queryKey: usersKeys.all,
  queryFn: ({ signal }) => unwrap(apiClient.GET('/users', { signal })),
});
