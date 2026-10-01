import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query';

import { ApiError, apiClient, toApiError, unwrap } from '../../../shared/api';
import type { components } from '../../../shared/api';

type UpdateRequest = components['schemas']['UpdateTelemetrySettingsRequest'];

export const telemetrySettingsQuery = queryOptions({
  queryKey: ['me', 'telemetry-settings'] as const,
  queryFn: ({ signal }) => unwrap(apiClient.GET('/me/telemetry-settings', { signal })),
});

/** True for an answer 409: the settings were saved elsewhere after they were read. */
export function isVersionConflict(error: unknown): boolean {
  return error instanceof ApiError && error.status === 409;
}

// Saves in place of the version the settings were read at. A 422 names where and why the
// settings break the schema, so its detail joins the message.
export function useSaveTelemetrySettings() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: UpdateRequest) => {
      const { data, error, response } = await apiClient.PUT('/me/telemetry-settings', { body });
      if (response.ok && data !== undefined) {
        return data;
      }
      if (response.status === 422 && error && 'detail' in error) {
        throw new ApiError(422, error.code, `${error.message}: ${error.detail}`);
      }
      throw toApiError(response, error);
    },
    onSuccess: (saved) => {
      queryClient.setQueryData(telemetrySettingsQuery.queryKey, saved);
    },
  });
}
