import type { components } from '../../../shared/api';
import { fmtN } from '../../../shared/lib/format';
import { localDaySpan, localDaysTo } from '../../../shared/lib/time';
import type { AnalyticsFilters, Period } from './filters';

// One base for every number of a page (README v5.1 «База подсчётов»), ported from the reference
// index.html: kindOf, inPeriod, inScope, sample, bySess, periodDays, frIn, scopeOf, signalOf,
// topicEps, topicsOf, impactFor. Pure functions over the dataset; END is the dataset's window.to.

type Schemas = components['schemas'];
type Dataset = Schemas['AnalyticsDataset'];
type Session = Schemas['AnalyticsSession'];
type Friction = Schemas['AnalyticsFriction'];
type Finding = Schemas['AnalyticsFinding'];

const DAY = 86_400_000;
export const noProject = 'без проекта';

export type SampleFilters = Pick<AnalyticsFilters, 'days' | 'agent' | 'project' | 'kind'>;

/** The sessions of a page and what the labels need to name the rest. */
export type Sample = {
  /** The sample: period, agent, project, and «Без служебных» hides kind=system. */
  S: Session[];
  /** The same filter before «Без служебных». */
  all: Session[];
  ids: ReadonlySet<string>;
  /** Every session of the dataset is in S: a row's own totals stand, by_session is not needed. */
  full: boolean;
  /** Your sessions (kind=user) and the scheduled ones (kind=automation) of S. */
  U: Session[];
  A: Session[];
  sys: number;
  /** The service sessions «Без служебных» hides; 0 under «Все». */
  hiddenSys: number;
};

export const kindOf = (s: Pick<Session, 'kind'>) => s.kind;
export const projectOf = (s: Pick<Session, 'project'>) => s.project || noProject;
export const endOf = (dataset: Pick<Dataset, 'window'>) => Date.parse(dataset.window.to);

/** A session touches the period ending offset periods before END. «Всё» takes every session. */
export function inPeriod(s: Session, days: Period, end: number, offset = 0): boolean {
  if (days === 'all') {
    return true;
  }
  const to = end - offset * days * DAY;
  const from = to - days * DAY;
  return Date.parse(s.end || s.start) > from && Date.parse(s.start) <= to;
}

export function inScope(s: Session, filters: SampleFilters, end: number, offset = 0): boolean {
  return (
    inPeriod(s, filters.days, end, offset) &&
    (filters.agent === 'all' || s.agent === filters.agent) &&
    (filters.project === 'all' || projectOf(s) === filters.project)
  );
}

/** offset 1 is the previous period of the same length, for «к прошлым 7 дн». */
export function sample(
  dataset: Pick<Dataset, 'sessions' | 'window'>,
  filters: SampleFilters,
  offset = 0,
): Sample {
  const end = endOf(dataset);
  const all = dataset.sessions.filter((s) => inScope(s, filters, end, offset));
  const S = all.filter((s) => filters.kind === 'all' || kindOf(s) !== 'system');
  const sys = all.filter((s) => kindOf(s) === 'system').length;
  return {
    S,
    all,
    ids: new Set(S.map((s) => s.id)),
    full: S.length === dataset.sessions.length,
    U: S.filter((s) => kindOf(s) === 'user'),
    A: S.filter((s) => kindOf(s) === 'automation'),
    sys,
    hiddenSys: filters.kind === 'work' ? sys : 0,
  };
}

type Counts = Readonly<Record<string, number | Readonly<Record<string, number | null>> | null>>;

/**
 * A field of a row over the sample: the row's own total when every session is chosen; else the sum
 * over by_session, and null when there is no count by session or a chosen session lacks the field.
 */
export function bySess(
  total: number | null | undefined,
  bySession: Counts | null | undefined,
  X: Pick<Sample, 'ids' | 'full'>,
  key: string,
): number | null {
  if (X.full) {
    return total ?? null;
  }
  if (!bySession) {
    return null;
  }
  let sum = 0;
  for (const [id, value] of Object.entries(bySession)) {
    if (!X.ids.has(id)) {
      continue;
    }
    const count = typeof value === 'number' ? value : value?.[key];
    if (count == null) {
      return null;
    }
    sum += count;
  }
  return sum;
}

/** The browser's days of the period, oldest first, as yyyy-mm-dd; «Всё» starts at the first session. */
export function periodDays(S: readonly Session[], days: Period, end: number): string[] {
  const n =
    days === 'all'
      ? Math.max(
          1,
          localDaySpan(
            Math.min(...S.map((s) => Date.parse(s.start)).filter((t) => !isNaN(t)), end),
            end,
          ),
        )
      : days;
  return localDaysTo(end, n);
}

/** A count by session over the chosen sessions; null when a chosen session has no count. */
function sumOver(chosen: readonly string[], counts: Readonly<Record<string, number>>) {
  let sum = 0;
  for (const id of chosen) {
    const count = counts[id];
    if (count === undefined) {
      return null;
    }
    sum += count;
  }
  return sum;
}

export type SignalIn = {
  /** The signal's sessions in the sample. */
  ss: string[];
  /** Every session of the signal is in the sample. */
  full: boolean;
  /** Episodes in the sample; null without a count by session. Evidence never counts: it is cut to 60. */
  eps: number | null;
};

export function frIn(f: Friction, ids: ReadonlySet<string>): SignalIn {
  const ss = f.sessions.filter((id) => ids.has(id));
  const full = ss.length === f.sessions.length;
  const eps = ss.length === 0 ? 0 : full ? f.count : sumOver(ss, f.by_session);
  return { ss, full, eps };
}

export type Scope = 'work' | 'collection';

/** A card without scope is about collection when a detector diagnoses it. */
export function scopeOf(f: Pick<Finding, 'scope' | 'kind' | 'source'>): Scope {
  const scope = f.scope as Scope | undefined;
  return scope === 'collection' || (!scope && f.kind === 'diagnostic' && f.source === 'detector')
    ? 'collection'
    : 'work';
}

/**
 * The friction signal a detector's D-code stands on, as the server's genericCoveredBy: the topic
 * of D03 is the retry signal's, of D24 mcpfail's, of D16 wait's. The one table for signalOf and
 * every topicForSignal.
 */
const signalOfPattern: Readonly<Record<string, string>> = {
  D03: 'retry',
  D24: 'mcpfail',
  D16: 'wait',
};

/** The key of a topic's signal: a D-code through the table, any other pattern_id as it is. */
export const signalKeyOf = (f: Pick<Finding, 'pattern_id'>): string | undefined =>
  f.pattern_id ? (signalOfPattern[f.pattern_id] ?? f.pattern_id) : undefined;

/** A topic's signal, by its signal key and never by a matching name. */
export const signalOf = (dataset: Pick<Dataset, 'friction'>, f: Finding): Friction | null => {
  const key = signalKeyOf(f);
  return key ? (dataset.friction.find((r) => r.key === key) ?? null) : null;
};

export type Topic = SignalIn & {
  f: Finding;
  sig: Friction | null;
  /** The episodes come from a count (the signal or the finding's episodes), not from evidence. */
  counted: boolean;
};

export function topicEps(
  dataset: Pick<Dataset, 'friction'>,
  f: Finding,
  ids: ReadonlySet<string>,
): Omit<Topic, 'f'> {
  const sig = signalOf(dataset, f);
  // A generic card «Разобрать эпизоды…» is its signal (pattern_id is the key) and counts by it.
  // A detector's D-code card links to its signal but has its own rule (D16 counts permission
  // windows the wait signal leaves out, D24 one server of mcpfail), so it counts by its finding.
  if (sig && sig.key === f.pattern_id) {
    return { ...frIn(sig, ids), sig, counted: true };
  }
  const ss = f.sessions.filter((id) => ids.has(id));
  const full = ss.length === f.sessions.length;
  // Without a signal the episodes are the finding's own count, known for the whole finding only:
  // impact_by_session measures the impact (D24: calls after the first error, D16: minutes of
  // waiting), whatever impact_unit says.
  return { ss, full, eps: full ? f.episodes : null, sig: null, counted: true };
}

const severityRank = { bad: 0, warn: 1, info: 2 } as const;

/** The work topics touching the sample, without the hidden ones: bad → warn → info, then by episodes. */
export function topicsOf(
  dataset: Pick<Dataset, 'findings' | 'friction'>,
  X: Pick<Sample, 'ids'>,
  hidden: ReadonlySet<string> = new Set(),
): Topic[] {
  return dataset.findings
    .filter(
      (f) => scopeOf(f) === 'work' && f.sessions.some((id) => X.ids.has(id)) && !hidden.has(f.id),
    )
    .map((f) => ({ f, ...topicEps(dataset, f, X.ids) }))
    .sort((a, b) => severityRank[a.f.sev] - severityRank[b.f.sev] || (b.eps ?? -1) - (a.eps ?? -1));
}

/** A collection card's impact over the chosen sessions, when it is counted by session. */
export function impactFor(
  f: Finding,
  ids: ReadonlySet<string>,
): { value: string; label: string } | null {
  const chosen = f.sessions.filter((id) => ids.has(id));
  const counts = f.impact_by_session;
  if (Object.keys(counts).length === 0 || chosen.length === f.sessions.length) {
    return f.impact ?? null;
  }
  const value = chosen.reduce((n, id) => n + (counts[id] ?? 0), 0);
  return {
    value: `${fmtN(value)} ${f.impact_unit ?? ''}`.trim(),
    label: `${f.impact?.label ?? ''} · в выбранных`,
  };
}
