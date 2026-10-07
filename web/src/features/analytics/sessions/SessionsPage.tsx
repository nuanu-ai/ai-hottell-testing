import type { components } from '../../../shared/api';
import { estMoney, fmtDuration } from '../../../shared/lib/format';
import { formatWhen } from '../../../shared/lib/time';
import { costTitle, KINDS } from './labels';
import { DataLimits, Panel, TableRow, Tag, Td, Th, View, type TagTone } from '../../../shared/ui';
import { pageGaps } from '../model/gaps';

type Session = components['schemas']['AnalyticsSession'];
type Friction = components['schemas']['AnalyticsFriction'];

// Начало · Агент · [Человек] · Тип · Проект · Задача (the rest) · Работа · Реплик · Ошибок · $ · Сигналы.
const LEAD = [96, 104] as const;
const PERSON = 140;
const TAIL = [104, 140, undefined, 80, 64, 68, 76, 240] as const;

function openTitle(wallMin: number | null): string {
  return `открыта ${wallMin != null && wallMin >= 1440 ? 'больше суток' : fmtDuration(wallMin)}`;
}

function signalTone(sev: Friction['sev'] | undefined): TagTone {
  return sev === 'bad' ? 'bad' : sev === 'warn' ? 'warn' : 'plain';
}

export type SessionsPageProps = {
  // The page's sample: the sessions under the current filter, «Без служебных» applied.
  sessions: readonly Session[];
  // The same filter before «Без служебных»: the system sessions it hides are counted from here.
  allSessions?: readonly Session[];
  kind?: 'work' | 'all';
  onShowSystem?: () => void;
  // ?flag=: only the sessions with this signal.
  flag?: string;
  onClearFlag?: () => void;
  // «Все люди»: the column «Человек», names from GET /users by the session's user_id.
  showPerson?: boolean;
  userNames?: ReadonlyMap<string, string>;
  friction: readonly Friction[];
  // The dataset's gaps: «Ограничения данных» at the end of the page.
  gaps: readonly string[];
  sessionHref: (session: Session) => string;
  onNavigate?: (href: string) => void;
};

const hasFlag = (s: Session, flag: string | undefined) => !flag || s.flags.includes(flag);

// «Сессии» (README v5.1, reference renderSessions): the sample's sessions, newest first; a row
// opens the session's feed. «Ограничения данных» close the page.
export function SessionsPage({
  sessions,
  allSessions = sessions,
  kind = 'work',
  onShowSystem,
  flag,
  onClearFlag,
  showPerson = false,
  userNames,
  friction,
  gaps,
  sessionHref,
  onNavigate,
}: SessionsPageProps) {
  const signals = new Map(friction.map((f) => [f.key, f]));
  const rows = sessions
    .filter((s) => hasFlag(s, flag))
    .sort((a, b) => Date.parse(b.start) - Date.parse(a.start));
  const hidden =
    kind === 'work' ? allSessions.filter((s) => s.kind === 'system' && hasFlag(s, flag)).length : 0;
  const flagName = flag ? (signals.get(flag)?.name ?? flag) : undefined;
  const title = flagName
    ? `Сессии · ${flagName} · ${String(rows.length)}`
    : `Сессии · ${String(rows.length)}`;
  const columns = [...LEAD, ...(showPerson ? [PERSON] : []), ...TAIL];
  const limits = pageGaps(gaps);

  return (
    <View>
      <Panel>
        <div className="ph sess-ph">
          <h2>{title}</h2>
          {hidden > 0 && (
            <button type="button" className="link" onClick={onShowSystem}>
              + {hidden} служебных
            </button>
          )}
          {flag && (
            <button type="button" className="link" onClick={onClearFlag}>
              все сессии
            </button>
          )}
          <span className="sub">время местное · UTC в подсказке · клик — лента</span>
        </div>
        <div className="tbl">
          <table className="sesst">
            <colgroup>
              {columns.map((width, i) => (
                <col key={i} style={width === undefined ? undefined : { width }} />
              ))}
            </colgroup>
            <thead>
              <tr>
                <Th>Начало</Th>
                <Th>Агент</Th>
                {showPerson && <Th>Человек</Th>}
                <Th>Тип</Th>
                <Th>Проект</Th>
                <Th>Задача</Th>
                <Th numeric>Работа</Th>
                <Th numeric>Реплик</Th>
                <Th numeric>Ошибок</Th>
                <Th numeric>$</Th>
                <Th>Сигналы</Th>
              </tr>
            </thead>
            <tbody>
              {rows.map((s) => (
                <SessionRow
                  key={`${s.user_id}:${s.agent}:${s.id}`}
                  session={s}
                  signals={signals}
                  person={showPerson ? (userNames?.get(s.user_id) ?? s.user_name) : undefined}
                  href={sessionHref(s)}
                  onNavigate={onNavigate}
                />
              ))}
              {rows.length === 0 && (
                <tr>
                  <td colSpan={columns.length} className="empty">
                    Нет сессий под этот фильтр
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </Panel>
      <DataLimits items={limits.items} count={limits.count} />
    </View>
  );
}

type SessionRowProps = {
  session: Session;
  signals: ReadonlyMap<string, Friction>;
  person?: string;
  href: string;
  onNavigate?: (href: string) => void;
};

function SessionRow({ session: s, signals, person, href, onNavigate }: SessionRowProps) {
  const when = formatWhen(s.start);
  const kind = KINDS[s.kind];
  const task = s.first || s.title || '—';
  const flags = s.flags.map((key) => {
    const f = signals.get(key);
    return { name: f?.name ?? key, tone: signalTone(f?.sev) };
  });
  const [head, ...rest] = flags;
  const sys = s.kind === 'system' ? 'sys' : undefined;

  return (
    <TableRow href={href} onNavigate={onNavigate}>
      <Td className={sys ? `mono ${sys}` : 'mono'} title={when.title}>
        {when.text}
      </Td>
      <Td className={sys}>
        <Tag tone={s.agent === 'claude' ? 'claude' : 'codex'}>
          {s.agent === 'claude' ? 'Claude Code' : 'Codex'}
        </Tag>
      </Td>
      {person !== undefined && (
        <Td className={sys ? `ell ${sys}` : 'ell'} title={person}>
          {person}
        </Td>
      )}
      <Td className={sys} title={s.kind_reason ?? ''}>
        <span className={kind.cls}>{kind.text}</span>
      </Td>
      <Td className={sys ? `ell ${sys}` : 'ell'} title={s.project}>
        {s.project}
      </Td>
      <Td className={sys ? `ell ${sys}` : 'ell'}>
        <span title={task}>{task}</span>
      </Td>
      <Td numeric className={sys} title={openTitle(s.wall_min)}>
        {fmtDuration(s.active_min)}
      </Td>
      <Td numeric className={sys}>
        {s.prompts}
      </Td>
      <Td numeric className={s.errors ? 'bad' : sys}>
        {s.errors}
      </Td>
      <Td numeric className={sys}>
        <span title={costTitle(s)}>{estMoney(s.cost_usd, s.cost_basis)}</span>
      </Td>
      <Td className={sys} title={flags.map((f) => f.name).join(', ')}>
        <div className="flags">
          {head && <Tag tone={head.tone}>{head.name}</Tag>}
          {rest.length > 0 && <Tag tone="plain">+{rest.length}</Tag>}
        </div>
      </Td>
    </TableRow>
  );
}
