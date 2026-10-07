import type { components } from '../../../shared/api';
import { fmtMoney, fmtN, plural } from '../../../shared/lib/format';
import type { Sample, Topic } from '../model/sample';

type Session = components['schemas']['AnalyticsSession'];

const short = (id: string) => `${id.slice(0, 8)}…${id.slice(-4)}`;

/**
 * The price on the right of a topic (README v5.1 «4. Темы»; the reference topicCard, L623–L638):
 * «79 эпизодов · 8 сессий · ≈ $7.86». The cost is the episodes' own and only when every session of
 * the signal is in the sample; a partial sample adds « · в выбранных»; no count — «— эпизодов».
 */
export function topicPrice(t: Topic): { lead: string; rest: string } {
  const n = t.ss.length;
  const sessions = `${String(n)} ${plural(n, 'сессия', 'сессии', 'сессий')}`;
  const partial = t.full ? '' : ' · в выбранных';
  if (!t.counted) {
    return { lead: sessions, rest: partial };
  }
  const lead =
    t.eps == null
      ? '— эпизодов'
      : `${fmtN(t.eps)} ${plural(t.eps, 'эпизод', 'эпизода', 'эпизодов')}`;
  const cost = t.full && t.sig?.cost_usd != null ? ` · ≈ ${fmtMoney(t.sig.cost_usd)}` : '';
  return { lead, rest: ` · ${sessions}${cost}${partial}` };
}

/**
 * Why the topic matters, in one line: the session with the most episodes in the sample,
 * «Больше всего в 01a0f058…5b9f · ai-hottell: 5 из 9 эпизодов»; without a count, the first
 * sentence of what.
 */
export function topicWhy(t: Topic, X: Pick<Sample, 'ids'>, sessions: readonly Session[]): string {
  // Episodes by session come from the signal only: impact_by_session measures the impact.
  const counts = t.sig ? t.sig.by_session : {};
  const top = Object.entries(counts)
    .filter(([id]) => X.ids.has(id))
    .sort((a, b) => b[1] - a[1])[0];
  if (top && t.eps) {
    const [id, count] = top;
    const session = sessions.find((s) => s.id === id);
    return (
      `Больше всего в ${short(id)}${session ? ` · ${session.project}` : ''}: ` +
      `${fmtN(count)} из ${fmtN(t.eps)} ${plural(t.eps, 'эпизода', 'эпизодов', 'эпизодов')}`
    );
  }
  return t.f.what.split(/(?<=\.)\s/)[0] ?? '';
}
