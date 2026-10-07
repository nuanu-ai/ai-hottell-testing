// The «Исправлено» rows and steps 4–6 of the improvement cycle, as the v5.1 page
// (ai-hottell@c3c8357 ui/static/index.html journalTag, journalRow, renderFix; README «5. Исправлено»).

import { plural } from '../../../shared/lib/format';
import type { CycleStep } from '../../../shared/ui';
import type { ImprovementCycle, JournalEntry } from './api';
import { decisionLabel } from './model';

export type JournalTone = 'ok' | 'warn' | 'plain' | 'muted';

const LAYERS: Record<string, string> = {
  experience: 'Опыт',
  instructions: 'Инструкции',
  skill: 'Skill',
  technical: 'Техника',
};

const EFFECTS: Record<string, string> = {
  not_measured: 'эффект не измерен',
  helped: 'помогло',
  no_effect: 'без эффекта',
  worse: 'стало хуже',
  insufficient_data: 'мало данных',
};

type Evidence = { session: string; seq: number | null; quote: string };

// The parts of the masked record the page shows; a field of the wrong type is absent.
export type JournalRow = {
  id: string;
  // Whose record it is: a session link carries it, the session may be outside the page's dataset.
  user: string;
  at: string;
  kind: JournalEntry['kind'];
  layer: string;
  target: string;
  change: string;
  topic: string;
  check: string;
  checkAfter: string;
  rollback: string;
  proposals: string[];
  evidence: Evidence[];
  tag: [string, JournalTone];
  result: string;
  applied: boolean;
  checked: boolean;
};

function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

function evidenceOf(v: unknown): Evidence[] {
  if (!Array.isArray(v)) return [];
  return v.flatMap((e: unknown) => {
    if (typeof e !== 'object' || e === null) return [];
    const r = e as Record<string, unknown>;
    const session = str(r.session);
    return session || str(r.quote)
      ? [{ session, seq: typeof r.seq === 'number' ? r.seq : null, quote: str(r.quote) }]
      : [];
  });
}

function texts(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string' && x !== '') : [];
}

function objectOf(v: unknown): Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : {};
}

// A lifecycle event keeps its sources in proposal_sources (session and transcript lines) and the
// evidence of an application or a measurement in detail.evidence.
function lifecycleEvidence(record: Record<string, unknown>): Evidence[] {
  const sources = Array.isArray(record.proposal_sources) ? record.proposal_sources : [];
  return [
    ...sources.flatMap((v: unknown) => {
      const s = objectOf(v);
      const session = str(s.session_id);
      const quote = texts(s.evidence).join(', ');
      return session || quote ? [{ session, seq: null, quote }] : [];
    }),
    ...texts(objectOf(record.detail).evidence).map((quote) => ({ session: '', seq: null, quote })),
  ];
}

// The check of a measurement: method, metric, the sample before and after, the note.
function effectCheck(record: Record<string, unknown>): string {
  const d = objectOf(record.detail);
  const n = (v: unknown) => (typeof v === 'number' ? String(v) : '—');
  const sample =
    typeof d.n_before === 'number' || typeof d.n_after === 'number'
      ? `выборка ${n(d.n_before)} → ${n(d.n_after)}`
      : '';
  return [str(d.method), str(d.metric), sample, str(d.note)].filter(Boolean).join(' · ');
}

const pad = (n: number) => String(n).padStart(2, '0');

// DD.MM of a calendar day (check_after is a date, not a moment).
function dayMonth(date: string): string {
  const m = /^\d{4}-(\d{2})-(\d{2})/.exec(date);
  return m ? `${m[2] ?? ''}.${m[1] ?? ''}` : date;
}

// DD.MM in the browser's zone, as dmLocal.
export function localDayMonth(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '—' : `${pad(d.getDate())}.${pad(d.getMonth() + 1)}`;
}

function coachTag(e: JournalEntry, checkAfter: string): [string, JournalTone] {
  if (e.decision === 'declined' || e.decision === 'not_justified') return ['не внедряли', 'muted'];
  if (e.result === 'not_repeated') {
    const n = e.observations;
    return n == null
      ? ['повтора не было', 'ok']
      : [`повтора не было на ${String(n)} ${plural(n, 'задаче', 'задачах', 'задачах')}`, 'ok'];
  }
  if (e.result === 'repeated') {
    return [
      `повторилось на ${e.repeats == null ? '—' : String(e.repeats)} из ${e.observations == null ? '—' : String(e.observations)}`,
      'warn',
    ];
  }
  if (e.result === 'not_enough_data') return ['мало данных', 'plain'];
  return [checkAfter ? `проверка ${dayMonth(checkAfter)}` : 'без проверки', 'plain'];
}

function eventTag(e: JournalEntry, record: Record<string, unknown>): [string, JournalTone] {
  if (e.kind === 'application') return ['внедрено', 'plain'];
  if (e.kind === 'effect') {
    const status = e.decision ?? str(record.status);
    return [
      EFFECTS[status] ?? status,
      status === 'helped' ? 'ok' : status === 'worse' ? 'warn' : 'plain',
    ];
  }
  const status = e.decision ?? str(record.status);
  return [decisionLabel(status), status === 'rejected' ? 'muted' : 'plain'];
}

export function journalRow(e: JournalEntry): JournalRow {
  const r = e.record;
  const coach = e.kind === 'coach_decision';
  const checkAfter = str(r.check_after);
  const lifecycle = e.kind === 'application' || e.kind === 'effect';
  const findings: unknown[] = Array.isArray(r.findings) ? r.findings : [];
  const proposals = [
    ...new Set(
      [e.proposal_id, ...findings].filter(
        (x): x is string => typeof x === 'string' && x.startsWith('p2:'),
      ),
    ),
  ];
  const applied = coach
    ? e.decision === 'applied' || e.decision === 'test'
    : e.kind === 'application';
  const tag = coach ? coachTag(e, checkAfter) : eventTag(e, r);
  const result =
    coach && e.result
      ? `итог: ${tag[0]}${e.checks && e.checks > 1 ? `, проверок ${String(e.checks)}` : ''}`
      : checkAfter
        ? `проверка после ${dayMonth(checkAfter)}`
        : '';
  return {
    id: e.record_id,
    user: e.user_id,
    at: e.recorded_at,
    kind: e.kind,
    layer: LAYERS[str(r.layer)] ?? '—',
    target: str(r.target),
    change: str(r.change),
    topic: str(r.topic) || e.topic_key || e.proposal_id || '',
    check: e.kind === 'effect' ? effectCheck(r) : str(r.check),
    checkAfter,
    rollback: str(r.rollback),
    proposals,
    evidence: lifecycle ? lifecycleEvidence(r) : evidenceOf(r.evidence),
    tag,
    result,
    applied,
    checked: applied && coach && Boolean(e.result),
  };
}

// The key an effect event shares with the application it measured.
function lifecycleKey(e: JournalEntry): string {
  return JSON.stringify([e.user_id, e.proposal_id ?? '', str(e.record.proposal_fingerprint)]);
}

// The rows of the journal (newest first, as GET /journal answers). An application is checked
// when an effect of the same owner, proposal and fingerprint follows it, as the server counts
// «Проверено»; the effect itself is a measurement, not a checked application.
export function journalRows(entries: readonly JournalEntry[]): JournalRow[] {
  const later = new Set<string>(); // the keys of the effects newer than the current entry
  return entries.map((e) => {
    const row = journalRow(e);
    if (e.kind === 'effect') later.add(lifecycleKey(e));
    else if (e.kind === 'application') row.checked = later.has(lifecycleKey(e));
    return row;
  });
}

export type CycleKey = 'discussed' | 'applied' | 'checked';

// Steps 4–6 over the journal of the period; an empty journal shows «—», not zeros.
export function journalCycleSteps(cycle: ImprovementCycle): CycleStep[] {
  const empty = cycle.empty;
  return [
    {
      key: 'discussed',
      label: 'Обсуждено',
      value: empty ? null : cycle.discussed,
      sub: empty ? 'журнал пуст' : 'с любым решением',
      title: 'Записи журнала коуча с любым решением и решения из веба',
    },
    {
      key: 'applied',
      label: 'Внедрено',
      value: empty ? null : cycle.applied,
      sub: empty ? '—' : 'внесённые изменения',
      title: 'Внесённые изменения: applied и test коуча, события application',
    },
    {
      key: 'checked',
      label: 'Проверено',
      value: empty ? null : cycle.checked,
      suffix: empty ? undefined : ` из ${String(cycle.checked_of)}`,
      sub: empty ? '—' : 'с итогом проверки',
      title: 'Внедрённые изменения с итогом проверки коуча или замером эффекта',
      tone: !empty && cycle.checked > 0 ? 'ok' : undefined,
    },
  ];
}

export const EMPTY_HINT = 'Обсудите первую тему в Codex или Claude Code';

// The rows a pressed step leaves: «Внедрено» — applied, «Проверено» — applied with a result.
export function rowsOfStep(rows: readonly JournalRow[], step: string | null): JournalRow[] {
  if (step === 'applied') return rows.filter((r) => r.applied);
  if (step === 'checked') return rows.filter((r) => r.checked);
  return [...rows];
}
