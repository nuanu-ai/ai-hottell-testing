import type { components } from '../../../shared/api';
import { pct } from '../../../shared/lib/format';

type Session = components['schemas']['AnalyticsSession'];

/**
 * «Полнота данных» over the sample (README v5.1 «3»; the reference coverageLine, L581–L585):
 * hooks from the start, the share of calls with a known outcome, sessions with a recorded cost.
 * null for an empty sample: there is nothing to qualify.
 */
export function coverageLine(S: readonly Session[]): string | null {
  if (S.length === 0) {
    return null;
  }
  const n = S.length;
  const full = S.filter((s) => s.sources.hooks === 'recorded').length;
  const calls = S.reduce((sum, s) => sum + s.calls, 0);
  const known = S.reduce((sum, s) => sum + s.outcome_known, 0);
  const cost = S.filter((s) => s.cost_usd != null).length;
  const outcome = calls > 0 ? pct(known / calls) : '—';
  return (
    `Полнота данных: хуки с начала сессии — у ${String(full)} из ${String(n)} · ` +
    `исход вызова известен — ${outcome} · стоимость записана — у ${String(cost)} из ${String(n)} сессий`
  );
}
