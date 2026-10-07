import type { components } from '../../../shared/api';

type AnalyticsEvent = components['schemas']['AnalyticsEvent'];

export type TimelineMode = 'main' | 'all';

/** How many feed events are drawn at once (reference TLWIN). */
export const WINDOW = 1200;

/** «Главное» of README v5.1: everything but tool calls and model requests. */
const MAIN = new Set<AnalyticsEvent['k']>([
  'prompt',
  'answer',
  'err',
  'wait',
  'compact',
  'abort',
  'skill',
  'agent',
]);

export function visibleEvents(
  events: readonly AnalyticsEvent[],
  mode: TimelineMode,
): readonly AnalyticsEvent[] {
  return mode === 'all' ? events : events.filter((e) => MAIN.has(e.k));
}

/** The event a jump to `line` lands on: the last one at or before it, else the first. */
export function jumpTarget(
  events: readonly AnalyticsEvent[],
  line: number,
): AnalyticsEvent | undefined {
  let best: AnalyticsEvent | undefined;
  for (const e of events) if (e.line <= line && (!best || e.line > best.line)) best = e;
  return best ?? events[0];
}

/**
 * The event a jump to the transcript line `src` (a Deep L<n>) lands on: the one with the greatest
 * main-file source line at or before it, the first of a tie; undefined when none (HT-410).
 */
export function srcTarget(
  events: readonly AnalyticsEvent[],
  src: number,
): AnalyticsEvent | undefined {
  let best: AnalyticsEvent | undefined;
  for (const e of events) {
    if (e.src_kind !== 'main' || e.src_line == null || e.src_line > src) continue;
    if (!best || e.src_line > (best.src_line ?? 0)) best = e;
  }
  return best;
}

/** The window start that shows the target with 200 events of context above it. */
export function windowAround(total: number, index: number): number {
  if (total <= WINDOW || index < 0) return 0;
  return Math.max(0, Math.min(index - 200, total - WINDOW));
}

export function clampFrom(total: number, from: number): number {
  return total <= WINDOW ? 0 : Math.max(0, Math.min(from, total - WINDOW));
}
