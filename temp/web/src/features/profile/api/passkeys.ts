import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query';

import { apiClient, unwrap, unwrapEmpty } from '../../../shared/api';
import { createPasskey } from '../../../shared/lib/webauthn';

export const passkeysKeys = {
  all: ['me', 'passkeys'] as const,
};

// The signed-in user's passkeys, oldest first; adding and deleting refresh it by this key.
export const passkeysQuery = queryOptions({
  queryKey: passkeysKeys.all,
  queryFn: ({ signal }) => unwrap(apiClient.GET('/me/passkeys', { signal })),
});

function useRefreshPasskeys() {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: passkeysKeys.all });
}

// begin with the name → the browser's prompt → finish; the answer is the added passkey.
export function useAddPasskey() {
  const onSuccess = useRefreshPasskeys();
  return useMutation({
    mutationFn: async (name: string) => {
      const { options } = await unwrap(
        apiClient.POST('/me/passkeys/register/begin', { body: { name } }),
      );
      const credential = await createPasskey(options);
      return unwrap(apiClient.POST('/me/passkeys/register/finish', { body: { credential } }));
    },
    onSuccess,
  });
}

export function useDeletePasskey() {
  const onSuccess = useRefreshPasskeys();
  return useMutation({
    mutationFn: (id: string) =>
      unwrapEmpty(apiClient.DELETE('/me/passkeys/{id}', { params: { path: { id } } })),
    onSuccess,
  });
}
