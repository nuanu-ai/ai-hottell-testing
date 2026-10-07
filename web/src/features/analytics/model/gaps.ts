import { plural } from '../../../shared/lib/format';

// «Ограничения данных» (README v5.1 «7»): every methodology note of a page in one folded block at
// its end. The reference gapsBlock (index.html:L575–L580). A gap starting «<id8>:» or «<id8>…<id4>:»
// belongs to one session and is shown in its timeline only.

const perSession = /^[0-9a-f]{8}(…[0-9a-f]{4})?:\s*/;
export const noSessionGaps = 'Для этой сессии отдельных замечаний нет.';

export type Limits = { items: string[]; count: number };

/** A page's notes: its own first, then the general gaps, then how many stay in the timelines. */
export function pageGaps(gaps: readonly string[], extra: readonly string[] = []): Limits {
  const general = gaps.filter((gap) => !perSession.test(gap));
  return limitsOf(general, gaps.length - general.length, extra);
}

/** The notes of a page from the general gaps and the count of those about one session. */
export function limitsOf(
  general: readonly string[],
  rest: number,
  extra: readonly string[] = [],
): Limits {
  const items = [
    ...extra,
    ...general,
    ...(rest > 0
      ? [
          `Ещё ${String(rest)} ${plural(rest, 'замечание', 'замечания', 'замечаний')} по отдельным сессиям — в ленте каждой сессии.`,
        ]
      : []),
  ];
  return { items, count: items.length };
}

/** One session's notes without their prefix; none gives one line and a count of 0. */
export function sessionGaps(gaps: readonly string[], sid: string): Limits {
  const prefixes = [`${sid.slice(0, 8)}:`, `${sid.slice(0, 8)}…${sid.slice(-4)}:`];
  const items = gaps
    .filter((gap) => prefixes.some((prefix) => gap.startsWith(prefix)))
    .map((gap) => gap.replace(perSession, ''));
  return items.length > 0 ? { items, count: items.length } : { items: [noSessionGaps], count: 0 };
}
