import type { components } from '../../../shared/api';
import { plural } from '../../../shared/lib/format';
import type { CycleStep } from '../../../shared/ui';
import type { Period } from '../model/filters';
import { frIn, type Sample } from '../model/sample';

type Friction = components['schemas']['AnalyticsFriction'];

// The coach journal as the cycle needs it; E4 brings the entries. null — no journal yet.
export type CycleJournalEntry = { decision: string; result?: unknown };

export type StepKey = 'sessions' | 'signals' | 'topics' | 'discussed' | 'applied' | 'checked';

/** «за 7 дней», «за всё время». */
export function periodText(days: Period): string {
  return days === 'all'
    ? 'за всё время'
    : `за ${String(days)} ${plural(days, 'день', 'дня', 'дней')}`;
}

/**
 * Friction episodes in the sample: the sum over the signals; one signal without a count by session
 * makes it unknown. «Трение» counts its rows by the same frIn, so the two numbers agree.
 */
export function signalsOf(friction: readonly Friction[], ids: ReadonlySet<string>): number | null {
  let sum = 0;
  for (const row of friction) {
    const { eps } = frIn(row, ids);
    if (eps == null) {
      return null;
    }
    sum += eps;
  }
  return sum;
}

const isApplied = (entry: CycleJournalEntry) =>
  entry.decision === 'applied' || entry.decision === 'test';

type StepsInput = {
  X: Sample;
  kind: 'work' | 'all';
  days: Period;
  friction: readonly Friction[];
  topics: number;
  journal: readonly CycleJournalEntry[] | null;
};

/** The six steps of the improvement cycle (README v5.1 «1»; the reference renderFix, L588–L603). */
export function steps({ X, kind, days, friction, topics, journal }: StepsInput): (CycleStep & {
  key: StepKey;
})[] {
  const signals = signalsOf(friction, X.ids);
  const empty = !journal || journal.length === 0;
  const entries = journal ?? [];
  const applied = entries.filter(isApplied);
  const checked = applied.filter((entry) => entry.result != null);
  const anyTest = applied.some((entry) => entry.decision === 'test');
  const sessionsSub =
    (X.A.length > 0 ? `ваши и ${String(X.A.length)} по расписанию` : 'ваши') +
    (kind === 'all' && X.sys > 0 ? ` · ${String(X.sys)} служебных` : '') +
    ` · ${periodText(days)}`;

  return [
    {
      key: 'sessions',
      label: 'Сессии',
      value: X.S.length,
      sub: sessionsSub,
      title: 'Сессии выборки: ваши и запуски по расписанию; служебные — в режиме «Все»',
    },
    {
      key: 'signals',
      label: 'Сигналы',
      value: signals,
      sub: signals == null ? 'нет счёта по сессиям' : 'эпизоды трения',
      title:
        'Эпизоды трения в этих сессиях. «—» — сигнал задевает сессии вне выборки, а счёта по сессиям в данных нет',
    },
    {
      key: 'topics',
      label: 'Темы',
      value: topics,
      sub: 'о вашей работе',
      title: 'Карточки о работе, без здоровья сбора данных',
    },
    {
      key: 'discussed',
      label: 'Обсуждено',
      value: empty ? null : entries.length,
      sub: empty ? 'журнал пуст' : 'с любым решением',
      title: 'Записи журнала коуча с любым решением',
    },
    {
      key: 'applied',
      label: 'Внедрено',
      value: empty ? null : applied.length,
      sub: empty ? '—' : anyTest ? 'applied и test' : 'decision: applied',
      title: `Внесённые изменения: decision applied${anyTest ? ' и test' : ''}`,
    },
    {
      key: 'checked',
      label: 'Проверено',
      value: empty ? null : checked.length,
      suffix: empty ? undefined : ` из ${String(applied.length)}`,
      sub: empty ? '—' : 'с итогом проверки',
      title: 'Внедрённые изменения с итогом проверки',
      tone: !empty && checked.length > 0 ? 'ok' : undefined,
    },
  ];
}
