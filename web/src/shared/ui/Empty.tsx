import type { ReactNode } from 'react';

type EmptyProps = {
  title: string;
  children?: ReactNode;
};

export function Empty({ title, children }: EmptyProps) {
  return (
    <div className="empty">
      <h3>{title}</h3>
      {children}
    </div>
  );
}
