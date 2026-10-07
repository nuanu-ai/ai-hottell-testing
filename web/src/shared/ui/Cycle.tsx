import { useEffect, useRef } from 'react';

import { fmtN } from '../lib/format';
import { EASE, prefersReducedMotion } from '../lib/motion';
import { CountUp } from './CountUp';

// The improvement cycle of «Что исправить» (README v5.1 «1. Цикл улучшений»; the reference markup
// index.html:L605): six steps in a row, an arrow after each but the last, one pressed at a time.
// The component knows no data: the screen gives it the steps and the hint under steps 4–6.

export type CycleStep = {
  key: string;
  label: string;
  value: number | null;
  /** Written small after the number, as « из 3». */
  suffix?: string;
  /** One line under the number; the full explanation goes to `title`. */
  sub: string;
  title: string;
  tone?: 'ok';
};

type Props = {
  steps: readonly CycleStep[];
  selected?: string | null;
  onSelect: (key: string) => void;
  hint?: string;
  /**
   * A silent update: when a step's number grew since the last render, a 6px dot runs from the
   * previous step to it (README v5.1 «Тихое обновление»; the reference cycleDot, L903–L912).
   */
  animateGrowth?: boolean;
};

const DOT_DURATION = 800;

function centre(rect: DOMRect) {
  return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
}

export function Cycle({ steps, selected, onSelect, hint, animateGrowth = false }: Props) {
  const box = useRef<HTMLDivElement>(null);
  const dot = useRef<HTMLElement>(null);
  const previous = useRef<ReadonlyMap<string, number | null> | null>(null);

  useEffect(() => {
    const before = previous.current;
    previous.current = new Map(steps.map((step) => [step.key, step.value]));
    if (!animateGrowth || !before || prefersReducedMotion()) return;
    const grew = steps.findIndex((step, index) => {
      const old = before.get(step.key);
      return index > 0 && step.value !== null && old != null && step.value > old;
    });
    const buttons = box.current?.querySelectorAll<HTMLElement>('.cstep');
    const from = buttons?.[grew - 1];
    const to = buttons?.[grew];
    if (grew < 1 || !box.current || !from || !to || typeof dot.current?.animate !== 'function') {
      return;
    }
    const frame = box.current.getBoundingClientRect();
    const start = centre(from.getBoundingClientRect());
    const end = centre(to.getBoundingClientRect());
    const x0 = start.x - frame.left;
    const x1 = end.x - frame.left;
    const y = end.y - frame.top - frame.height / 2;
    const at = (x: number) => `translate(${String(x)}px,${String(y)}px)`;
    dot.current.animate(
      [
        { transform: at(x0), opacity: 1 },
        { transform: at(x1), opacity: 1 },
        { transform: at(x1), opacity: 0 },
      ],
      { duration: DOT_DURATION, easing: EASE },
    );
  }, [steps, animateGrowth]);

  return (
    <div className="cycle" ref={box}>
      {steps.map((step, index) => (
        <button
          key={step.key}
          type="button"
          className="cstep"
          aria-pressed={step.key === selected}
          title={step.title}
          onClick={() => {
            onSelect(step.key);
          }}
        >
          <span className="cl">
            <span>{step.label}</span>
            <b>{index < steps.length - 1 ? '→' : ''}</b>
          </span>
          <span className={step.tone === 'ok' ? 'cn ok' : 'cn'}>
            <CountUp value={step.value} format={fmtN} />
            {step.suffix && <small>{step.suffix}</small>}
          </span>
          <span className="cs">{step.sub}</span>
        </button>
      ))}
      {hint && <div className="chint">{hint}</div>}
      <i className="cdot" ref={dot} />
    </div>
  );
}
