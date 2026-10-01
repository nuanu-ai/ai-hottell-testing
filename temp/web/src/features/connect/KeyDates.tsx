import type { components } from '../../shared/api';
import { formatAgo, formatDateTime, fullMoment } from '../../shared/lib/time';

type KeyStatus = components['schemas']['KeyStatus'];

// A moment in one of deploy's formats; the full moment is in the title.
function Moment({ at, format }: { at: string; format: (moment: Date) => string }) {
  const moment = new Date(at);
  return (
    <time dateTime={at} title={fullMoment(moment)}>
      {format(moment)}
    </time>
  );
}

// When the active key was created and last used.
export function KeyDates({ status }: { status: KeyStatus }) {
  return (
    <div className="row faint" style={{ gap: 24, fontSize: 13 }}>
      <span>
        создан {status.createdAt ? <Moment at={status.createdAt} format={formatDateTime} /> : '—'}
      </span>
      {status.lastUsedAt ? (
        <span>
          последнее использование{' '}
          <Moment at={status.lastUsedAt} format={(moment) => formatAgo(moment)} />
        </span>
      ) : (
        <span>ещё не использовался</span>
      )}
    </div>
  );
}
