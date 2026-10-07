import type { ReactNode } from 'react';

import { classNames } from './classNames';

export function Hint({ children }: { children: ReactNode }) {
  return <p className="hint">{children}</p>;
}

// The page column: blocks one under another with the v3 16px step.
export function View({ children }: { children: ReactNode }) {
  return <div className="view">{children}</div>;
}

// The v3 12-column grid; a Panel takes its width through span.
export function Grid({ children }: { children: ReactNode }) {
  return <div className="g">{children}</div>;
}

type StackProps = { gap?: 4 | 8 | 12 | 16; className?: string; children: ReactNode };

// Children one under another; replaces inline display:grid with a gap.
export function Stack({ gap = 12, className, children }: StackProps) {
  return (
    <div className={classNames('stack', gap !== 12 && `g${String(gap)}`, className)}>
      {children}
    </div>
  );
}

type ActionsProps = {
  justify?: 'between' | 'end';
  align?: 'end';
  className?: string;
  children: ReactNode;
};

// A row of buttons and short controls, wrapping on a narrow screen; align="end" puts
// a field and a button of the same height level on their bottom edge.
export function Actions({ justify, align, className, children }: ActionsProps) {
  return (
    <div className={classNames('acts', justify, align === 'end' && 'items-end', className)}>
      {children}
    </div>
  );
}
