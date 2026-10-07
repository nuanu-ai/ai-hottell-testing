import { formatClock, formatDateTime } from '../../lib/time';

// The session timeline of «Skills» (CONTRACT.md «Skills.gantt»).
export type GanttData = {
  from: string;
  to: string;
  rows: readonly {
    name: string;
    segs: readonly { from: string; to: string; c: string; label?: string | null }[];
  }[];
  marks: readonly { at: string; text?: string | null; line?: number | null }[];
};

const COLORS: Record<string, string> = {
  skill: 'var(--m2)',
  review: 'var(--m4)',
  subagent: 'var(--m3)',
  noskill: 'var(--ok)',
  mcp: 'var(--m1)',
};
const STEPS_MIN = [5, 10, 15, 30, 60, 120, 180, 360, 720, 1440];
const MINUTE = 60_000;
const DAY = 1440 * MINUTE;

const clock = (ms: number) => formatClock(new Date(ms));
const dayMonth = (ms: number) => formatDateTime(new Date(ms)).slice(0, 5);

type GanttProps = {
  data: GanttData;
  /** Opens the session feed at the line of a prompt mark. */
  onMark: (line: number) => void;
};

// README v5.1 «Skills → Хронология сессии» (ganttHTML in the reference): 28px rows with 150px mono names,
// 18px segments coloured by kind, your prompts as 2px marks, a local time scale of at most 12 steps.
// On a narrow screen the diagram scrolls inside its .tbl.
export function Gantt({ data, onMark }: GanttProps) {
  const from = Date.parse(data.from);
  const to = Date.parse(data.to);
  const span = Math.max(to - from, MINUTE);
  const x = (ms: number) => `${(((ms - from) / span) * 100).toFixed(2)}%`;
  // Past a day the step grows by whole days, so a long session still gets at most 12 intervals.
  const step =
    (STEPS_MIN.find((m) => span / (m * MINUTE) <= 12) ?? Math.ceil(span / (12 * DAY)) * 1440) *
    MINUTE;
  const ticks: number[] = [];
  for (let t = Math.ceil(from / step) * step; t <= to; t += step) ticks.push(t);
  const tickLabel = step >= 1440 * MINUTE ? dayMonth : clock;

  return (
    <div className="tbl">
      <div className="gantt">
        <div className="names">
          {data.rows.map((row, i) => (
            <span key={i} title={row.name}>
              {row.name === 'skill' ? 'analytics/skill' : row.name}
            </span>
          ))}
        </div>
        <div className="area">
          {ticks.map((t) => (
            <div key={t}>
              <div className="tk" style={{ left: x(t) }} />
              <span className="tl" style={{ left: x(t) }}>
                {tickLabel(t)}
              </span>
            </div>
          ))}
          {data.rows.map((row, i) => (
            <div className="gr" key={i}>
              {row.segs.map((seg, k) => {
                const a = Date.parse(seg.from);
                const b = Date.parse(seg.to);
                return (
                  <i
                    key={k}
                    title={`${row.name}: ${seg.label ?? ''} · ${clock(a)}–${clock(b)}`}
                    style={{
                      left: x(a),
                      width: `${Math.max(((b - a) / span) * 100, 0.3).toFixed(2)}%`,
                      background: COLORS[seg.c] ?? 'var(--ink-3)',
                    }}
                  />
                );
              })}
            </div>
          ))}
          {data.marks.map((mark, i) => {
            const label = `${clock(Date.parse(mark.at))} ${mark.text ?? ''}`.trim();
            const line = mark.line;
            return (
              <button
                key={i}
                type="button"
                className="mk"
                style={{ left: x(Date.parse(mark.at)) }}
                title={label}
                aria-label={label}
                disabled={line == null}
                onClick={() => {
                  if (line != null) onMark(line);
                }}
              />
            );
          })}
        </div>
      </div>
    </div>
  );
}
