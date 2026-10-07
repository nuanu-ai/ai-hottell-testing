import type { HTMLAttributes, ReactNode } from 'react';

import { classNames } from './classNames';

type PanelProps = Omit<HTMLAttributes<HTMLElement>, 'title'> & {
  title?: ReactNode;
  sub?: ReactNode;
  action?: ReactNode;
  span?: 4 | 5 | 6 | 7 | 8 | 12;
};

// The v3 panel .p with its head .ph: a title, a quiet note and an action on the right.
export function Panel({ title, sub, action, span, className, children, ...rest }: PanelProps) {
  return (
    <section className={classNames('p', span && `s${String(span)}`, className)} {...rest}>
      {title !== undefined && (
        <div className="ph">
          <h2>{title}</h2>
          {sub !== undefined && <span className="sub">{sub}</span>}
          {action}
        </div>
      )}
      {children}
    </section>
  );
}
