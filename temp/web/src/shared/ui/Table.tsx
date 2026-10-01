import type { MouseEvent, ReactNode, TdHTMLAttributes, ThHTMLAttributes } from 'react';

import { classNames } from './classNames';

export function Table({ children }: { children: ReactNode }) {
  return (
    <div className="table-wrap">
      <table className="table">{children}</table>
    </div>
  );
}

type ThProps = ThHTMLAttributes<HTMLTableCellElement> & { nowrap?: boolean };

export function Th({ nowrap, className, ...rest }: ThProps) {
  return <th className={classNames(nowrap && 'nowrap', className) || undefined} {...rest} />;
}

type TdProps = TdHTMLAttributes<HTMLTableCellElement> & {
  nowrap?: boolean;
  cellTitle?: boolean;
};

export function Td({ nowrap, cellTitle, className, ...rest }: TdProps) {
  return (
    <td
      className={classNames(nowrap && 'nowrap', cellTitle && 'cell-title', className) || undefined}
      {...rest}
    />
  );
}

type TableRowProps = {
  // The row leads where the link in its first cell leads. The link stays for the keyboard
  // and for opening in a new tab; the handler makes the whole row a target, as in deploy's shell.html.
  href?: string;
  onNavigate?: (href: string) => void;
  children: ReactNode;
};

function goTo(href: string) {
  window.location.assign(href);
}

export function TableRow({ href, onNavigate = goTo, children }: TableRowProps) {
  if (href === undefined) {
    return <tr>{children}</tr>;
  }

  function handleClick(event: MouseEvent<HTMLTableRowElement>) {
    if (!(event.target instanceof Element)) return;
    if (event.target.closest('a,button,input,label')) return;
    if (window.getSelection()?.toString()) return;
    if (href !== undefined) onNavigate(href);
  }

  return (
    <tr className="clickable" data-href={href} onClick={handleClick}>
      {children}
    </tr>
  );
}
