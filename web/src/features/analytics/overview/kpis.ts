import type { components } from '../../../shared/api';
import { fmtDuration, fmtMoney, fmtN, pct, plural } from '../../../shared/lib/format';
import type { Whose } from '../model/filters';

type Session = components['schemas']['AnalyticsSession'];

/** The page's sample as the overview needs it (reference sample()). */
export type OverviewSample = {
  // Sessions under the filter, «Без служебных» applied.
  S: readonly Session[];
  // System sessions under the filter: hidden ones under «Без служебных», shown ones under «Все».
  system: number;
};

export type KpiDelta = { text: string; change: number; goodWhen: 'up' | 'down' };

export type KpiItem = {
  key: string;
  label: string;
  value: number | null;
  format: (value: number) => string;
  sub: string;
  delta: KpiDelta | null;
  title?: string;
};

export type KpiContext = {
  // The period in days; 0 is «Всё», which has no previous period.
  days: number;
  kind: 'work' | 'all';
  // Whose data it is: «Ваше…» only on the viewer's own (HT-444).
  whose?: Whose;
};

const TIME_LABEL: Record<Whose, string> = {
  own: 'Ваше время',
  other: 'Время человека',
  all: 'Время людей',
};

const sum = (list: readonly Session[], f: (s: Session) => number | null | undefined) =>
  list.reduce((acc, s) => acc + (f(s) ?? 0), 0);

/** The person's own work: neither a scheduled run nor a session another agent started (HT-501). */
const ownWork = (list: readonly Session[]) =>
  list.filter((s) => s.kind !== 'automation' && s.kind !== 'agent');

function knownSum(list: readonly Session[], f: (s: Session) => number | null): number | null {
  return list.some((s) => f(s) != null) ? sum(list, f) : null;
}

const knownCalls = (list: readonly Session[]) => sum(list, (s) => s.outcome_known);

function errorShare(list: readonly Session[]): number | null {
  const known = knownCalls(list);
  return known ? sum(list, (s) => s.errors) / known : null;
}

/** Spend over the sessions with a known cost: null when none has one, not 0; «Команда» marks a partial one. */
export function spendOf(list: readonly Session[]) {
  const costed = list.filter((s) => s.cost_usd != null);
  return {
    cost: costed.length ? sum(costed, (s) => s.cost_usd) : null,
    costed: costed.length,
    allCosted: costed.length === list.length,
  };
}

function hoursOrMinutes(minutes: number): string {
  return minutes >= 60
    ? `${String(Math.round(minutes / 60))} ч`
    : `${String(Math.round(minutes))} м`;
}

/**
 * The six KPI of «Обзор» (reference renderOverview): your time, the agent's work, your sessions,
 * spend, cache hits and tool errors, each with its caption and, when both values are known, the
 * change to the previous period of the same length.
 */
export function buildKpis(
  X: OverviewSample,
  P: OverviewSample | null,
  { days, kind, whose = 'own' }: KpiContext,
): KpiItem[] {
  const S = X.S;
  const prev = days && P && P.S.length ? P.S : null;

  const delta = (
    cur: number | null,
    before: number | null | undefined,
    goodWhen: 'up' | 'down',
  ): KpiDelta | null => {
    if (!prev || cur == null || before == null || !before) return null;
    const change = ((cur - before) / before) * 100;
    if (Math.abs(change) < 2) return { text: '≈ как в прошлом периоде', change, goodWhen };
    return {
      text: `${change > 0 ? '▲' : '▼'} ${Math.abs(change).toFixed(0)}% к прошлым ${String(days)} дн`,
      change,
      goodWhen,
    };
  };

  const work = ownWork(S);
  const automation = S.filter((s) => s.kind === 'automation');
  const agents = S.filter((s) => s.kind === 'agent');
  const users = S.filter((s) => s.kind === 'user');
  const userMin = knownSum(work, (s) => s.user_min);
  const agentMin = knownSum(work, (s) => s.active_min);
  const autoMin = sum(automation, (s) => s.active_min);
  const agentsMin = sum(agents, (s) => s.active_min);

  const sessNote = [
    automation.length ? `ещё ${String(automation.length)} по расписанию` : '',
    agents.length ? `${String(agents.length)} агентских` : '',
    X.system ? `${String(X.system)} служебных${kind === 'work' ? ' скрыто' : ''}` : '',
  ]
    .filter(Boolean)
    .join(' · ');

  const { cost, costed, allCosted } = spendOf(S);
  const prevCost = prev?.every((s) => s.cost_usd != null) ? sum(prev, (s) => s.cost_usd) : null;

  const withTokens = S.filter((s) => s.tok?.input);
  const tokIn = sum(withTokens, (s) => s.tok?.input);
  const tokCached = sum(withTokens, (s) => s.tok?.cached);

  const calls = sum(S, (s) => s.calls);
  const known = knownCalls(S);
  const errors = errorShare(S);

  return [
    {
      key: 'user',
      label: TIME_LABEL[whose],
      value: userMin,
      format: fmtDuration,
      sub: 'паузы «ответ → реплика» ≤ 30 мин',
      delta: delta(userMin, prev && sum(ownWork(prev), (s) => s.user_min), 'down'),
    },
    {
      key: 'agent',
      label: 'Агент работал',
      value: agentMin,
      format: fmtDuration,
      sub:
        [
          autoMin ? `ещё ${hoursOrMinutes(autoMin)} — по расписанию` : '',
          agentsMin ? `${hoursOrMinutes(agentsMin)} — в агентских` : '',
        ]
          .filter(Boolean)
          .join(' · ') || 'сумма длительностей ходов',
      delta: delta(agentMin, prev && sum(ownWork(prev), (s) => s.active_min), 'up'),
    },
    {
      key: 'sessions',
      label: whose === 'own' ? 'Ваши сессии' : 'Сессии',
      value: users.length,
      format: fmtN,
      sub: sessNote || 'без расписания, агентских и служебных',
      delta: delta(users.length, prev?.filter((s) => s.kind === 'user').length, 'up'),
    },
    {
      key: 'cost',
      label: 'Расходы',
      value: cost,
      format: (v) => `≈ ${fmtMoney(v)}`,
      sub: allCosted ? 'наблюдаемая оценка' : `у ${String(costed)} из ${String(S.length)} сессий`,
      delta: allCosted ? delta(cost, prevCost, 'down') : null,
      title:
        'Claude Code — по данным OTel, без него — оценка по цене API модели; Codex — оценка по условной цене API; фактически — подписки',
    },
    {
      key: 'cache',
      label: 'Попадание в кэш',
      value: tokIn ? tokCached / tokIn : null,
      format: pct,
      sub: `по ${String(withTokens.length)} ${plural(withTokens.length, 'сессии', 'сессиям', 'сессиям')} с токенами`,
      delta: null,
    },
    {
      key: 'errors',
      label: 'Ошибки инструментов',
      value: errors,
      format: pct,
      sub: `среди вызовов с известным исходом (${calls ? pct(known / calls) : '—'})`,
      delta: delta(errors, prev && errorShare(prev), 'down'),
    },
  ];
}
