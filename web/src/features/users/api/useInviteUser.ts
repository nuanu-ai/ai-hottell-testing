import { useMutation, useQueryClient } from '@tanstack/react-query';

import { apiClient, unwrap } from '../../../shared/api';
import type { components } from '../../../shared/api';
import { usersKeys } from '../../../shared/api';

type InviteUserRequest = components['schemas']['InviteUserRequest'];

// The new user joins the list as invited: the list is asked again once the link exists.
export function useInviteUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: InviteUserRequest) => unwrap(apiClient.POST('/users', { body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: usersKeys.all }),
  });
}
