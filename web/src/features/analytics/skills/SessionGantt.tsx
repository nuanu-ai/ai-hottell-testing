import type { components } from '../../../shared/api';
import { shortId } from '../../../shared/lib/format';
import { formatClock, formatWhen } from '../../../shared/lib/time';
import { Gantt, Legend, Panel } from '../../../shared/ui';
import type { Selection } from '../tools/count';

type Dataset = components['schemas']['AnalyticsDataset'];
type GanttData = components['schemas']['AnalyticsGantt'];

const LEGEND = [
  { label: 'skill', color: 'var(--m2)' },
  { label: 'субагенты', color: 'var(--m3)' },
  { label: 'MCP', color: 'var(--m1)' },
  { label: 'без skills', color: 'var(--ok)' },
  { label: 'ваши реплики', color: 'var(--bad)' },
];

function goTo(href: string) {
  window.location.assign(href);
}

type SessionGanttProps = {
  gantt: GanttData | undefined;
  sessions: Dataset['sessions'];
  selection: Selection;
  /** Opens the session feed; the route layer passes the router's navigation. */
  onNavigate?: (href: string) => void;
};

// ganttHTML of the reference: skills, subagents and MCP of one session over time. Shown only
// while that session is among the chosen ones; a prompt mark opens its feed at the line.
export function SessionGantt({ gantt, sessions, selection, onNavigate = goTo }: SessionGanttProps) {
  if (!gantt || !selection.ids.has(gantt.sid)) return null;
  const sid = gantt.sid;
  const project = sessions.find((s) => s.id === sid)?.project;
  const from = new Date(gantt.from);
  const to = new Date(gantt.to);
  const end =
    from.toDateString() === to.toDateString() ? formatClock(to) : formatWhen(gantt.to).text;
  const title = [`Сессия ${shortId(sid)}`, project, `${formatWhen(gantt.from).text}–${end}`]
    .filter(Boolean)
    .join(' · ');

  return (
    <Panel title={title} action={<Legend items={LEGEND} />} style={{ gap: 12 }}>
      <Gantt
        data={gantt}
        onMark={(line) => {
          onNavigate(`/sessions/${encodeURIComponent(sid)}?line=${String(line)}`);
        }}
      />
    </Panel>
  );
}
