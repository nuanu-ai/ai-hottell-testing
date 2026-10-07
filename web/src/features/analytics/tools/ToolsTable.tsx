import { useState } from 'react';

import type { components } from '../../../shared/api';
import { fmtN, sec } from '../../../shared/lib/format';
import { OutcomeBar, OutcomeLegend, Panel, Table, Tag, Td, Th } from '../../../shared/ui';
import { countIn, windowTimeNote, type Selection } from './count';

type ToolRow = components['schemas']['AnalyticsToolRow'];

// The table shows this many tools until «Показать все».
const FIRST = 15;

type Counted = { tool: ToolRow; calls: number; errors: number; unknown: number | null };

// An MCP tool reads as «server · tool»: mcp__github__get_issue → github · get_issue.
function toolName(tool: ToolRow): string {
  return tool.kind === 'mcp'
    ? tool.name
        .replace(/^mcp__/, '')
        .split('__')
        .join(' · ')
    : tool.name;
}

function countTools(tools: readonly ToolRow[], selection: Selection): Counted[] {
  return tools
    .map((tool) => ({
      tool,
      calls: countIn(tool.calls, tool.by_session, selection, 'calls') ?? 0,
      errors: countIn(tool.errors, tool.by_session, selection, 'errors') ?? 0,
      unknown: countIn(tool.unknown, tool.by_session, selection, 'unknown'),
    }))
    .filter((row) => row.calls > 0)
    .sort((a, b) => b.calls - a.calls);
}

// renderTools of the v5.1 reference: tools of the chosen sessions with their outcome.
export function ToolsTable({
  tools,
  selection,
}: {
  tools: readonly ToolRow[];
  selection: Selection;
}) {
  const [all, setAll] = useState(false);
  const counted = countTools(tools, selection);
  const shown = all ? counted : counted.slice(0, FIRST);
  const notSplit = counted.some((row) => row.unknown === null);

  return (
    <Panel
      title={`Инструменты · ${String(counted.length)}`}
      sub={selection.full ? undefined : `p50 и p95${windowTimeNote(selection)}`}
      action={<OutcomeLegend notSplit={notSplit} />}
      style={{ gap: 8 }}
    >
      <Table>
        <thead>
          <tr>
            <Th>Инструмент</Th>
            <Th numeric>Вызовов</Th>
            <Th style={{ width: 170 }}>Исход</Th>
            <Th numeric>Ошибок</Th>
            <Th numeric>Нет рез.</Th>
            <Th numeric>p50, с</Th>
            <Th numeric>p95, с</Th>
          </tr>
        </thead>
        <tbody>
          {shown.map(({ tool, calls, errors, unknown }) => (
            <tr key={`${tool.kind}:${tool.name}`}>
              <Td>
                <div className="tool-name">
                  <span className="mono">{toolName(tool)}</span>
                  {tool.kind === 'mcp' && <Tag tone="plain">MCP</Tag>}
                  {tool.kind === 'nested' && <Tag tone="plain">внутри exec</Tag>}
                </div>
              </Td>
              <Td numeric>{fmtN(calls)}</Td>
              <Td>
                <OutcomeBar calls={calls} errors={errors} unknown={unknown} />
              </Td>
              <Td numeric className={errors ? 't-bad' : 'muted'}>
                {fmtN(errors)}
              </Td>
              <Td numeric className="soft">
                {fmtN(unknown)}
              </Td>
              <Td numeric className="soft">
                {sec(tool.p50_s)}
              </Td>
              <Td numeric>{sec(tool.p95_s)}</Td>
            </tr>
          ))}
          {!counted.length && (
            <tr>
              <td colSpan={7} className="empty">
                Нет вызовов
              </td>
            </tr>
          )}
        </tbody>
      </Table>
      {counted.length > FIRST && (
        <div>
          <button
            type="button"
            className="link"
            onClick={() => {
              setAll(!all);
            }}
          >
            {all ? `Свернуть до ${String(FIRST)}` : `Показать все ${String(counted.length)}`}
          </button>
        </div>
      )}
    </Panel>
  );
}
