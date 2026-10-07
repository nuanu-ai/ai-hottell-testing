import type { RouterHistory } from '@tanstack/react-router';

import { createQueryClient } from './providers';
import { createAppRouter, leaveClosedPage } from './router';

// The cache and the router know each other: a 401 anywhere moves the router.
export function createApp(history?: RouterHistory) {
  const queryClient = createQueryClient(() => {
    leaveClosedPage(router, queryClient);
  });
  const router = createAppRouter(queryClient, history);
  return { queryClient, router };
}
