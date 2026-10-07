import type { ReactNode } from 'react';

import { type Trend, trendClass } from './trend';

// The numeric summaries of v3: the KPI strip, the session stat strip and the hero.

export function Kpis({ children }: { children: ReactNode }) {
  return <div className="kpis">{children}</div>;
}

type KpiProps = { label: ReactNode; value: ReactNode; trend?: Trend; note?: ReactNode };

export function Kpi({ label, value, trend, note }: KpiProps) {
  return (
    <div className="kpi">
      <span className="l">{label}</span>
      <span className="v num">{value}</span>
      {trend ? (
        <span className={`d ${trendClass(trend)}`}>
          {trend.change > 0 ? '▲' : '▼'} {trend.text}
        </span>
      ) : (
        note !== undefined && <span className="d flat">{note}</span>
      )}
    </div>
  );
}

export function Stats({ children }: { children: ReactNode }) {
  return <div className="stats">{children}</div>;
}

export function Stat({ label, value }: { label: ReactNode; value: ReactNode }) {
  return (
    <div className="st">
      <span className="l">{label}</span>
      <span className="v">{value}</span>
    </div>
  );
}

type HeroProps =
  | { title: ReactNode; items: readonly { value: ReactNode; label: ReactNode }[] }
  // The v5.1 hero of «Что исправить»: a title, one line under it and the actions on the right.
  | { title: ReactNode; sub: ReactNode; actions?: ReactNode; items?: undefined };

export function Hero(props: HeroProps) {
  const { title, items } = props;
  if (!items) {
    return (
      <div className="hero">
        <div className="hero-t">
          <h1>{title}</h1>
          <span className="hs">{props.sub}</span>
        </div>
        {props.actions && <div className="acts">{props.actions}</div>}
      </div>
    );
  }
  return (
    <div className="hero">
      <h1>{title}</h1>
      <div className="sum">
        {items.map((item, index) => (
          <div key={index}>
            <b>{item.value}</b>
            <span>{item.label}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
