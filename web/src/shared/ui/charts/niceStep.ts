// A round grid step for an axis that tops out at max: about four lines, 1/2/5/10 × 10ⁿ.
// Same as niceStep in the v3 dashboard (HT-118).
export function niceStep(max: number): number {
  const raw = max / 4 || 1;
  const power = Math.pow(10, Math.floor(Math.log10(raw)));
  const f = raw / power;
  return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10) * power;
}

// Grid values from 0 to the first step at or above max.
export function gridValues(max: number): { step: number; top: number; values: number[] } {
  const step = niceStep(max);
  const top = Math.ceil(max / step) * step || step;
  const values: number[] = [];
  for (let i = 0; i * step <= top + 1e-9; i++) values.push(i * step);
  return { step, top, values };
}
