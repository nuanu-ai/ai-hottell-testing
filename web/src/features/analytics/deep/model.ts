// The Deep v2 document arrives as a free-form object (pkg/deepv2 owns its shape), so the screen
// reads it defensively: a field of the wrong type is treated as absent, never as a made-up value.
// Labels and the line references follow ai-hottell@c3c8357 ui/static/index.html (OUTCOME, CHECK)
// and ui/builder/conclusions.py (_REF, _checks).

import type { TagTone } from '../../../shared/ui';

export type DeepDoc = Record<string, unknown>;

export type TaskOutcome = 'verified' | 'partial' | 'failed' | 'unknown';

export type DeepTask = {
  id: string;
  goal: string;
  startLine: number | null;
  endLine: number | null;
  outcome: string | null;
};

export type CheckStatus =
  | 'suspected'
  | 'confirmed'
  | 'checked_clear'
  | 'insufficient_data'
  | 'not_checked'
  | 'not_applicable';

export type DeepCheck = {
  id: string;
  name: string;
  // null: the report holds no result for this check («нет данных»).
  status: string | null;
  summary: string;
  lines: number[];
  missingData: string[];
};

// The 13 checks in catalogue order (pkg/deepv2 CheckIDs) with their names (ui/builder/catalogue.json).
export const CHECK_CATALOG: readonly { id: string; name: string }[] = [
  { id: 'D01', name: 'Постановка задачи' },
  { id: 'D03', name: 'Повтор без прогресса' },
  { id: 'D05', name: '«Готово» без подтверждения' },
  { id: 'D07', name: 'Каскад ошибок среды' },
  { id: 'D10', name: 'Повторяющаяся инструкция' },
  { id: 'D12', name: 'Агент забыл сказанное' },
  { id: 'D13', name: 'Выброс затрат' },
  { id: 'D16', name: 'Простой на человеке' },
  { id: 'D19', name: 'Пропущенный skill' },
  { id: 'D22', name: 'Не тот класс работы' },
  { id: 'D24', name: 'Неработающий MCP' },
  { id: 'D25', name: 'Skill без результата' },
  { id: 'D26', name: 'Устаревшие настройки' },
];

type Label = readonly [string, TagTone];

export const OUTCOME_LABELS: Record<TaskOutcome, Label> = {
  verified: ['подтверждён', 'ok'],
  partial: ['частично', 'warn'],
  failed: ['не достигнут', 'bad'],
  unknown: ['неизвестно', 'plain'],
};

export const CHECK_LABELS: Record<CheckStatus, Label> = {
  suspected: ['подозрение', 'warn'],
  confirmed: ['подтверждено', 'bad'],
  checked_clear: ['проверено · чисто', 'ok'],
  insufficient_data: ['мало данных', 'plain'],
  not_checked: ['не проверено', 'plain'],
  not_applicable: ['не относится', 'plain'],
};

export const NO_DATA: Label = ['нет данных', 'plain'];

// A label for a status the screen does not know: shown as is, never mapped to a known one.
export function labelOf<K extends string>(labels: Record<K, Label>, status: string | null): Label {
  if (status === null) return NO_DATA;
  return (labels as Record<string, Label | undefined>)[status] ?? [status, 'plain'];
}

const UUID = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}';
// `L1273`, `#1273`, `local_transcript:L1273`, `<uuid>:L12`, `L1-L5859: scan` — a range gives its first line.
const REF = new RegExp(`^\\s*(?:${UUID}\\s*:\\s*|local_transcript\\s*:\\s*)?[L#](\\d+)`, 'i');

export function lineOf(ref: unknown): number | null {
  if (typeof ref !== 'string') return null;
  const m = REF.exec(ref);
  return m ? Number(m[1]) : null;
}

export function linesOf(refs: unknown): number[] {
  if (!Array.isArray(refs)) return [];
  const out: number[] = [];
  for (const ref of refs) {
    const line = lineOf(ref);
    if (line !== null && !out.includes(line)) out.push(line);
  }
  return out;
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

function int(v: unknown): number | null {
  return typeof v === 'number' && Number.isInteger(v) ? v : null;
}

function strings(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string' && x !== '') : [];
}

function records(v: unknown): Record<string, unknown>[] {
  return Array.isArray(v)
    ? v.filter(
        (x): x is Record<string, unknown> =>
          typeof x === 'object' && x !== null && !Array.isArray(x),
      )
    : [];
}

export function deepTasks(deep: DeepDoc): DeepTask[] {
  return records(deep.tasks).map((t) => ({
    id: str(t.task_id),
    goal: str(t.goal),
    startLine: int(t.start_line),
    endLine: int(t.end_line),
    outcome: typeof t.outcome === 'string' ? t.outcome : null,
  }));
}

// All 13 checks in catalogue order, then any the catalogue does not know; a catalogue check the
// report lacks is «нет данных», not an invented status.
export function deepChecks(deep: DeepDoc): DeepCheck[] {
  const byId = new Map<string, DeepCheck>();
  for (const c of records(deep.checks)) {
    const id = str(c.id);
    if (id === '' || byId.has(id)) continue;
    byId.set(id, {
      id,
      name: '',
      status: typeof c.status === 'string' ? c.status : null,
      summary: str(c.summary),
      lines: linesOf(c.evidence),
      missingData: strings(c.missing_data),
    });
  }
  const out: DeepCheck[] = CHECK_CATALOG.map(({ id, name }) => ({
    ...(byId.get(id) ?? { id, status: null, summary: '', lines: [], missingData: [] }),
    name,
  }));
  const known = new Set(CHECK_CATALOG.map((c) => c.id));
  const extra = [...byId.values()]
    .filter((c) => !known.has(c.id))
    .sort((a, b) => a.id.localeCompare(b.id));
  return [...out, ...extra];
}

// The panel note: how many checks are in each status, «подозрение: 1 · не проверено: 12».
export function checksSummary(checks: readonly DeepCheck[]): string {
  const counts = new Map<string, number>();
  for (const c of checks) {
    const [label] = labelOf(CHECK_LABELS, c.status);
    counts.set(label, (counts.get(label) ?? 0) + 1);
  }
  return [...counts].map(([label, n]) => `${label}: ${String(n)}`).join(' · ');
}

export type ObservationStatus = 'suspected' | 'confirmed' | 'dismissed';

export const OBSERVATION_LABELS: Record<ObservationStatus, Label> = {
  suspected: ['подозрение', 'warn'],
  confirmed: ['подтверждено', 'bad'],
  dismissed: ['отклонено', 'plain'],
};

export type DeepObservation = {
  pattern: string;
  finding: string;
  status: string | null;
  lines: number[];
  taskIds: string[];
};

// Patterns outside the 13 checks (deep.observations).
export function deepObservations(deep: DeepDoc): DeepObservation[] {
  return records(deep.observations).map((o) => ({
    pattern: str(o.pattern),
    finding: str(o.finding),
    status: typeof o.status === 'string' ? o.status : null,
    lines: linesOf(o.evidence),
    taskIds: strings(o.task_ids),
  }));
}

// What the retro could not establish (deep.unknowns).
export function deepUnknowns(deep: DeepDoc): string[] {
  return strings(deep.unknowns);
}
