import type { components } from '../../../shared/api';
import { signalKeyOf } from '../model/sample';
import type { Selection } from '../tools/count';

type Friction = components['schemas']['AnalyticsFriction'];
type Finding = components['schemas']['AnalyticsFinding'];

export type FrictionCount = {
  // The signal's sessions among the chosen ones.
  sessions: string[];
  // Every session of the signal is chosen: its total count and cost stand.
  full: boolean;
  // Episodes in the chosen sessions; null when they cannot be counted.
  episodes: number | null;
};

// frIn of the reference: a friction signal over the chosen sessions. The cycle's «Сигналы» are the
// sum of these episodes, so the friction table and the cycle agree.
export function frictionIn(signal: Friction, selection: Selection): FrictionCount {
  const sessions = signal.sessions.filter((sid) => selection.ids.has(sid));
  const full = sessions.length === signal.sessions.length;
  let episodes: number | null;
  if (!sessions.length) episodes = 0;
  else if (full) episodes = signal.count;
  else if (Object.keys(signal.by_session).length)
    episodes = sessions.reduce((n, sid) => n + (signal.by_session[sid] ?? 0), 0);
  else episodes = null;
  return { sessions, full, episodes };
}

const SEVERITY = { bad: 0, warn: 1, info: 2 } as const;

// topicForSignal of the reference: the «Что исправить» topic of a signal, matched by signalKeyOf —
// a work topic touching the chosen sessions and not hidden as «Не проблема».
export function topicForSignal(
  key: string,
  findings: readonly Finding[],
  selection: Selection,
  hidden: ReadonlySet<string>,
): Finding | undefined {
  return findings
    .filter(
      (f) =>
        signalKeyOf(f) === key &&
        f.scope === 'work' &&
        !hidden.has(f.id) &&
        f.sessions.some((sid) => selection.ids.has(sid)),
    )
    .sort((a, b) => SEVERITY[a.sev] - SEVERITY[b.sev])[0];
}
