import type { ReactNode } from 'react';

export type Trend = { change: number; goodWhen: 'up' | 'down'; text: ReactNode };

// The class of a trend, as v3's kpi(): under 2 % either way is flat.
export function trendClass({ change, goodWhen }: Pick<Trend, 'change' | 'goodWhen'>): string {
  if (Math.abs(change) < 2) return 'flat';
  const up = change > 0;
  const good = up === (goodWhen === 'up');
  return `${up ? 'up' : 'down'}-${good ? 'good' : 'bad'}`;
}
