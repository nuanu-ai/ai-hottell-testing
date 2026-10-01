import type { DialogHTMLAttributes, KeyboardEvent, Ref } from 'react';

import { classNames } from './classNames';

type DialogProps = DialogHTMLAttributes<HTMLDialogElement> & {
  // The caller opens and closes the dialog through this ref: showModal() and close().
  ref: Ref<HTMLDialogElement>;
};

export function Dialog({ ref, className, onKeyDown, ...rest }: DialogProps) {
  // A modal dialog closes on Esc by itself; a dialog opened with show() does not.
  function handleKeyDown(event: KeyboardEvent<HTMLDialogElement>) {
    onKeyDown?.(event);
    if (event.key === 'Escape') {
      event.currentTarget.close();
    }
  }

  return (
    <dialog
      ref={ref}
      className={classNames('окно', className)}
      onKeyDown={handleKeyDown}
      {...rest}
    />
  );
}
