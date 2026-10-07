import type { ReactNode } from 'react';

// The head of a page section (the reference .sec-h): a 15px title, a 12px note, an action on the right.
export function SectionHead({
  title,
  sub,
  action,
}: {
  title: ReactNode;
  sub?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="sec-h">
      <h2>{title}</h2>
      {sub && <span className="sub">{sub}</span>}
      {action && <span className="end">{action}</span>}
    </div>
  );
}
