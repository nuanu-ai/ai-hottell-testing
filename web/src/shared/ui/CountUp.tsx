import { useEffect, useState } from 'react';

import { canAnimate } from '../lib/motion';

// A number that counts from its old value to the new one (README v5.1 «Тихое обновление»; the
// reference num() and afterRender, index.html:L573–L574 and L895–L901): ease-out cubic over 600 ms,
// whole numbers stay whole on the way. The first render, null, reduced motion and a hidden tab show
// the value at once.

const DURATION = 600;

type Run = { from: number; to: number };

type Props = { value: number | null; format: (value: number) => string };

function known(value: number | null): value is number {
  return value !== null && !Number.isNaN(value);
}

export function CountUp({ value, format }: Props) {
  const [target, setTarget] = useState(value);
  const [shown, setShown] = useState(value);
  const [run, setRun] = useState<Run | null>(null);

  // The change is noticed during render, so the old value stays on screen until the count starts.
  if (!Object.is(value, target)) {
    setTarget(value);
    if (known(target) && known(value) && canAnimate()) {
      setRun({ from: target, to: value });
      setShown(target);
    } else {
      setRun(null);
      setShown(value);
    }
  }

  useEffect(() => {
    if (!run) return;
    const { from, to } = run;
    const whole = Number.isInteger(from) && Number.isInteger(to);
    const start = performance.now();
    let frame = 0;
    const step = (now: number) => {
      const progress = Math.min(1, (now - start) / DURATION);
      const eased = 1 - Math.pow(1 - progress, 3);
      const current = from + (to - from) * eased;
      setShown(whole && progress < 1 ? Math.round(current) : progress < 1 ? current : to);
      if (progress < 1) frame = requestAnimationFrame(step);
    };
    frame = requestAnimationFrame(step);
    return () => {
      cancelAnimationFrame(frame);
    };
  }, [run]);

  return <span className="cnt num">{known(shown) ? format(shown) : '—'}</span>;
}
