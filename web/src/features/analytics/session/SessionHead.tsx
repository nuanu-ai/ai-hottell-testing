import { useState, type ReactNode } from 'react';

import type { components } from '../../../shared/api';
import { estMoney, fmtDuration, fmtN, fmtOpen, pct, plural } from '../../../shared/lib/format';
import { formatWhen } from '../../../shared/lib/time';
import { SessionId, Tag } from '../../../shared/ui';
import type { Whose } from '../model/filters';
import { costTitle, KINDS } from '../sessions/labels';

type Session = components['schemas']['AnalyticsSession'];

const LONG_TITLE = 160;

function unknownShare(s: Session): string {
  return s.calls ? pct(s.unknown_results / s.calls) : '—';
}

type StatItem = { label: string; value: ReactNode; title?: string };

function stats(s: Session, own: boolean): StatItem[] {
  return [
    { label: 'Стоимость', value: estMoney(s.cost_usd, s.cost_basis), title: costTitle(s) },
    { label: 'Работа агента', value: fmtDuration(s.active_min), title: 'сумма ходов' },
    { label: 'Открыта', value: fmtOpen(s.wall_min), title: 'от первого до последнего события' },
    {
      label: own ? 'Ваше время' : 'Время человека',
      value: fmtDuration(s.user_min),
      title: 'паузы «ответ → реплика» ≤ 30 мин',
    },
    { label: own ? 'Ваших реплик' : 'Реплик человека', value: fmtN(s.prompts) },
    { label: 'Запросов к модели', value: fmtN(s.reqs) },
    { label: 'Вызовов', value: fmtN(s.calls) },
    {
      label: 'Без результата',
      value: unknownShare(s),
      title: `${fmtN(s.unknown_results)} ${plural(s.unknown_results, 'вызов', 'вызова', 'вызовов')} без PostToolUse`,
    },
  ];
}

type SessionHeadProps = {
  session: Session;
  // «Обсудить эту сессию»: the buttons and the command line come with E4.
  discussSlot?: ReactNode;
  // Whose the session is, by its owner: «Ваше…» only on the viewer's own (HT-444).
  whose?: Whose;
};

// The head of a session's feed (reference renderSession): the first prompt in two lines, the
// metadata line and eight figures.
export function SessionHead({ session: s, discussSlot, whose = 'own' }: SessionHeadProps) {
  const [open, setOpen] = useState(false);
  const title = s.first || s.title || s.short;
  const long = title.length > LONG_TITLE;
  const start = formatWhen(s.start);
  const end = formatWhen(s.end);
  const kind = KINDS[s.kind];

  return (
    <>
      <div className="sess-top">
        <div className="sess-h">
          <h1 className={long && open ? undefined : 'clamp'} title={title}>
            {title}
          </h1>
          {long && (
            <button
              type="button"
              className="link sess-more"
              onClick={() => {
                setOpen(!open);
              }}
            >
              {open ? 'свернуть' : 'полностью'}
            </button>
          )}
          <div className="meta">
            <Tag tone={s.agent === 'claude' ? 'claude' : 'codex'}>
              {s.agent === 'claude' ? 'Claude Code' : 'Codex'}
            </Tag>
            <span>
              {s.project}
              {s.branch && (
                <>
                  {' · '}
                  <span className="mono">{s.branch}</span>
                </>
              )}
            </span>
            <SessionId id={s.id} />
            <span title={`${start.title} → ${end.title}`}>
              {start.text} → {end.text}
            </span>
            <span>{s.model ?? 'модель неизвестна'}</span>
            <span className={kind.cls} title={s.kind_reason ?? ''}>
              {kind.text}
            </span>
            {s.sources.hooks === 'partial' && <Tag tone="warn">записан только хвост</Tag>}
          </div>
        </div>
        {discussSlot && (
          <div className="sess-d">
            <span className="muted">Обсудить эту сессию</span>
            {discussSlot}
          </div>
        )}
      </div>
      <div className="stats">
        {stats(s, whose === 'own').map((item) => (
          <div key={item.label} className="st" title={item.title}>
            <span className="l">{item.label}</span>
            <span className="v">{item.value}</span>
          </div>
        ))}
      </div>
    </>
  );
}
