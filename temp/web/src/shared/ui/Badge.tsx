import type { ReactNode } from 'react';

type BadgeProps = {
  tone: 'ok' | 'warn' | 'err' | 'quiet';
  title?: string;
  children: ReactNode;
};

export function Badge({ tone, title, children }: BadgeProps) {
  return (
    <span className={`badge badge-${tone}`} title={title}>
      {children}
    </span>
  );
}
