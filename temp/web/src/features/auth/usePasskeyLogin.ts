import { useMutation, useQueryClient } from '@tanstack/react-query';

import { apiClient, meQueryOptions, unwrap, unwrapEmpty } from '../../shared/api';
import { getPasskey } from '../../shared/lib/webauthn';

// begin → the browser's prompt → finish; like useLogin, a new session drops the cached «me».
export function usePasskeyLogin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const { options } = await unwrap(apiClient.POST('/auth/passkey/login/begin', { body: {} }));
      const credential = await getPasskey(options);
      await unwrapEmpty(apiClient.POST('/auth/passkey/login/finish', { body: { credential } }));
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: meQueryOptions.queryKey }),
  });
}
