import type { ReactNode } from 'react';

import type { components } from '../../../shared/api';
import { fmtN } from '../../../shared/lib/format';
import { MeterList, More, Panel, Tag } from '../../../shared/ui';

import { sessionGaps, sourceRows, toolLabel } from './sources';

type Session = components['schemas']['AnalyticsSession'];
type AnalyticsTimeline = components['schemas']['AnalyticsTimeline'];

const TOP_TOOLS = 10;

function SessionTools({ tools }: { tools: AnalyticsTimeline['tools'] | undefined }) {
  const top = Object.entries(tools ?? {})
    .sort((a, b) => b[1] - a[1])
    .slice(0, TOP_TOOLS);
  const max = top[0]?.[1] ?? 1;
  return (
    <Panel title="Инструменты в сессии" className="sess-side">
      {top.length ? (
        <MeterList
          items={top.map(([name, count]) => ({
            label: (
              <span className="mono" title={name}>
                {toolLabel(name)}
              </span>
            ),
            value: count / max,
            text: fmtN(count),
          }))}
        />
      ) : (
        <div className="empty">{tools ? 'Нет вызовов' : '—'}</div>
      )}
    </Panel>
  );
}

function SessionSources({ session }: { session: Session }) {
  return (
    <Panel title="Откуда данные" className="sess-side">
      <div className="srcs">
        {sourceRows(session).map((row) => (
          <div key={row.label} className="src">
            <span className="l">{row.label}</span>
            <span>
              <Tag tone={row.tone}>{row.tag}</Tag>
            </span>
            <span className="n">{row.note}</span>
          </div>
        ))}
      </div>
    </Panel>
  );
}

type SessionAsideProps = {
  session: Session;
  timeline: AnalyticsTimeline | undefined;
  // The feed panel (Timeline) on the left.
  feed: ReactNode;
};

// The body of a session's feed: the feed on the left (8fr), the tools and the sources on the right
// (4fr); one column from 900px down.
export function SessionAside({ session, timeline, feed }: SessionAsideProps) {
  return (
    <div className="g84">
      {feed}
      <div className="col16">
        <SessionTools tools={timeline?.tools} />
        <SessionSources session={session} />
      </div>
    </div>
  );
}

// «Ограничения данных» of one session.
export function SessionGaps({ gaps, id }: { gaps: readonly string[]; id: string }) {
  const items = sessionGaps(gaps, id);
  return (
    <More summary={`Ограничения данных · ${String(items.length)}`}>
      <ul>
        {(items.length ? items : ['Для этой сессии отдельных замечаний нет.']).map((item, i) => (
          <li key={i}>{item}</li>
        ))}
      </ul>
    </More>
  );
}
