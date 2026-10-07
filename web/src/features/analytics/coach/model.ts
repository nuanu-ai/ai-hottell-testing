// Russian captions and the order of the proposal registry, as the v5.1 page
// (ai-hottell@c3c8357 ui/static/index.html KIND, READY, DECISION, EXEC, EFFECT; README «4. Темы»).

import { plural } from '../../../shared/lib/format';
import type { Proposal } from './api';

export type Severity = 'bad' | 'warn' | 'info';

// change_type of Deep v2 and the card kinds of conclusions.py _KIND: workflow is a script;
// settings, product and diagnosis are a diagnostic.
const KIND_LABELS: Record<string, string> = {
  personalization: 'персонализация',
  project_rule: 'правило проекта',
  skill: 'skill',
  hook: 'проверка · hook',
  script: 'скрипт',
  workflow: 'скрипт',
  automation: 'автоматизация',
  settings: 'диагностика',
  product: 'диагностика',
  diagnosis: 'диагностика',
  diagnostic: 'диагностика',
  habit: 'привычка',
};

export function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind;
}

const READINESS: Record<string, string> = {
  hypothesis: 'гипотеза',
  needs_specification: 'нужна спецификация',
  prepared_verified: 'готово к применению',
};
const DECISION: Record<string, string> = {
  not_requested: 'не запрошено',
  accepted: 'принято',
  rejected: 'отклонено',
  revision_requested: 'на доработке',
};
const EXECUTION: Record<string, string> = { not_applied: 'не применено', applied: 'применено' };
const EFFECT: Record<string, string> = {
  not_measured: 'не измерен',
  helped: 'помогло',
  no_effect: 'без эффекта',
  worse: 'стало хуже',
  insufficient_data: 'мало данных',
};

export function decisionLabel(status: string): string {
  return DECISION[status] ?? status;
}

// The four axes as caption and value pairs: Готовность, Решение, Применение, Эффект.
export function axesOf(p: Proposal): [string, string][] {
  const { readiness, decision, execution, effect } = p.axes;
  const applied =
    execution.status === 'applied' && execution.version
      ? `применено, версия ${execution.version}`
      : (EXECUTION[execution.status] ?? execution.status);
  return [
    ['Готовность', READINESS[readiness] ?? readiness],
    ['Решение', decisionLabel(decision.status)],
    ['Применение', applied],
    ['Эффект', EFFECT[effect.status] ?? effect.status],
  ];
}

// The severity of conclusions.py _severity, which the server computes: bad only on a confirmation.
export function severityOf(p: Proposal): Severity {
  return p.severity;
}

export function sessionsOf(p: Proposal): number {
  return new Set(p.sources.map((s) => s.session_id)).size;
}

const SEV_RANK: Record<Severity, number> = { bad: 0, warn: 1, info: 2 };

// Important first (bad → warn → info), then by the number of sessions; equal ones keep their order.
export function sortProposals(list: readonly Proposal[]): Proposal[] {
  return [...list].sort(
    (a, b) => SEV_RANK[severityOf(a)] - SEV_RANK[severityOf(b)] || sessionsOf(b) - sessionsOf(a),
  );
}

// What the coach's checks found after the change (README «5. Исправлено» captions).
export function recurrenceText(p: Proposal): string | null {
  const r = p.recurrence;
  if (!r) return null;
  if (r.result === 'not_repeated') {
    if (r.observations == null) return 'повтора не было';
    const n = r.observations;
    return `повтора не было на ${String(n)} ${plural(n, 'задаче', 'задачах', 'задачах')}`;
  }
  if (r.result === 'repeated') {
    return r.repeats != null && r.observations != null
      ? `повторилось на ${String(r.repeats)} из ${String(r.observations)}`
      : 'повторилось';
  }
  return 'мало данных';
}

export function sessionsText(n: number): string {
  return `${String(n)} ${plural(n, 'сессия', 'сессии', 'сессий')}`;
}

export function titleOf(p: Proposal): string {
  return p.title || p.change || p.id;
}
