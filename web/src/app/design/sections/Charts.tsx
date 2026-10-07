import { AreaLine, Gantt, Grid, Legend, Panel, PulseBars, StackedBars } from '../../../shared/ui';
import type { GanttData, PulseBar } from '../../../shared/ui';

const DAYS = Array.from({ length: 14 }, (_, i) => `${String(18 + i).padStart(2, '0')}.09`);
const usd = (v: number) => `$${v.toFixed(2)}`;
const hours = (v: number) => `${v.toFixed(1)} ч`;
const CLAUDE = DAYS.map((_, i) => (i % 4) * 0.5 + 0.3);
const CODEX = DAYS.map((_, i) => (i % 3) * 0.2);

// Synthetic pulse: a working Claude and Codex over the last 10 minutes, then the same quiet for 4.
const PULSE_NOW = Date.now();
const pulse = (quietSlots: number): PulseBar[] =>
  Array.from({ length: 60 - quietSlots }, (_, i) => i + quietSlots).flatMap((ago) => {
    const t = new Date(PULSE_NOW - ago * 10_000).toISOString();
    return [
      { t, agent: 'claude' as const, n: (ago * 7) % 13 },
      { t, agent: 'codex' as const, n: (ago * 5) % 9 },
    ];
  });

// A synthetic 2-hour session for the timeline: skills, a review, subagents, MCP and three prompts.
const G0 = Date.UTC(2026, 8, 30, 6, 0);
const g = (minute: number) => new Date(G0 + minute * 60_000).toISOString();
const GANTT: GanttData = {
  from: g(0),
  to: g(125),
  rows: [
    { name: 'skill', segs: [{ from: g(2), to: g(9), c: 'skill', label: 'чтение SKILL.md' }] },
    {
      name: 'team-skills:frontend',
      segs: [
        { from: g(10), to: g(40), c: 'skill', label: 'вёрстка' },
        { from: g(70), to: g(70.2), c: 'skill', label: 'короткое открытие' },
      ],
    },
    { name: 'code-review', segs: [{ from: g(41), to: g(55), c: 'review', label: 'проверка' }] },
    { name: 'субагенты', segs: [{ from: g(56), to: g(80), c: 'subagent', label: '2 агента' }] },
    { name: 'mcp · itsaplan', segs: [{ from: g(81), to: g(84), c: 'mcp', label: 'карточки' }] },
    { name: 'без skills', segs: [{ from: g(85), to: g(125), c: 'noskill', label: 'правки' }] },
  ],
  marks: [
    { at: g(1), text: '«Сверстай пульс»', line: 12 },
    { at: g(56), text: '«Проверь типы»', line: 340 },
    { at: g(110), text: '«Готово?»', line: 902 },
  ],
};

export function ChartsSection() {
  return (
    <Grid>
      <Panel span={8} className="chart" title="Расходы по дням" sub="StackedBars · оценка">
        <StackedBars
          ariaLabel="Расходы по дням"
          labels={DAYS}
          format={usd}
          series={[
            { name: 'gpt-demo', values: DAYS.map((_, i) => (i % 3) * 0.4 + 0.2) },
            { name: 'claude-demo', values: DAYS.map((_, i) => (i % 4) * 0.3) },
          ]}
        />
        <Legend
          items={[
            { label: 'gpt-demo · по прайсу', color: 'var(--m1)' },
            { label: 'claude-demo · по прайсу', color: 'var(--m2)' },
          ]}
        />
      </Panel>
      <Panel span={4} title="Стоимость нарастающим итогом" sub="AreaLine">
        <AreaLine
          ariaLabel="Стоимость нарастающим итогом"
          points={[
            [0, 0],
            [1, 0.03],
            [5, 0.045],
            [9, 0.06],
          ]}
          marks={[5]}
          format={usd}
          formatX={(x) => `${String(x)} м`}
        />
      </Panel>
      <Panel span={8} title="Расходы по дням · v5.1" sub="StackedBars · axis=max · slotTitle">
        <StackedBars
          ariaLabel="Расходы по дням, v5.1"
          labels={DAYS}
          colors={['var(--m1)', 'var(--m3)']}
          format={(v) => (v ? `≈ ${usd(v)}` : '$0')}
          axis="max"
          slotTitle={(i) =>
            `${DAYS[i] ?? ''} · Claude ${usd(CLAUDE[i] ?? 0)} · Codex ≈ ${usd(CODEX[i] ?? 0)}`
          }
          series={[
            { name: 'Claude', values: CLAUDE },
            { name: 'Codex', values: CODEX },
          ]}
        />
        <Legend
          items={[
            { label: 'Claude Code · по данным OTel', color: 'var(--m1)' },
            { label: 'Codex · оценка по цене API', color: 'var(--m3)' },
          ]}
        />
      </Panel>
      <Panel span={4} title="Кто работал: агент и вы" sub="StackedBars · layout=grouped">
        <StackedBars
          ariaLabel="Кто работал"
          layout="grouped"
          labels={DAYS.slice(0, 7)}
          colors={['var(--m1)', 'var(--m4)']}
          format={hours}
          series={[
            { name: 'агент', values: [0.4, 1.2, 0.8, 0, 2.1, 1.5, 0.6] },
            { name: 'вы', values: [0.2, 0.5, 0.3, 0, 0.9, 0.4, 0.3] },
          ]}
        />
        <Legend
          items={[
            { label: 'агент', color: 'var(--m1)' },
            { label: 'вы: паузы «ответ → реплика» ≤ 30 мин', color: 'var(--m4)' },
          ]}
        />
      </Panel>
      <Panel span={12} title="Пульс" sub="PulseBars · работа, тишина, ошибка">
        <PulseBars bars={pulse(0)} now={PULSE_NOW} active={2} />
        <PulseBars bars={pulse(24)} now={PULSE_NOW} active={0} />
        <PulseBars bars={[]} now={PULSE_NOW} active={0} />
        <PulseBars bars={pulse(0)} now={PULSE_NOW} active={0} error="Пульс не ответил: 503" />
      </Panel>
      <Panel span={12} title="Хронология сессии" sub="Gantt · клик по реплике открывает ленту">
        <Legend
          items={[
            { label: 'skill', color: 'var(--m2)' },
            { label: 'review', color: 'var(--m4)' },
            { label: 'субагенты', color: 'var(--m3)' },
            { label: 'MCP', color: 'var(--m1)' },
            { label: 'без skills', color: 'var(--ok)' },
            { label: 'ваши реплики', color: 'var(--bad)' },
          ]}
        />
        <Gantt data={GANTT} onMark={() => undefined} />
      </Panel>
      <Panel span={6} title="Пустые состояния">
        <StackedBars
          ariaLabel="Пусто"
          labels={DAYS.slice(0, 3)}
          format={usd}
          series={[{ name: 'x', values: [0, 0, 0] }]}
        />
        <AreaLine ariaLabel="Пусто" points={[[0, 1]]} format={usd} formatX={String} />
      </Panel>
    </Grid>
  );
}
