import type { ReactNode } from 'react';

import type { components } from '../../../shared/api';
import { fmtN, plural } from '../../../shared/lib/format';
import { DataLimits, Meter, PageHead, Panel, Table, Tag, Td, Th } from '../../../shared/ui';
import { pageGaps } from '../model/gaps';
import type { Selection } from '../tools/count';
import { SessionGantt } from './SessionGantt';
import { skillGroup, skillName, unusedCount } from './skillGroups';
import './skills.css';

type Dataset = components['schemas']['AnalyticsDataset'];
type SkillRow = components['schemas']['AnalyticsSkillRow'];

type SkillsPageProps = {
  dataset: Pick<Dataset, 'skills' | 'sessions' | 'gaps'>;
  selection: Selection;
  // The period filter in days; 0 is the whole time.
  days: number;
  onNavigate?: (href: string) => void;
  // The blocks the screen adds under the page, above «Ограничения данных».
  children?: ReactNode;
};

// The page's own note in «Ограничения данных» (the v5.1 reference, gapsSk).
const skillsLimits = [
  'Активация — агент прочитал SKILL.md. «Известных» — открывавшиеся плюс skills из снимка без активаций.',
];

const dec1 = (value: number) => value.toFixed(1).replace('.', ',');

function periodText(days: number): string {
  return days ? `за ${String(days)} ${plural(days, 'день', 'дня', 'дней')}` : 'за всё время';
}

// Activations of the chosen sessions; a row without by_session keeps its total.
function activationsOf(row: SkillRow, selection: Selection): number {
  const entries = Object.entries(row.by_session);
  if (selection.full || !entries.length) return row.activations;
  return entries.reduce((n, [sid, k]) => n + (selection.ids.has(sid) ? k : 0), 0);
}

// «Skills» of v5.1 (renderSkills in the reference): which skills the agent opened in the chosen sessions.
// «Ограничения данных» close the page, after the screen's own blocks.
export function SkillsPage({ dataset, selection, days, onNavigate, children }: SkillsPageProps) {
  const { rows, available } = dataset.skills;
  const unused = unusedCount(rows);
  const used = rows
    .filter((r) => r.state !== 'unused' && r.sessions.some((sid) => selection.ids.has(sid)))
    .map((row) => ({ row, acts: activationsOf(row, selection) }))
    .sort((a, b) => b.acts - a.acts);
  const known = Math.max(
    available ?? 0,
    rows.filter((r) => r.state !== 'unused').length + (unused ?? 0),
  );
  const acts = used.reduce((n, x) => n + x.acts, 0);
  const ktok = used.some((x) => x.row.size_ktok != null)
    ? used.reduce((n, x) => n + (x.row.size_ktok ?? 0), 0)
    : null;
  const most = Math.max(...used.map((x) => x.acts), 1);

  const groups = new Map<string, { label: string; count: number; acts: number }>();
  for (const { row, acts: a } of used) {
    const label = skillGroup(row.source);
    const group = groups.get(label) ?? { label, count: 0, acts: 0 };
    group.count += 1;
    group.acts += a;
    groups.set(label, group);
  }
  const byGroup = [...groups.values()].sort((a, b) => b.acts - a.acts);
  const groupMost = Math.max(...byGroup.map((g) => g.acts), 1);
  const limits = pageGaps(dataset.gaps, skillsLimits);

  const heading = `${String(used.length)} ${plural(used.length, 'skill использовался', 'skills использовались', 'skills использовались')} из ${known ? String(known) : '—'} известных`;
  const sub = `${fmtN(acts)} ${plural(acts, 'активация', 'активации', 'активаций')} ${periodText(days)} · ${ktok == null ? '—' : `≈ ${dec1(ktok)}`} тыс. токенов инструкций открытых skills`;

  return (
    <div className="skills-page">
      <PageHead title={heading} sub={sub} />
      <div className="skills-grid">
        <Panel title="Открывались" sub="активация — агент прочитал SKILL.md" style={{ gap: 8 }}>
          <Table>
            <thead>
              <tr>
                <Th>Skill</Th>
                <Th>Источник</Th>
                <Th style={{ width: 200 }}>Активаций</Th>
                <Th numeric>Сессий</Th>
                <Th numeric>Тыс. ток.</Th>
              </tr>
            </thead>
            <tbody>
              {used.map(({ row, acts: a }) => (
                <tr key={`${row.source}:${row.name}`}>
                  <Td className="mono">{skillName(row)}</Td>
                  <Td>
                    <Tag tone="plain" title={row.source}>
                      {skillGroup(row.source)}
                    </Tag>
                  </Td>
                  <Td>
                    <div className="skills-acts m2">
                      <Meter value={a / most} label={`${skillName(row)}: активаций`} />
                      <span className="r">{fmtN(a)}</span>
                    </div>
                  </Td>
                  <Td numeric>{row.sessions.filter((sid) => selection.ids.has(sid)).length}</Td>
                  <Td numeric className="skills-soft">
                    {row.size_ktok != null ? dec1(row.size_ktok) : '—'}
                  </Td>
                </tr>
              ))}
              {!used.length && (
                <tr>
                  <td colSpan={5} className="empty">
                    В этих сессиях агент не открыл ни одного skill
                  </td>
                </tr>
              )}
            </tbody>
          </Table>
        </Panel>
        <div className="skills-aside">
          <Panel title="По источникам" sub="skills · активаций" style={{ gap: 10 }}>
            {byGroup.length ? (
              <ul className="skills-groups">
                {byGroup.map((g) => (
                  <li key={g.label} className="grp">
                    <span>{g.label}</span>
                    <span className="m2">
                      <Meter value={g.acts / groupMost} label={`${g.label}: активаций`} />
                    </span>
                    <span className="r muted num">{g.count}</span>
                    <span className="r num">{fmtN(g.acts)}</span>
                  </li>
                ))}
              </ul>
            ) : (
              <div className="empty">—</div>
            )}
          </Panel>
          <Panel style={{ gap: 6 }}>
            <span className="skills-label">Не открывались ни разу</span>
            <b className="big">{unused == null ? '—' : fmtN(unused)}</b>
            <span className="muted skills-note">
              описания всё равно попадают в контекст каждой сессии
            </span>
          </Panel>
        </div>
      </div>
      <SessionGantt
        gantt={dataset.skills.gantt}
        sessions={dataset.sessions}
        selection={selection}
        onNavigate={onNavigate}
      />
      {children}
      <DataLimits items={limits.items} count={limits.count} />
    </div>
  );
}
