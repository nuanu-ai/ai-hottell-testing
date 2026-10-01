import type { ButtonHTMLAttributes } from 'react';

import { classNames } from './classNames';

type ChipProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  on?: boolean;
  count?: number;
};

export function Chip({ on, count, type = 'button', className, children, ...rest }: ChipProps) {
  return (
    <button type={type} className={classNames('chip', on && 'on', className)} {...rest}>
      {children}
      {count !== undefined && (
        <>
          {' '}
          <span className="count">{count}</span>
        </>
      )}
    </button>
  );
}
