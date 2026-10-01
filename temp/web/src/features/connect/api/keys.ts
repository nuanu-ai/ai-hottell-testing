import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query';

import { apiClient, unwrap, unwrapEmpty } from '../../../shared/api';

export const keysKeys = {
  all: ['me', 'keys'] as const,
};

// The state of the MCP key and the collector token; every action refreshes it by this key.
export const keysQuery = queryOptions({
  queryKey: keysKeys.all,
  queryFn: ({ signal }) => unwrap(apiClient.GET('/me/keys', { signal })),
});

function useRefreshKeys() {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: keysKeys.all });
}

// Issuing revokes the key the user had; the answer is the open value, shown once.
export function useIssueMcpKey() {
  const onSuccess = useRefreshKeys();
  return useMutation({
    mutationFn: () => unwrap(apiClient.POST('/me/keys/mcp')),
    onSuccess,
  });
}

export function useRevokeMcpKey() {
  const onSuccess = useRefreshKeys();
  return useMutation({
    mutationFn: () => unwrapEmpty(apiClient.DELETE('/me/keys/mcp')),
    onSuccess,
  });
}

export function useReissueIngestToken() {
  const onSuccess = useRefreshKeys();
  return useMutation({
    mutationFn: () => unwrapEmpty(apiClient.POST('/me/keys/ingest/reissue')),
    onSuccess,
  });
}
