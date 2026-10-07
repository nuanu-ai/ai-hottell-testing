import type { ReactNode } from 'react';

export function Loading({ children = 'Загрузка…' }: { children?: ReactNode }) {
  return (
    <div className="state" role="status">
      {children}
    </div>
  );
}
