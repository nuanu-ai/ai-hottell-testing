import type { components } from '../../../shared/api';
import { fmtMoney, plural } from '../../../shared/lib/format';
import { Legend, Panel, StackedBars } from '../../../shared/ui';

import { ddmm, periodDays, periodText } from './days';

type Session = components['schemas']['AnalyticsSession'];

type SpendProps = {
  sessions: readonly Session[];
  days: number;
  // The end of the dataset window: the last day of the period.
  end: string;
};

const sum = (list: readonly Session[]) => list.reduce((acc, s) => acc + (s.cost_usd ?? 0), 0);

function SpendByDay({ sessions, days, end }: SpendProps) {
  const period = periodDays(sessions, days, end);
  const index = new Map(period.map((d, i) => [d, i]));
  // A day's cost of an agent stays null (unknown) until some session of it records one; a day
  // without sessions of the agent costs nothing.
  const claude: (number | null)[] = period.map(() => 0);
  const codex: (number | null)[] = period.map(() => 0);
  const known = new Set<string>();
  for (const s of sessions) {
    // The server lays daily out by the browser's days (tz of the dataset, HT-514).
    for (const d of s.daily) {
      const i = index.get(d.date);
      if (i === undefined) continue;
      const target = s.agent === 'claude' ? claude : codex;
      const key = `${s.agent}:${String(i)}`;
      if (d.cost_usd == null) {
        if (!known.has(key)) target[i] = null;
        continue;
      }
      target[i] = (known.has(key) ? (target[i] ?? 0) : 0) + d.cost_usd;
      known.add(key);
    }
  }
  const costed = sessions.filter((s) => s.cost_usd != null);
  const total = costed.length ? `≈ ${fmtMoney(sum(costed))}` : '—';
  return (
    <Panel span={8} title="Расходы по дням" sub={`${total} ${periodText(days)}`}>
      <StackedBars
        ariaLabel="Расходы по дням"
        labels={period.map(ddmm)}
        series={[
          { name: 'Codex', values: codex.map((v) => v ?? 0) },
          { name: 'Claude Code', values: claude.map((v) => v ?? 0) },
        ]}
        colors={['var(--m3)', 'var(--m1)']}
        axis="max"
        format={(v) => (v ? `≈ ${fmtMoney(v)}` : '$0')}
        slotTitle={(i) =>
          `${ddmm(period[i] ?? '')} · Claude ${fmtMoney(claude[i])} · Codex ${codex[i] == null ? '—' : `≈ ${fmtMoney(codex[i])}`}`
        }
      />
      <Legend
        items={[
          { label: 'Claude Code · по данным OTel или оценка по цене модели', color: 'var(--m1)' },
          { label: 'Codex · оценка по цене API', color: 'var(--m3)' },
        ]}
      />
    </Panel>
  );
}

type ProjectSpend = { project: string; cost: number; known: number; n: number };

function spendByProject(sessions: readonly Session[]): ProjectSpend[] {
  const groups = new Map<string, Session[]>();
  for (const s of sessions) groups.set(s.project, [...(groups.get(s.project) ?? []), s]);
  return [...groups]
    .map(([project, list]) => {
      const known = list.filter((s) => s.cost_usd != null);
      return { project, cost: sum(known), known: known.length, n: list.length };
    })
    .sort((a, b) => b.cost - a.cost);
}

function SpendByProject({ sessions }: { sessions: readonly Session[] }) {
  const rows = spendByProject(sessions);
  const max = Math.max(...rows.map((p) => p.cost), 1e-9);
  return (
    <Panel span={4} title="Куда уходят деньги" sub="по проектам">
      {rows.length ? (
        <div className="list">
          {rows.map((p) => (
            <div
              key={p.project}
              className="li"
              title={`стоимость записана у ${String(p.known)} из ${String(p.n)} ${plural(p.n, 'сессии', 'сессий', 'сессий')}`}
            >
              <span className="t">{p.project}</span>
              <div className="bar">
                <i style={{ width: `${((p.cost / max) * 100).toFixed(1)}%` }} />
              </div>
              <span className="r num spend-v">
                {p.known ? `≈ ${fmtMoney(p.cost)}` : '—'}
                {p.known > 0 && p.known < p.n && (
                  <span className="muted spend-k">{`${String(p.known)}/${String(p.n)}`}</span>
                )}
              </span>
            </div>
          ))}
        </div>
      ) : (
        <div className="empty">Нет сессий</div>
      )}
    </Panel>
  );
}

// «Обзор, 3»: spend by day (span 8) and by project (span 4).
export function Spend(props: SpendProps) {
  return (
    <>
      <SpendByDay {...props} />
      <SpendByProject sessions={props.sessions} />
    </>
  );
}
