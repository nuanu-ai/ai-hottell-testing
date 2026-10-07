import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query';

import { apiClient, unwrap, unwrapEmpty } from '../../../shared/api';
import { analyticsKeys } from './queries';

// The topics a person marked «Не проблема»: kept on the server for that person only.
export const hiddenTopicsKey = [...analyticsKeys.all, 'hidden-topics'] as const;

export const hiddenTopicsQuery = queryOptions({
  queryKey: hiddenTopicsKey,
  queryFn: ({ signal }) => unwrap(apiClient.GET('/analytics/hidden-topics', { signal })),
  select: (data) => new Set(data.keys),
});

type Keys = { keys: string[] };

// Optimistic: the list changes at once and rolls back if the server refuses.
function useHiddenMutation<T>(
  request: (value: T) => Promise<void>,
  apply: (keys: string[], value: T) => string[],
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: request,
    onMutate: async (value: T) => {
      await queryClient.cancelQueries({ queryKey: hiddenTopicsKey });
      const before = queryClient.getQueryData<Keys>(hiddenTopicsKey);
      queryClient.setQueryData<Keys>(hiddenTopicsKey, { keys: apply(before?.keys ?? [], value) });
      return { before };
    },
    onError: (_error, _value, context) => {
      queryClient.setQueryData(hiddenTopicsKey, context?.before);
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: hiddenTopicsKey }),
  });
}

export function useHideTopic() {
  return useHiddenMutation<string>(
    (key) =>
      unwrapEmpty(apiClient.PUT('/analytics/hidden-topics/{key}', { params: { path: { key } } })),
    (keys, key) => (keys.includes(key) ? keys : [...keys, key]),
  );
}

export function useRestoreTopics() {
  return useHiddenMutation<undefined>(
    () => unwrapEmpty(apiClient.DELETE('/analytics/hidden-topics')),
    () => [],
  );
}
