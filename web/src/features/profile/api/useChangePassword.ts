import { useMutation } from '@tanstack/react-query';

import { apiClient, unwrapEmpty } from '../../../shared/api';
import type { components } from '../../../shared/api';

type ChangePasswordRequest = components['schemas']['ChangePasswordRequest'];

// This session survives the change, so nothing cached has to be dropped.
export function useChangePassword() {
  return useMutation({
    mutationFn: (body: ChangePasswordRequest) =>
      unwrapEmpty(apiClient.POST('/me/password', { body })),
  });
}
