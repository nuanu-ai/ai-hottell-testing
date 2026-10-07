import { gridValues } from './niceStep';

const W = 320;
const H = 130;
const L = 38;
const B = 18;
const T = 8;
const R = 6;

type AreaLineProps = {
  points: readonly (readonly [number, number])[];
  format: (value: number) => string;
  formatX: (x: number) => string;
  marks?: readonly number[];
  ariaLabel: string;
};

// A filled line that grows along x, as line() in the v3 dashboard.
export function AreaLine({ points, format, formatX, marks = [], ariaLabel }: AreaLineProps) {
  const last = points.at(-1);
  const first = points[0];
  if (points.length < 2 || !last || !first) {
    return <div className="empty">Нет данных</div>;
  }
  const maxY = Math.max(0, ...points.map((p) => p[1])) || 1;
  const maxX = Math.max(1, ...points.map((p) => p[0]));
  const { top, values } = gridValues(maxY);
  const x = (v: number) => L + ((W - L - R) * v) / maxX;
  const y = (v: number) => T + (H - T - B) * (1 - v / top);
  const line = points
    .map((p, i) => `${i ? 'L' : 'M'}${x(p[0]).toFixed(1)} ${y(p[1]).toFixed(1)}`)
    .join(' ');
  const area = `${line} L${x(last[0]).toFixed(1)} ${String(y(0))} L${x(first[0]).toFixed(1)} ${String(y(0))} Z`;

  return (
    <div className="chart">
      <svg viewBox={`0 0 ${String(W)} ${String(H)}`} role="img" aria-label={ariaLabel}>
        <g className="grid">
          {values.map((v) => (
            <g key={v}>
              <line x1={L} x2={W - R} y1={y(v)} y2={y(v)} />
              <text x={L - 5} y={y(v) + 3} textAnchor="end">
                {format(v)}
              </text>
            </g>
          ))}
        </g>
        <path d={area} fill="var(--accent-soft)" />
        {marks.map((m) => (
          <line
            key={m}
            x1={x(m)}
            x2={x(m)}
            y1={T}
            y2={y(0)}
            stroke="var(--warn)"
            strokeDasharray="3 3"
          />
        ))}
        <path d={line} fill="none" stroke="var(--accent)" strokeWidth={2} />
        <circle cx={x(last[0])} cy={y(last[1])} r={3.5} fill="var(--accent)" />
        <text x={L} y={H - 3}>
          0
        </text>
        <text x={W - R} y={H - 3} textAnchor="end">
          {formatX(maxX)}
        </text>
      </svg>
    </div>
  );
}
