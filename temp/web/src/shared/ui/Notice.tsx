import type { HTMLAttributes, ReactNode } from 'react';

type NoticeProps = Omit<HTMLAttributes<HTMLDivElement>, 'className'> & {
  tone: 'err' | 'ok' | 'info' | 'warn';
  children: ReactNode;
};

export function Notice({ tone, children, ...rest }: NoticeProps) {
  return (
    <div className={`notice notice-${tone}`} role={tone === 'err' ? 'alert' : undefined} {...rest}>
      {children}
    </div>
  );
}
