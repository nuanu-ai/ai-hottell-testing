import { useId, type ReactNode } from 'react';

// Small v3 pieces the analytics panels are built from.

type MeterTone = 'warn' | 'bad';

function clamp(value: number): number {
  return Math.min(1, Math.max(0, value));
}

type MeterProps = {
  value: number;
  tone?: MeterTone;
  // The accessible name: a label text, or the id of the visible label next to the bar.
  label?: string;
  labelledBy?: string;
  // What a screen reader says for the value, as «2,0%»: without it, the raw fraction.
  valueText?: string;
};

// A share from 0 to 1 as a thin bar.
export function Meter({ value, tone, label, labelledBy, valueText }: MeterProps) {
  const share = clamp(value);
  return (
    <div
      className={tone ? `bar ${tone}` : 'bar'}
      role="meter"
      aria-label={label}
      aria-labelledby={labelledBy}
      aria-valuemin={0}
      aria-valuemax={1}
      aria-valuenow={share}
      aria-valuetext={valueText}
    >
      <i style={{ width: `${String(share * 100)}%` }} />
    </div>
  );
}

export type MeterItem = { label: ReactNode; value: number; text: ReactNode; tone?: MeterTone };

// «Name — bar — number» rows, as in «Куда уходят деньги».
export function MeterList({ items }: { items: readonly MeterItem[] }) {
  const id = useId();
  return (
    <div className="list">
      {items.map((item, index) => (
        <div className="li" key={index}>
          <span className="t" id={`${id}-${String(index)}`}>
            {item.label}
          </span>
          <Meter value={item.value} tone={item.tone} labelledBy={`${id}-${String(index)}`} />
          <span className="r num">{item.text}</span>
        </div>
      ))}
    </div>
  );
}

export function Legend({ items }: { items: readonly { label: ReactNode; color: string }[] }) {
  return (
    <div className="legend">
      {items.map((item, index) => (
        <span key={index}>
          <i style={{ background: item.color }} />
          {item.label}
        </span>
      ))}
    </div>
  );
}

export function SevDot({ tone }: { tone: 'bad' | 'warn' | 'info' }) {
  return <span className={`sev ${tone}`} aria-hidden="true" />;
}

// An uppercase mono caption, as «ГДЕ ВИДНО» in v3.
export function Label({ children }: { children: ReactNode }) {
  return <span className="lbl">{children}</span>;
}

// The «Полнота данных» strip.
export function Coverage({ children }: { children: ReactNode }) {
  return <div className="cov">{children}</div>;
}

export function More({ summary, children }: { summary: ReactNode; children: ReactNode }) {
  return (
    <details className="more">
      <summary>{summary}</summary>
      {children}
    </details>
  );
}
