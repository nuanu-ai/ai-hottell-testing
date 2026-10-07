import type { components } from '../../../shared/api';
import { fmtN, sec } from '../../../shared/lib/format';
import { Panel } from '../../../shared/ui';
import { countIn, windowTimeNote, type Selection } from './count';

type McpRow = components['schemas']['AnalyticsMcpRow'];
type PermissionRow = components['schemas']['AnalyticsPermissionRow'];

type ToolsAsideProps = {
  mcp: readonly McpRow[];
  permissions: readonly PermissionRow[];
  selection: Selection;
};

// A permission row without per-session counts keeps its total, as in the reference.
function permissionCount(row: PermissionRow, selection: Selection): number {
  if (!Object.keys(row.by_session).length) return row.count;
  return countIn(row.count, row.by_session, selection, 'count') ?? 0;
}

// The right column of «Инструменты и MCP»: MCP servers and who decided on permissions.
export function ToolsAside({ mcp, permissions, selection }: ToolsAsideProps) {
  const servers = mcp
    .map((row) => ({
      row,
      calls: countIn(row.calls, row.by_session, selection, 'calls') ?? 0,
      errors: countIn(row.errors, row.by_session, selection, 'errors') ?? 0,
    }))
    .filter((x) => x.calls > 0)
    .sort((a, b) => b.calls - a.calls);
  const decided = permissions
    .map((row) => ({ row, count: permissionCount(row, selection) }))
    .filter((x) => x.count > 0)
    .sort((a, b) => b.count - a.count);

  return (
    <div className="tools-aside">
      <Panel
        title="MCP-серверы"
        sub={`вызовов · ошибок · ср., с${windowTimeNote(selection)}`}
        style={{ gap: 8 }}
      >
        {servers.length ? (
          <ul className="tools-rows">
            {servers.map(({ row, calls, errors }) => (
              <li key={row.server} className="tools-mcp num">
                <span className="t">{row.server}</span>
                <span className="r">{fmtN(calls)}</span>
                <span className={errors ? 'r t-bad' : 'r muted'}>{fmtN(errors)}</span>
                <span className="r soft">{sec(row.avg_s)}</span>
              </li>
            ))}
          </ul>
        ) : (
          <div className="empty">Нет данных</div>
        )}
      </Panel>
      <Panel title="Разрешения" sub="кто решил" style={{ gap: 8 }}>
        {decided.length ? (
          <ul className="tools-rows">
            {decided.map(({ row, count }) => (
              <li key={row.label} className="tools-perm">
                <span>{row.label}</span>
                <span className="r num">{fmtN(count)}</span>
              </li>
            ))}
          </ul>
        ) : (
          <div className="empty">Нет данных</div>
        )}
      </Panel>
    </div>
  );
}
