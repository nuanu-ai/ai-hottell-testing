import type { ReactNode } from 'react';

import { classNames } from './classNames';

export function Hint({ children }: { children: ReactNode }) {
  return <p className="hint">{children}</p>;
}

export function Row({ tight, children }: { tight?: boolean; children: ReactNode }) {
  return <div className={classNames('row', tight && 'tight')}>{children}</div>;
}
