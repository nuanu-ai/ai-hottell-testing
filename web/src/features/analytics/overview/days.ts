import type { components } from '../../../shared/api';
import { plural } from '../../../shared/lib/format';
import { localDaySpan, localDaysTo } from '../../../shared/lib/time';

type Session = components['schemas']['AnalyticsSession'];

/**
 * The browser's days of the period, oldest first (reference periodDays): `days` days up to the
 * day of `end`; for «Всё» (0) from the day of the earliest session on.
 */
export function periodDays(sessions: readonly Session[], days: number, end: string): string[] {
  const e = Date.parse(end);
  let n = days;
  if (!n) {
    const starts = sessions.map((s) => Date.parse(s.start)).filter((t) => !Number.isNaN(t));
    n = Math.max(1, localDaySpan(Math.min(...starts, e), e));
  }
  return localDaysTo(e, n);
}

/** «за 7 дней» / «за всё время». */
export function periodText(days: number): string {
  return days ? `за ${String(days)} ${plural(days, 'день', 'дня', 'дней')}` : 'за всё время';
}

/** DD.MM of a YYYY-MM-DD day. */
export const ddmm = (day: string) => `${day.slice(8, 10)}.${day.slice(5, 7)}`;
