import { Link } from '@tanstack/react-router';
import { Fragment, useState } from 'react';

import { fmtDuration, fmtMoney, fmtN, pct } from '../../../shared/lib/format';
import { formatWhen } from '../../../shared/lib/time';
import { Sparkline, Table, TableRow, Tag, Td, Th } from '../../../shared/ui';
import { filtersToSearch, withFilters, type AnalyticsFilters } from '../model/filters';
import type { TeamRow } from './rows';

type Key =
  | 'name'
  | 'sessions'
  | 'userMin'
  | 'agentMin'
  | 'cost'
  | 'errorRate'
  | 'frictionSessions'
  | 'lastActive';

type Sort = { key: Key; dir: 'ascending' | 'descending' };

const COLUMNS: { key: Key; title: string; numeric: boolean }[] = [
  { key: 'name', title: 'Человек', numeric: false },
  { key: 'sessions', title: 'Сессий', numeric: true },
  { key: 'userMin', title: 'Время человека', numeric: true },
  { key: 'agentMin', title: 'Агент работал', numeric: true },
  { key: 'cost', title: 'Расходы', numeric: true },
  { key: 'errorRate', title: 'Ошибки инструментов', numeric: true },
  { key: 'frictionSessions', title: 'Сессий с трением', numeric: true },
  { key: 'lastActive', title: 'Последняя активность', numeric: true },
];

const hasSessions = (row: TeamRow) => row.lastActive !== null;

function valueOf(row: TeamRow, key: Exclude<Key, 'name'>): number | null {
  if (key === 'lastActive') return row.lastActive === null ? null : Date.parse(row.lastActive);
  return row[key];
}

/**
 * The rows in the chosen order. People without sessions stay last whatever the column, and an
 * unknown value goes after the known ones in both directions; ties keep the default order.
 */
function ordered(rows: readonly TeamRow[], sort: Sort | null): TeamRow[] {
  const withSessions = rows.filter(hasSessions);
  const without = rows.filter((row) => !hasSessions(row));
  if (!sort) return [...withSessions, ...without];
  const sign = sort.dir === 'ascending' ? 1 : -1;
  const compare = (a: TeamRow, b: TeamRow) => {
    if (sort.key === 'name') return sign * a.name.localeCompare(b.name, 'ru');
    const x = valueOf(a, sort.key);
    const y = valueOf(b, sort.key);
    if (x === null || y === null) return Number(x === null) - Number(y === null);
    return sign * (x - y);
  };
  const by = (list: TeamRow[]) => [...list].sort(compare);
  return [...by(withSessions), ...(sort.key === 'name' ? by(without) : without)];
}

function costText(row: TeamRow): { text: string; title?: string } {
  if (row.cost === null) return { text: '—' };
  return row.costPartial
    ? { text: `≥ ${fmtMoney(row.cost)}`, title: 'стоимость известна не у всех сессий' }
    : { text: `≈ ${fmtMoney(row.cost)}` };
}

/** The filters of a person's «Обзор»: the page's, with the person; the signed-in one is the default. */
const personFilters = (filters: AnalyticsFilters, row: TeamRow): AnalyticsFilters => ({
  ...filters,
  user: row.isMe ? undefined : row.userId,
});

type TeamTableProps = {
  rows: readonly TeamRow[];
  // The page's filters: a row leads to «Обзор» of its person with them (HT-470).
  filters: AnalyticsFilters;
  onNavigate?: (href: string) => void;
};

// «Команда» (HT-466): one row per person with the «Обзор» numbers over their sessions and the pulse
// of the 5 days before today (HT-540); a click on a column head but the pulse's sorts by it, up then
// down. Numbers sit right in the mono face; time is local, UTC in the title. A row opens its
// person's «Обзор»; the name is the link for a new tab.
export function TeamTable({ rows, filters, onNavigate }: TeamTableProps) {
  const [sort, setSort] = useState<Sort | null>(null);
  const toggle = (key: Key) => {
    setSort((current) =>
      current?.key === key && current.dir === 'ascending'
        ? { key, dir: 'descending' }
        : { key, dir: 'ascending' },
    );
  };

  return (
    <Table>
      <thead>
        <tr>
          {COLUMNS.map((column) => (
            <Fragment key={column.key}>
              <Th
                numeric={column.numeric}
                aria-sort={sort?.key === column.key ? sort.dir : undefined}
              >
                <button
                  type="button"
                  className="sort"
                  onClick={() => {
                    toggle(column.key);
                  }}
                >
                  {column.title}
                </button>
              </Th>
              {column.key === 'name' && (
                <Th className="pulse">
                  Пульс<span className="sub">последние 5 дней</span>
                </Th>
              )}
            </Fragment>
          ))}
        </tr>
      </thead>
      <tbody>
        {ordered(rows, sort).map((row) => {
          const cost = costText(row);
          const last = formatWhen(row.lastActive);
          const person = personFilters(filters, row);
          return (
            <TableRow
              key={row.userId}
              href={withFilters('/overview', person)}
              onNavigate={onNavigate}
            >
              <Td cellTitle>
                <Link to="/overview" search={filtersToSearch(person)}>
                  {row.name}
                </Link>
                {row.isMe && <Tag tone="acc">это вы</Tag>}
                {!hasSessions(row) && <span className="sub">нет сессий за период</span>}
              </Td>
              {row.pulse ? (
                <Td className="pulse">
                  <Sparkline values={row.pulse} />
                </Td>
              ) : (
                <Td className="pulse" title="нет данных">
                  —
                </Td>
              )}
              <Td numeric className="mono">
                {fmtN(row.sessions)}
              </Td>
              <Td numeric className="mono">
                {fmtDuration(row.userMin)}
              </Td>
              <Td numeric className="mono">
                {fmtDuration(row.agentMin)}
              </Td>
              <Td numeric className="mono" title={cost.title}>
                {cost.text}
              </Td>
              <Td numeric className="mono">
                {pct(row.errorRate)}
              </Td>
              <Td numeric className="mono">
                {fmtN(row.frictionSessions)}
              </Td>
              <Td numeric className="mono" title={last.title || undefined}>
                {last.text}
              </Td>
            </TableRow>
          );
        })}
      </tbody>
    </Table>
  );
}
