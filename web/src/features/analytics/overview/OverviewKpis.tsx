import { CountUp, trendClass } from '../../../shared/ui';

import type { KpiItem } from './kpis';

// The KPI strip of «Обзор»: the caption stays always, the change to the previous period is a second
// line under it (shared Kpi shows only one of the two).
export function OverviewKpis({ items }: { items: readonly KpiItem[] }) {
  return (
    <div className="kpis">
      {items.map((item) => (
        <div key={item.key} className="kpi" title={item.title}>
          <span className="l">{item.label}</span>
          <span className="v num">
            <CountUp value={item.value} format={item.format} />
          </span>
          <span className="d">{item.sub}</span>
          {item.delta && <span className={`d ${trendClass(item.delta)}`}>{item.delta.text}</span>}
        </div>
      ))}
    </div>
  );
}
