import type { components } from '../../../shared/api';
import { frIn, signalKeyOf } from '../model/sample';

type Friction = components['schemas']['AnalyticsFriction'];
type Finding = components['schemas']['AnalyticsFinding'];

export type SignalInSample = { sessions: number; eps: number | null };

/**
 * A friction signal within the sample (reference frIn): its sessions there and its episodes —
 * the whole count when every session is selected, the sum over by_session otherwise, and null
 * when a selected session has no count, as in «Что исправить».
 */
export function signalInSample(f: Friction, ids: ReadonlySet<string>): SignalInSample {
  const { ss, eps } = frIn(f, ids);
  return { sessions: ss.length, eps };
}

/**
 * The topic of a signal (reference topicForSignal): a work finding whose signalKeyOf is this key, that
 * touches the sample and is not hidden as «Не проблема».
 */
export function topicForSignal(
  key: string,
  findings: readonly Finding[],
  ids: ReadonlySet<string>,
  hidden: ReadonlySet<string> = new Set(),
): string | undefined {
  return findings.find(
    (f) =>
      f.scope === 'work' &&
      signalKeyOf(f) === key &&
      !hidden.has(f.id) &&
      f.sessions.some((id) => ids.has(id)),
  )?.id;
}
