import type { components } from '../../../shared/api';
import { fmtN, sec } from '../../../shared/lib/format';
import { Meter, Panel, Table, Td, Th } from '../../../shared/ui';
import { countIn, windowTimeNote, type Selection } from './count';

type CommandRow = components['schemas']['AnalyticsCommandRow'];

const TOP = 10;

// Minutes: one decimal below 10, whole from 10 on.
function minutes(value: number): string {
  return value < 10 ? value.toFixed(1).replace('.', ',') : String(Math.round(value));
}

// «Команды shell — куда ушло время»: the 10 commands of the chosen sessions with the most minutes.
export function Commands({
  commands,
  selection,
}: {
  commands: readonly CommandRow[];
  selection: Selection;
}) {
  const top = commands
    .map((row) => ({
      row,
      runs: countIn(row.runs, row.by_session, selection, 'runs') ?? 0,
      failed: countIn(row.failed, row.by_session, selection, 'failed') ?? 0,
    }))
    .filter((x) => x.runs > 0)
    .sort((a, b) => b.row.total_min - a.row.total_min)
    .slice(0, TOP);
  const most = Math.max(...top.map((x) => x.row.total_min), 0.01);

  return (
    <Panel
      title="Команды shell — куда ушло время"
      sub={`топ-10 по суммарному времени${windowTimeNote(selection)}`}
      className="tools-wide"
      style={{ gap: 8 }}
    >
      <Table>
        <thead>
          <tr>
            <Th>Команда</Th>
            <Th>Проект</Th>
            <Th numeric>Запусков</Th>
            <Th numeric>Упало</Th>
            <Th numeric>p95, с</Th>
            <Th style={{ width: 240 }}>Всего, мин</Th>
          </tr>
        </thead>
        <tbody>
          {top.map(({ row, runs, failed }) => (
            <tr key={`${row.project}:${row.cmd}`}>
              <Td className="mono">{row.cmd}</Td>
              <Td className="soft">{row.project || '—'}</Td>
              <Td numeric>{fmtN(runs)}</Td>
              <Td numeric className={failed ? 't-bad' : 'muted'}>
                {fmtN(failed)}
              </Td>
              <Td numeric>{sec(row.p95_s)}</Td>
              <Td>
                <div className="tools-minutes m4">
                  <Meter value={row.total_min / most} label={`${row.cmd}: минут`} />
                  <span className="r">{minutes(row.total_min)}</span>
                </div>
              </Td>
            </tr>
          ))}
          {!top.length && (
            <tr>
              <td colSpan={6} className="empty">
                Нет данных
              </td>
            </tr>
          )}
        </tbody>
      </Table>
    </Panel>
  );
}
