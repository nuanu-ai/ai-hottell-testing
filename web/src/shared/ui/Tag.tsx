import type { ReactNode } from 'react';

export type TagTone = 'ok' | 'warn' | 'bad' | 'acc' | 'plain' | 'claude' | 'codex';

type TagProps = {
  tone: TagTone;
  title?: string;
  children: ReactNode;
};

export function Tag({ tone, title, children }: TagProps) {
  return (
    <span className={tone === 'plain' ? 'tag' : `tag ${tone}`} title={title}>
      {children}
    </span>
  );
}
