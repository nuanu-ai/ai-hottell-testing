import { useQueryClient } from '@tanstack/react-query';
import { useBlocker } from '@tanstack/react-router';
import { useEffect, useRef } from 'react';

import { meQueryOptions } from '../../shared/api';
import { Actions, Button, Dialog, Stack } from '../../shared/ui';

const title = 'Есть несохранённые изменения';

// Asks before a draft is lost: leaving the page through the app asks in a dialog, closing
// or reloading the browser tab asks through the browser. Switching the page's own tabs
// changes only its search, so it is not leaving. A logout or a 401 drops the cached session
// before it sends the visitor to sign in: with no session the page cannot save, so that
// navigation is never blocked, while a live session going back to /login still asks.
export function LeaveGuard({ dirty }: { dirty: boolean }) {
  const queryClient = useQueryClient();
  const blocker = useBlocker({
    shouldBlockFn: ({ current, next }) =>
      dirty &&
      next.pathname !== current.pathname &&
      queryClient.getQueryData(meQueryOptions.queryKey) !== undefined,
    // Closing the tab is guarded below: the router's own guard lives in browser history only.
    enableBeforeUnload: false,
    withResolver: true,
  });

  useEffect(() => {
    if (!dirty) return;
    const ask = (event: BeforeUnloadEvent) => {
      event.preventDefault();
    };
    window.addEventListener('beforeunload', ask);
    return () => {
      window.removeEventListener('beforeunload', ask);
    };
  }, [dirty]);
  const dialogRef = useRef<HTMLDialogElement>(null);
  const blocked = blocker.status === 'blocked';

  useEffect(() => {
    if (blocked) dialogRef.current?.showModal();
  }, [blocked]);

  return (
    <Dialog
      ref={dialogRef}
      aria-label={title}
      onClose={() => {
        // Esc or «Остаться»: the navigation is dropped and the draft stays.
        blocker.reset?.();
      }}
    >
      {blocked && (
        <Stack>
          <p>{title}. Если уйти со страницы, они пропадут</p>
          <Actions justify="end">
            <Button
              onClick={() => {
                dialogRef.current?.close();
              }}
            >
              Остаться
            </Button>
            <Button
              className="t-bad"
              onClick={() => {
                blocker.proceed();
              }}
            >
              Уйти
            </Button>
          </Actions>
        </Stack>
      )}
    </Dialog>
  );
}
