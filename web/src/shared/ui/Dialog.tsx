import { useId } from 'react';
import type { DialogHTMLAttributes, KeyboardEvent, ReactNode, Ref } from 'react';

import { classNames } from './classNames';

type DialogProps = Omit<DialogHTMLAttributes<HTMLDialogElement>, 'title'> & {
  // The caller opens and closes the dialog through this ref: showModal() and close().
  ref: Ref<HTMLDialogElement>;
  // A visible head as the panel's .ph; it also names the dialog.
  title?: ReactNode;
};

export function Dialog({ ref, className, onKeyDown, title, children, ...rest }: DialogProps) {
  const titleId = useId();

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
      className={classNames('dialog', className)}
      onKeyDown={handleKeyDown}
      {...(title ? { 'aria-labelledby': titleId } : {})}
      {...rest}
    >
      {title && (
        <div className="ph">
          <h2 id={titleId}>{title}</h2>
        </div>
      )}
      {children}
    </dialog>
  );
}
