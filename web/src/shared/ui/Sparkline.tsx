import { fmtN, plural } from '../lib/format';

const WIDTH = 120;
const HEIGHT = 24;
// The stroke stays inside the box: the line's top and bottom are this far from the edges.
const PAD = 1.5;

type SparklineProps = {
  /** The counts, oldest first; the line is scaled to their maximum. */
  values: readonly number[];
  /** The span the values cover, for the accessible name. */
  span?: string;
};

function sparklineLabel(max: number, span: string) {
  if (max <= 0) return `Активность за ${span}: событий нет`;
  return `Активность за ${span}: максимум ${fmtN(max)} ${plural(max, 'событие', 'события', 'событий')} в час`;
}

// A row's activity as a line, like the sparkline of a repository in GitHub's list (HT-540): no
// axes and no labels, a fixed width, the height of a table row, scaled to the row's own maximum;
// all zeros are a flat line at the bottom.
export function Sparkline({ values, span = '5 дней' }: SparklineProps) {
  const max = values.reduce((m, v) => Math.max(m, v), 0);
  const step = values.length > 1 ? (WIDTH - 2 * PAD) / (values.length - 1) : 0;
  const points = values
    .map((v, i) => {
      const x = PAD + i * step;
      const y = HEIGHT - PAD - (max > 0 ? (v / max) * (HEIGHT - 2 * PAD) : 0);
      return `${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(' ');
  return (
    <svg
      className="spark"
      width={WIDTH}
      height={HEIGHT}
      viewBox={`0 0 ${String(WIDTH)} ${String(HEIGHT)}`}
      role="img"
      aria-label={sparklineLabel(max, span)}
    >
      <polyline points={points} />
    </svg>
  );
}
