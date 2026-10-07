import { useId, useState, type ReactNode } from 'react';

import { Button } from '../../shared/ui';
import type { Change, Scope } from './model';

const scopeTitles: Record<Scope, string> = {
  claude: 'Claude Code',
  codex: 'Codex',
  shared: 'Папки и история',
};

// «Claude Code 2, Папки и история 1»: the changes by tab, in the page's order.
function byScope(changes: Change[]): string {
  return (Object.keys(scopeTitles) as Scope[])
    .map((scope) => [scope, changes.filter((change) => change.scope === scope).length] as const)
    .filter(([, count]) => count > 0)
    .map(([scope, count]) => `${scopeTitles[scope]} ${String(count)}`)
    .join(', ');
}

type SaveBarProps = {
  changes: Change[];
  busy: boolean;
  onCancel: () => void;
  // The save's warnings and errors, shown above the buttons.
  children?: ReactNode;
};

// The one save of the page, stuck to the bottom of the screen while the draft differs from
// the settings read: it saves every tab at once, since the API takes the whole document.
export function SaveBar({ changes, busy, onCancel, children }: SaveBarProps) {
  const listId = useId();
  const [open, setOpen] = useState(false);

  if (changes.length === 0) {
    return null;
  }

  return (
    <section className="save-bar" aria-label="Несохранённые изменения">
      {children}
      <div className="save-bar-row">
        <span>
          Не сохранено: <span className="num">{changes.length}</span> ({byScope(changes)})
        </span>
        <Button
          variant="link"
          size="sm"
          aria-expanded={open}
          aria-controls={listId}
          onClick={() => {
            setOpen(!open);
          }}
        >
          {open ? 'Скрыть' : 'Показать'}
        </Button>
        <span className="save-bar-gap" />
        <Button disabled={busy} onClick={onCancel}>
          Отменить изменения
        </Button>
        <Button variant="primary" type="submit" disabled={busy}>
          Сохранить
        </Button>
      </div>
      {open && (
        <ul id={listId} className="save-bar-list">
          {changes.map((change) => (
            <li key={change.text}>{change.text}</li>
          ))}
        </ul>
      )}
    </section>
  );
}
