import { Fragment } from 'react';

import { fmtDuration } from '../lib/format';
import { formatClock, formatWhen, utcTitle } from '../lib/time';
import { classNames } from './classNames';
import { Empty } from './Empty';

// An event of the session feed (CONTRACT.md «sessions/<id>.json» events[]).
export type FeedEvent = {
  line: number;
  at: string;
  turn: number | null;
  k: string;
  x: string;
  note?: string | null;
  side?: string | null;
};

// A turn of the session feed (CONTRACT.md «sessions/<id>.json» turns[]).
export type FeedTurn = {
  turn: number;
  start: string;
  dur_ms: number | null;
  state: 'task_complete' | 'turn_aborted' | 'open';
};

const STATE = { task_complete: 'завершён', turn_aborted: 'прерван', open: 'не завершён' } as const;

// One feed row (README v5.1 «Лента сессии»): local time · kind icon · text and note · side figure.
export function EventRow({ event, highlight = false }: { event: FeedEvent; highlight?: boolean }) {
  const at = new Date(event.at);
  return (
    <div
      className={classNames('ev', event.k, highlight && 'hl')}
      data-l={event.line}
      title={`событие #${String(event.line)}`}
    >
      <span className="t" title={utcTitle(at)}>
        {formatClock(at)}
      </span>
      <span className="ic" />
      <div className="body">
        <span className="x">{event.k === 'prompt' ? `«${event.x}»` : event.x}</span>
        {event.note && (
          <span className={classNames('note', (event.k === 'err' || event.k === 'abort') && 'bad')}>
            {event.note}
          </span>
        )}
      </div>
      <span className="side">{event.side ?? ''}</span>
    </div>
  );
}

// The header over a turn's events: «ХОД 2 · 05:50» on the left, «6 м · завершён» on the right.
export function TurnHeader({ turn, info }: { turn: number; info?: FeedTurn }) {
  const start = info ? formatWhen(info.start) : null;
  return (
    <div className="turn-h">
      <span>
        {`Ход ${String(turn + 1)}`}
        {start && (
          <>
            {' · '}
            <span title={start.title}>{start.text}</span>
          </>
        )}
      </span>
      {info && (
        <span>{`${fmtDuration(info.dur_ms == null ? null : info.dur_ms / 60_000)} · ${STATE[info.state]}`}</span>
      )}
    </div>
  );
}

type EventFeedProps = {
  events: readonly FeedEvent[];
  turns: readonly FeedTurn[];
  /** The line to mark, as a jump from evidence (`?line=`). */
  highlightLine?: number;
};

// Which events open a turn: the first event of each run of one turn, events with no turn left out.
function turnStarts(events: readonly FeedEvent[]): Set<FeedEvent> {
  const starts = new Set<FeedEvent>();
  let current: number | null = null;
  for (const event of events) {
    if (event.turn != null && event.turn !== current) {
      current = event.turn;
      starts.add(event);
    }
  }
  return starts;
}

// The event list with a TurnHeader wherever the turn changes. Windowing and scrolling are the screen's.
export function EventFeed({ events, turns, highlightLine }: EventFeedProps) {
  if (!events.length) return <Empty title="Нет событий" />;
  const byTurn = new Map(turns.map((t) => [t.turn, t]));
  const starts = turnStarts(events);
  return (
    <div className="feed">
      {events.map((event) => (
        <Fragment key={event.line}>
          {starts.has(event) && event.turn != null && (
            <TurnHeader turn={event.turn} info={byTurn.get(event.turn)} />
          )}
          <EventRow event={event} highlight={event.line === highlightLine} />
        </Fragment>
      ))}
    </div>
  );
}
