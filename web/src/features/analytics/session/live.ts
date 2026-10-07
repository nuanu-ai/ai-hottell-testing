import type { components } from '../../../shared/api';

type AnalyticsTimeline = components['schemas']['AnalyticsTimeline'];
type AnalyticsPulse = components['schemas']['AnalyticsPulse'];

/** A session is live while its last event is younger than this (README v5.1). */
const LIVE_FOR_MS = 120_000;

/** How often the feed of a live session is asked again. */
export const LIVE_POLL_MS = 10_000;

/**
 * Whether the session goes on now (reference isLiveSession): the pulse lists it among the active
 * ones, or its last event is younger than two minutes.
 */
export function isLiveTimeline(
  timeline: AnalyticsTimeline | undefined,
  now: number = Date.now(),
  activeNow = false,
): boolean {
  if (activeNow) return true;
  const last = timeline?.events.at(-1);
  if (!last) return false;
  return now - Date.parse(last.at) < LIVE_FOR_MS;
}

/** refetchInterval of the session's feed query: every 10 s while live, else none. */
export function liveRefetchInterval(
  timeline: AnalyticsTimeline | undefined,
  now: number = Date.now(),
  activeNow = false,
): number | false {
  return isLiveTimeline(timeline, now, activeNow) ? LIVE_POLL_MS : false;
}

/** Whether the head pulse lists the session among the ones working now. */
export function activeInPulse(
  pulse: Pick<AnalyticsPulse, 'active'> | undefined,
  session: { id: string; agent: string } | undefined,
): boolean {
  if (!pulse || !session) return false;
  return pulse.active.some((a) => a.sid === session.id && a.agent === session.agent);
}
