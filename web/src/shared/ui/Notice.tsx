import type { HTMLAttributes, ReactNode } from 'react';

type NoticeProps = Omit<HTMLAttributes<HTMLDivElement>, 'className'> & {
  tone: 'err' | 'ok' | 'info' | 'warn';
  children: ReactNode;
};

const toneClass = { err: 'bad', ok: 'ok', info: 'acc', warn: 'warn' } as const;

export function Notice({ tone, children, ...rest }: NoticeProps) {
  return (
    <div
      className={`notice ${toneClass[tone]}`}
      role={tone === 'err' ? 'alert' : undefined}
      {...rest}
    >
      {children}
    </div>
  );
}
