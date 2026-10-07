import type { ReactNode } from 'react';

// The v5.1 page head: a panel with a 22px title, a 13.5px line under it and actions on the right
// (ai-hottell@c3c8357 .skh and .hero .acts); the actions move under the title below 900px.
export function PageHead({
  title,
  sub,
  action,
}: {
  title: ReactNode;
  sub?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <header className="page-head">
      <div className="page-head-t">
        <h1>{title}</h1>
        {sub && <span className="sub">{sub}</span>}
      </div>
      {action && <div className="acts">{action}</div>}
    </header>
  );
}
