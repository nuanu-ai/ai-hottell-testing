import { gridValues } from './niceStep';

const W = 640;
const H = 200;
const L = 44;
const B = 24;
const T = 10;
const R = 6;
const SERIES_COLORS = ['var(--m1)', 'var(--m2)', 'var(--m3)', 'var(--m4)'];

type StackedBarsProps = {
  labels: readonly string[];
  series: readonly { name: string; values: readonly number[] }[];
  colors?: readonly string[];
  format: (value: number) => string;
  ariaLabel: string;
  // grouped: the series stand side by side in a slot, as «Кто работал» in v5.1.
  layout?: 'stacked' | 'grouped';
  // max: no grid, only the maximum and zero on the axis, as «Расходы по дням» in v5.1.
  axis?: 'grid' | 'max';
  // One tooltip for the whole slot in place of one per column.
  slotTitle?: (index: number) => string;
};

// Columns per x label, stacked as barsStacked in the v3 dashboard or grouped as in v5.1.
export function StackedBars({
  labels,
  series,
  colors = SERIES_COLORS,
  format,
  ariaLabel,
  layout = 'stacked',
  axis = 'grid',
  slotTitle,
}: StackedBarsProps) {
  const grouped = layout === 'grouped';
  const heights = labels.map((_, i) => {
    const values = series.map((s) => s.values[i] ?? 0);
    return grouped ? Math.max(0, ...values) : values.reduce((sum, v) => sum + v, 0);
  });
  const max = Math.max(0, ...heights);
  if (!max) {
    return <div className="empty">За период нет данных</div>;
  }
  const { top, values } = axis === 'max' ? { top: max, values: [max, 0] } : gridValues(max);
  const y = (v: number) => T + (H - T - B) * (1 - v / top);
  const slot = (W - L - R) / labels.length;
  const every = Math.max(1, Math.ceil(labels.length / 12));
  const barWidth = grouped ? (slot * 0.64) / series.length : slot * 0.64;

  return (
    <div className="chart">
      <svg viewBox={`0 0 ${String(W)} ${String(H)}`} role="img" aria-label={ariaLabel}>
        <g className="grid">
          {values.map((v) => (
            <g key={v}>
              {axis === 'grid' && <line x1={L} x2={W - R} y1={y(v)} y2={y(v)} />}
              <text x={L - 6} y={y(v) + 3} textAnchor="end">
                {format(v)}
              </text>
            </g>
          ))}
        </g>
        {labels.map((label, i) => {
          let acc = 0;
          return (
            <g key={label}>
              {slotTitle && <title>{slotTitle(i)}</title>}
              {series.map((s, k) => {
                const v = s.values[i] ?? 0;
                if (v <= 0) return null;
                const base = grouped ? 0 : acc;
                const rect = (
                  <rect
                    key={s.name}
                    x={L + i * slot + slot * 0.18 + (grouped ? k * barWidth : 0)}
                    y={y(base + v)}
                    width={barWidth}
                    height={Math.max(0.5, y(base) - y(base + v))}
                    fill={colors[k % colors.length]}
                  >
                    {!slotTitle && <title>{`${label} · ${s.name}: ${format(v)}`}</title>}
                  </rect>
                );
                acc += v;
                return rect;
              })}
              {i % every === 0 && (
                <text x={L + i * slot + slot / 2} y={H - 8} textAnchor="middle">
                  {label}
                </text>
              )}
            </g>
          );
        })}
      </svg>
    </div>
  );
}
