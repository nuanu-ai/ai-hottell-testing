import { Fragment, type ReactNode } from 'react';

import { Panel, Tag } from '../../shared/ui';
import { agents, summary, type AgentId, type TelemetrySettings } from './model';

const title = (agent: AgentId) => agents.find((item) => item.id === agent)?.title ?? agent;

function offTag(agent: AgentId) {
  return (
    <Tag key={agent} tone="bad">
      {title(agent)} выключен
    </Tag>
  );
}

// «a · b · c»: the parts of the line with the dot between them.
function joined(parts: ReactNode[]) {
  return parts.map((part, index) => (
    <Fragment key={index}>
      {index > 0 && ' · '}
      {part}
    </Fragment>
  ));
}

type SummaryProps = {
  // The settings saved, not the draft: the save bar tells what the draft changes.
  settings: TelemetrySettings;
  // A status after the line, such as whether the binary applied the settings.
  children?: ReactNode;
};

/** One line above the tabs: what the saved settings send now, an agent that is off as a tag. */
export function Summary({ settings, children }: SummaryProps) {
  const { on, off, sent, denied } = summary(settings);

  let line: ReactNode;
  if (on.length === 0) {
    line = <>Ничего не отправляется: {joined(off.map(offTag))}</>;
  } else if (off.length === 0 && denied.length === 0) {
    line = <Tag tone="ok">Отправляется всё</Tag>;
  } else {
    line = joined([
      `Отправляется: ${on.map(title).join(' и ')}`,
      sent.length > 0 ? sent.join(', ') : 'источники выключены',
      ...off.map(offTag),
      ...(denied.length > 0 ? [`запрещено: ${denied.join(', ')}`] : []),
    ]);
  }

  return (
    <Panel aria-label="Что сейчас уходит">
      <p>
        {line}
        {children && <> · {children}</>}
      </p>
    </Panel>
  );
}
