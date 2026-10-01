import type { UseMutationResult } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';

import { Button, Dialog, Notice } from '../../shared/ui';

type ConfirmDialogProps<T> = {
  open: boolean;
  title: string;
  text: string;
  confirm: string;
  action: UseMutationResult<T, Error, void>;
  onDone?: (result: T) => void;
  onClosed: () => void;
};

// One question before an action that cannot be undone; an error keeps the dialog open.
export function ConfirmDialog<T>({
  open,
  title,
  text,
  confirm,
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
      aria-label={title}
      onClose={() => {
        action.reset();
        onClosed();
      }}
    >
      {open && (
        <div style={{ display: 'grid', gap: 14 }}>
          {action.error && <Notice tone="err">{action.error.message}</Notice>}
          <p style={{ margin: 0 }}>{text}</p>
          <div className="row" style={{ justifyContent: 'flex-end', marginTop: 4 }}>
            <Button onClick={close}>Отмена</Button>
            <Button
              className="плохо"
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
          </div>
        </div>
      )}
    </Dialog>
  );
}
