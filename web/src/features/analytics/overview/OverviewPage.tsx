import type { ReactNode } from 'react';

import { buildKpis, type KpiContext, type OverviewSample } from './kpis';
import { OverviewKpis } from './OverviewKpis';

export type OverviewPageProps = KpiContext & {
  // The page's sample and the same sample one period earlier (null for «Всё»).
  sample: OverviewSample;
  previous: OverviewSample | null;
  // «Полнота данных» under the KPI (HT-256).
  coverage?: ReactNode;
  children?: ReactNode;
};

// «Обзор» (README v5.1): six KPI, the data completeness line, then the panels.
export function OverviewPage({
  sample,
  previous,
  days,
  kind,
  whose,
  coverage,
  children,
}: OverviewPageProps) {
  return (
    <div className="overview">
      <OverviewKpis items={buildKpis(sample, previous, { days, kind, whose })} />
      {coverage}
      {children}
    </div>
  );
}
