import type {
  KeyboardEvent,
  MouseEvent,
  ReactNode,
  TdHTMLAttributes,
  ThHTMLAttributes,
} from 'react';

import { classNames } from './classNames';

// The v3 table: cells keep one line unless asked to wrap, numbers align right.
export function Table({ children }: { children: ReactNode }) {
  return (
    <div className="tbl">
      <table>{children}</table>
    </div>
  );
}

type ThProps = ThHTMLAttributes<HTMLTableCellElement> & { wrap?: boolean; numeric?: boolean };

export function Th({ wrap, numeric, className, ...rest }: ThProps) {
  return (
    <th className={classNames(wrap && 'wrap', numeric && 'r', className) || undefined} {...rest} />
  );
}

type TdProps = TdHTMLAttributes<HTMLTableCellElement> & {
  wrap?: boolean;
  numeric?: boolean;
  cellTitle?: boolean;
  // The row's buttons: a narrow cell on the right, the buttons in one row with an 8px gap.
  actions?: boolean;
};

export function Td({ wrap, numeric, cellTitle, actions, className, children, ...rest }: TdProps) {
  return (
    <td
      className={
        classNames(
          wrap && 'wrap',
          numeric && 'r',
          cellTitle && 't',
          actions && 'acts-cell',
          className,
        ) || undefined
      }
      {...rest}
    >
      {actions ? <div className="acts end">{children}</div> : children}
    </td>
  );
}

type TableRowProps = {
  // The row leads where the link in its first cell leads. The link stays for opening in a new
  // tab; the handlers make the whole row a target, by mouse and by Enter.
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
  const target = href;

  function handleClick(event: MouseEvent<HTMLTableRowElement>) {
    if (!(event.target instanceof Element)) return;
    if (event.target.closest('a,button,input,label')) return;
    if (window.getSelection()?.toString()) return;
    onNavigate(target);
  }

  function handleKeyDown(event: KeyboardEvent<HTMLTableRowElement>) {
    if (event.key === 'Enter' && event.target === event.currentTarget) onNavigate(target);
  }

  return (
    <tr
      className="row"
      tabIndex={0}
      data-href={href}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
    >
      {children}
    </tr>
  );
}
