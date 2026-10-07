import type { components } from '../../shared/api';
import { formatAgo, formatDateTime } from '../../shared/lib/time';
import { Actions, Moment } from '../../shared/ui';

type KeyStatus = components['schemas']['KeyStatus'];

// A moment in one of deploy's formats; the full moment is in the title.

// When the active key was created and last used.
export function KeyDates({ status }: { status: KeyStatus }) {
  return (
    <Actions className="muted">
      <span>
        создан{' '}
        {status.createdAt ? (
          <Moment at={status.createdAt} format={formatDateTime} mono={false} />
        ) : (
          '—'
        )}
      </span>
      {status.lastUsedAt ? (
        <span>
          последнее использование{' '}
          <Moment at={status.lastUsedAt} format={(moment) => formatAgo(moment)} mono={false} />
        </span>
      ) : (
        <span>ещё не использовался</span>
      )}
    </Actions>
  );
}
