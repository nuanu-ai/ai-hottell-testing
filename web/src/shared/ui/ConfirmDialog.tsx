import { useEffect, useRef } from 'react';

import { Button } from './Button';
import { Dialog } from './Dialog';
import { Actions, Stack } from './Layout';
import { Notice } from './Notice';

// The part of a react-query mutation the dialog uses; a UseMutationResult<T, Error, void> fits.
export type ConfirmAction<T> = {
  error: Error | null;
  isPending: boolean;
  mutate: (variables: undefined, options?: { onSuccess?: (result: T) => void }) => void;
  reset: () => void;
};

type ConfirmDialogProps<T> = {
  open: boolean;
  title: string;
  text: string;
  confirm: string;
  // bad: an action that cannot be undone (the default); primary: an ordinary one.
  tone?: 'bad' | 'primary';
  action: ConfirmAction<T>;
  onDone?: (result: T) => void;
  onClosed: () => void;
};

// One question before an action: a visible title, the error above the text,
// Cancel and the action on the right. An error keeps the dialog open.
export function ConfirmDialog<T>({
  open,
  title,
  text,
  confirm,
  tone = 'bad',
  action,
  onDone,
  onClosed,
}: ConfirmDialogProps<T>) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const close = () => {
    dialogRef.current?.close();
  };

  useEffect(() => {
    if (open) dialogRef.current?.showModal();
  }, [open]);

  return (
    <Dialog
      ref={dialogRef}
      title={title}
      onClose={() => {
        action.reset();
        onClosed();
      }}
    >
      {open && (
        <>
          <Stack>
            {action.error && <Notice tone="err">{action.error.message}</Notice>}
            <p>{text}</p>
          </Stack>
          <Actions justify="end" className="dialog-acts">
            <Button onClick={close}>Отмена</Button>
            <Button
              variant={tone === 'primary' ? 'primary' : undefined}
              className={tone === 'bad' ? 't-bad' : undefined}
              disabled={action.isPending}
              onClick={() => {
                action.mutate(undefined, {
                  onSuccess: (result) => {
                    onDone?.(result);
                    close();
                  },
                });
              }}
            >
              {confirm}
            </Button>
          </Actions>
        </>
      )}
    </Dialog>
  );
}
