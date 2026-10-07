import { useLayoutEffect, useRef, useState } from 'react';

import type { components } from '../../../shared/api';
import { fmtN, plural } from '../../../shared/lib/format';
import { prefersReducedMotion } from '../../../shared/lib/motion';
import { EventFeed, Segmented, type FeedEvent, type FeedTurn } from '../../../shared/ui';

import {
  clampFrom,
  jumpTarget,
  srcTarget,
  visibleEvents,
  WINDOW,
  windowAround,
  type TimelineMode,
} from './timelineWindow';

type AnalyticsTimeline = components['schemas']['AnalyticsTimeline'];
type AnalyticsEvent = components['schemas']['AnalyticsEvent'];
type AnalyticsTurn = components['schemas']['AnalyticsTurn'];

const STATES = new Set(['task_complete', 'turn_aborted']);

const toFeedEvent = (e: AnalyticsEvent): FeedEvent => ({ ...e, x: e.x ?? '' });

const toFeedTurn = (t: AnalyticsTurn): FeedTurn => ({
  turn: t.turn,
  start: t.start,
  dur_ms: t.dur_ms,
  state: STATES.has(t.state) ? (t.state as FeedTurn['state']) : 'open',
});

function subtitle(shown: number, from: number, visible: number, total: number): string {
  if (visible > WINDOW) {
    return `${fmtN(from + 1)}–${fmtN(from + shown)} из ${fmtN(visible)} · всего ${fmtN(total)} ${plural(total, 'событие', 'события', 'событий')}`;
  }
  return `${fmtN(visible)} из ${fmtN(total)} ${plural(total, 'события', 'событий', 'событий')}`;
}

export type TimelineProps = {
  // The feed from GET /api/analytics/sessions/{id}; undefined while it loads.
  timeline: AnalyticsTimeline | undefined;
  error?: Error | null;
  mode: TimelineMode;
  onModeChange: (mode: TimelineMode) => void;
  // ?line=: show every event and land on this one.
  line?: number;
  // ?src=: a line of the main transcript (Deep L<n>); show every event and land on the one built
  // from it or the nearest earlier line, else open from the start and say so.
  src?: number;
  // The session goes on now: «идёт сейчас», new events are appended below.
  live?: boolean;
};

/** Within this many pixels of the end the reader counts as following the feed. */
const BOTTOM_SLACK = 24;

function LiveBadge() {
  return (
    <span className="livebadge">
      <span className="pdot">
        {!prefersReducedMotion() && <i className="ring" />}
        <i />
      </span>
      идёт сейчас
    </span>
  );
}

// «Лента» of a session (reference renderTimeline): «Главное / Все события», a window of 1200 events
// and the jump to ?line=.
export function Timeline({
  timeline,
  error,
  mode,
  onModeChange,
  line,
  src,
  live = false,
}: TimelineProps) {
  const all = timeline?.events ?? [];
  // A jump, to ?line= or ?src=, is named by its key; it shows every event until the reader picks a
  // mode for that jump.
  const jump = line != null ? `line:${String(line)}` : src != null ? `src:${String(src)}` : null;
  const [pickedAt, setPickedAt] = useState<string | null>(null);
  const effective: TimelineMode = jump == null || pickedAt === jump ? mode : 'all';
  const pickMode = (next: TimelineMode) => {
    setPickedAt(jump);
    onModeChange(next);
  };
  const visible = visibleEvents(all, effective);
  const target =
    line != null ? jumpTarget(all, line) : src != null ? srcTarget(all, src) : undefined;
  const unlinked = line == null && src != null && timeline !== undefined && !target;
  const [from, setFrom] = useState(() =>
    target ? windowAround(visible.length, visible.indexOf(target)) : 0,
  );
  const [anchor, setAnchor] = useState({ jump, mode: effective, loaded: timeline !== undefined });
  // A new jump, a new mode or the feed's first answer re-seats the window.
  if (
    anchor.jump !== jump ||
    anchor.mode !== effective ||
    anchor.loaded !== (timeline !== undefined)
  ) {
    setAnchor({ jump, mode: effective, loaded: timeline !== undefined });
    setFrom(target ? windowAround(visible.length, visible.indexOf(target)) : 0);
  }
  const start = clampFrom(visible.length, from);
  const shown = visible.slice(start, start + WINDOW);
  const boxRef = useRef<HTMLDivElement>(null);

  // Appending: a reader at the end follows the feed; one who scrolled up gets «+N событий ↓».
  const [atBottom, setAtBottom] = useState(true);
  const [seen, setSeen] = useState(all.length);
  const [fresh, setFresh] = useState(0);
  const grownBy = seen > 0 && all.length > seen ? all.length - seen : 0;
  // Each append to a followed feed bumps follow; the layout effect then scrolls to the end.
  const [follow, setFollow] = useState(0);
  if (all.length !== seen) {
    setSeen(all.length);
    if (grownBy && atBottom) {
      // Following the feed moves the window to its end, so the appended events are drawn.
      setFrom(visible.length);
      setFollow(follow + 1);
    }
    if (grownBy && !atBottom) setFresh(fresh + grownBy);
  }

  useLayoutEffect(() => {
    const box = boxRef.current;
    if (box && follow > 0) box.scrollTop = box.scrollHeight;
  }, [follow]);

  function handleScroll() {
    const box = boxRef.current;
    if (!box) return;
    const bottom = box.scrollHeight - box.scrollTop - box.clientHeight < BOTTOM_SLACK;
    if (bottom !== atBottom) setAtBottom(bottom);
    if (bottom && fresh) setFresh(0);
  }

  function toBottom() {
    setFrom(visible.length);
    setFresh(0);
    setAtBottom(true);
    setFollow(follow + 1);
  }

  useLayoutEffect(() => {
    if (!target) return;
    const el = boxRef.current?.querySelector(`.ev[data-l="${String(target.line)}"]`);
    el?.scrollIntoView({ block: 'center' });
  }, [target]);

  let body;
  if (error) {
    body = <div className="empty">Лента недоступна: {error.message}</div>;
  } else if (!timeline) {
    body = <div className="empty">Загружаю ленту…</div>;
  } else {
    body = (
      <>
        {unlinked && (
          <div className="muted tl-unlinked">
            Строка L{String(src)} не связана с событием ленты — лента открыта с начала.
          </div>
        )}
        {start > 0 && (
          <div className="tl-shift">
            <button
              type="button"
              className="btn"
              onClick={() => {
                setFrom(start - WINDOW);
              }}
            >
              ← раньше · ещё {fmtN(start)}
            </button>
          </div>
        )}
        <EventFeed
          events={shown.map(toFeedEvent)}
          turns={timeline.turns.map(toFeedTurn)}
          highlightLine={target?.line}
        />
        {start + shown.length < visible.length && (
          <div className="tl-shift">
            <button
              type="button"
              className="btn"
              onClick={() => {
                setFrom(start + WINDOW);
              }}
            >
              дальше → · ещё {fmtN(visible.length - start - shown.length)}
            </button>
          </div>
        )}
      </>
    );
  }

  return (
    <section className="p tl-p">
      <div className="ph">
        <div className="tl-title">
          <h2>Лента</h2>
          {live && <LiveBadge />}
        </div>
        <div className="tl-ctl">
          {timeline && (
            <span className="sub">{subtitle(shown.length, start, visible.length, all.length)}</span>
          )}
          <Segmented
            label="Что показывать"
            value={effective}
            options={[
              { value: 'main', label: 'Главное' },
              { value: 'all', label: 'Все события' },
            ]}
            onChange={pickMode}
          />
        </div>
      </div>
      <div className="tl" ref={boxRef} onScroll={handleScroll}>
        {body}
        {fresh > 0 && (
          <button type="button" className="newev" onClick={toBottom}>
            +{fresh} {plural(fresh, 'событие', 'события', 'событий')} ↓
          </button>
        )}
      </div>
    </section>
  );
}
