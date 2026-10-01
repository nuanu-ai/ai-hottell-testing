import type { HTMLAttributes, ReactNode } from 'react';

import { classNames } from './classNames';

export function Card({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return <div className={classNames('card', className)} {...rest} />;
}

export function PageTitle({ children }: { children: ReactNode }) {
  return <span className="крупно">{children}</span>;
}
