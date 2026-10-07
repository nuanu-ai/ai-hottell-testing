// The value formats of the analytics screens (README v5.1 «Общие правила значений»),
// ported from the reference page's fmtN, fmt$, est$, fmtD, fmtOpen, pct, sec and short.
// An unknown value is written as «—», never as 0.

const NNBSP = ' '; // U+202F, narrow no-break space
const DASH = '—';

type Maybe = number | null | undefined;

const unknown = (value: Maybe): value is null | undefined => value == null || Number.isNaN(value);

/** Russian plural: 1 событие, 2 события, 5 событий, 11 событий, 21 событие. */
export function plural(n: number, one: string, few: string, many: string): string {
  const hundred = Math.abs(n) % 100;
  const ten = hundred % 10;
  if (hundred > 10 && hundred < 20) return many;
  if (ten > 1 && ten < 5) return few;
  if (ten === 1) return one;
  return many;
}

/** A whole number with thousands split by a narrow no-break space: 8 127. */
export function fmtN(value: Maybe): string {
  if (unknown(value)) return DASH;
  return Math.round(value).toLocaleString('ru-RU').replace(/\s/g, NNBSP);
}

/** Dollars: cents below $100, whole dollars from $100 on. */
export function fmtMoney(value: Maybe): string {
  if (unknown(value)) return DASH;
  return value >= 100 ? `$${fmtN(value)}` : `$${value.toFixed(2)}`;
}

/**
 * Dollars with their basis: «≈» marks an estimate by the API price list; a figure the agent
 * reported itself (Claude Code OTel, basis `otel_reported`) goes without it.
 */
export function estMoney(value: Maybe, basis: string | null | undefined): string {
  if (unknown(value)) return DASH;
  return (basis === 'otel_reported' ? '' : '≈ ') + fmtMoney(value);
}

/** Minutes as «43 м» or «2 ч 41 м». */
export function fmtDuration(minutes: Maybe): string {
  if (unknown(minutes)) return DASH;
  const whole = Math.round(minutes);
  return whole >= 60
    ? `${String(Math.floor(whole / 60))} ч ${String(whole % 60)} м`
    : `${String(whole)} м`;
}

/** How long a session stayed open: a day or more is «23 ч+». */
export function fmtOpen(minutes: Maybe): string {
  if (minutes != null && minutes >= 1440) return '23 ч+';
  return fmtDuration(minutes);
}

/** A share as a percent with one decimal and a comma: 0.391 → 39,1%. */
export function pct(value: Maybe): string {
  if (unknown(value) || !Number.isFinite(value)) return DASH;
  return `${(value * 100).toFixed(1).replace('.', ',')}%`;
}

/** Seconds: one decimal below 10, whole from 10 on. */
export function sec(value: Maybe): string {
  if (unknown(value)) return DASH;
  return value < 10 ? value.toFixed(1).replace('.', ',') : String(Math.round(value));
}

/** An id as its first 8 and last 4 signs; an id of up to 12 signs stays whole. */
export function shortId(id: string | null | undefined): string {
  const value = id ?? '';
  return value.length <= 12 ? value : `${value.slice(0, 8)}…${value.slice(-4)}`;
}
