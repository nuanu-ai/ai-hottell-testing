import type { components } from '../../../shared/api';
import { fmtN, plural } from '../../../shared/lib/format';
import { Legend, More, Panel, StackedBars, Table, Td, Th } from '../../../shared/ui';

import type { Whose } from '../model/filters';
import { ddmm, periodDays } from './days';

type Session = components['schemas']['AnalyticsSession'];

const dec1 = (v: number) => v.toFixed(1).replace('.', ',');

// Who is next to the agent: «вы» only on the viewer's own data (HT-444).
const WHO: Record<Whose, string> = { own: 'вы', other: 'человек', all: 'люди' };

function WorkByDay({ sessions, days, end, whose = 'own' }: WorkAndGitProps) {
  const who = WHO[whose];
  const title = `Кто работал: агент и ${who}`;
  const period = periodDays(sessions, days, end);
  const index = new Map(period.map((d, i) => [d, i]));
  const agent = period.map(() => 0);
  const you = period.map(() => 0);
  for (const s of sessions) {
    if (s.kind === 'automation' || s.kind === 'agent') continue;
    // The server lays daily out by the browser's days (tz of the dataset, HT-514).
    for (const d of s.daily) {
      const i = index.get(d.date);
      if (i === undefined) continue;
      agent[i] = (agent[i] ?? 0) + d.agent_min / 60;
      you[i] = (you[i] ?? 0) + (d.user_min ?? 0) / 60;
    }
  }
  return (
    <Panel span={6} title={title} sub="часы в день">
      <StackedBars
        ariaLabel={title}
        layout="grouped"
        labels={period.map(ddmm)}
        series={[
          { name: 'агент', values: agent },
          { name: who, values: you },
        ]}
        colors={['var(--m1)', 'var(--m4)']}
        format={(v) => `${dec1(v)} ч`}
        slotTitle={(i) =>
          `${ddmm(period[i] ?? '')} · агент ${dec1(agent[i] ?? 0)} ч · ${who} ${dec1(you[i] ?? 0)} ч`
        }
      />
      <Legend
        items={[
          { label: 'агент · без расписания', color: 'var(--m1)' },
          { label: `${who} · паузы «ответ → реплика» ≤ 30 мин`, color: 'var(--m4)' },
        ]}
      />
    </Panel>
  );
}

type GitRow = {
  project: string;
  commits: number;
  prs: number;
  added: number | null;
  removed: number | null;
};

function gitByProject(sessions: readonly Session[]): GitRow[] {
  const rows = new Map<string, GitRow>();
  for (const s of sessions) {
    const row = rows.get(s.project) ?? {
      project: s.project,
      commits: 0,
      prs: 0,
      added: null,
      removed: null,
    };
    row.commits += s.commits;
    row.prs += s.prs;
    if (s.added != null) {
      row.added = (row.added ?? 0) + s.added;
      row.removed = (row.removed ?? 0) + (s.removed ?? 0);
    }
    rows.set(s.project, row);
  }
  return [...rows.values()].filter((r) => r.commits || r.prs).sort((a, b) => b.commits - a.commits);
}

function GitResult({ sessions }: { sessions: readonly Session[] }) {
  const rows = gitByProject(sessions);
  return (
    <Panel span={6} className="ov-top" title="Результат в git" sub="коммиты с признаком успеха">
      <Table>
        <thead>
          <tr>
            <Th>Проект</Th>
            <Th numeric>Коммиты</Th>
            <Th numeric>PR</Th>
            <Th numeric>Строк +/−</Th>
          </tr>
        </thead>
        <tbody>
          {rows.length ? (
            rows.map((r) => (
              <tr key={r.project}>
                <Td>{r.project}</Td>
                <Td numeric>{fmtN(r.commits)}</Td>
                <Td numeric>{fmtN(r.prs)}</Td>
                <Td numeric>
                  {r.added == null ? (
                    '—'
                  ) : (
                    <>
                      <span className="ok">+{fmtN(r.added)}</span>{' '}
                      <span className="bad">−{fmtN(r.removed)}</span>
                    </>
                  )}
                </Td>
              </tr>
            ))
          ) : (
            <tr>
              <Td colSpan={4} wrap className="muted">
                В этих сессиях агент не делал коммитов и PR.
              </Td>
            </tr>
          )}
        </tbody>
      </Table>
    </Panel>
  );
}

const PER_SESSION = /^[0-9a-f]{8}(…[0-9a-f]{4})?:\s*/;

/**
 * «Ограничения данных» of a page (reference gapsBlock without a session): the page's own notes
 * first, then the dataset's general gaps, then how many gaps name single sessions.
 */
function pageGaps(gaps: readonly string[], extra: readonly string[]): string[] {
  const general = gaps.filter((g) => !PER_SESSION.test(g));
  const perSession = gaps.length - general.length;
  return [
    ...extra,
    ...general,
    ...(perSession
      ? [
          `Ещё ${String(perSession)} ${plural(perSession, 'замечание', 'замечания', 'замечаний')} по отдельным сессиям — в ленте каждой сессии.`,
        ]
      : []),
  ];
}

export function OverviewGaps({
  gaps,
  pricingNote,
}: {
  gaps: readonly string[];
  pricingNote?: string;
}) {
  const items = pageGaps(gaps, pricingNote ? [pricingNote] : []);
  return (
    <More summary={`Ограничения данных · ${String(items.length)}`}>
      <ul>
        {items.map((item, i) => (
          <li key={i}>{item}</li>
        ))}
      </ul>
    </More>
  );
}

type WorkAndGitProps = {
  sessions: readonly Session[];
  days: number;
  end: string;
  whose?: Whose;
};

// «Обзор, 3»: who worked by day (span 6) and the result in git (span 6).
export function WorkAndGit(props: WorkAndGitProps) {
  return (
    <>
      <WorkByDay {...props} />
      <GitResult sessions={props.sessions} />
    </>
  );
}
