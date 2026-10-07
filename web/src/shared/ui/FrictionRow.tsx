import type { ReactNode } from 'react';

import { estMoney, fmtN } from '../lib/format';
import { SevDot } from './Bits';
import { Tag } from './Tag';

type FrictionRowProps = {
  sev: 'bad' | 'warn' | 'info';
  name: string;
  /** Episodes in the sample; null when the signal has no episode count. */
  eps: number | null;
  sessions: number;
  /** The id of the topic this signal belongs to; with it the row links to the topic. */
  topic?: string;
  onTopic?: (topic: string) => void;
};

// A friction signal on the Overview (README v5.1 «Обзор»): severity dot · name · «79 эп.» · «8 сесс.» · «тема →».
export function FrictionRow({ sev, name, eps, sessions, topic, onTopic }: FrictionRowProps) {
  return (
    <div className="frm">
      <SevDot tone={sev} />
      <span>{name}</span>
      <span className="r num">{eps == null ? '—' : `${fmtN(eps)} эп.`}</span>
      <span className="r num muted">{`${String(sessions)} сесс.`}</span>
      <span className="r">
        {topic != null && onTopic && (
          <button
            type="button"
            className="link"
            onClick={() => {
              onTopic(topic);
            }}
          >
            тема →
          </button>
        )}
      </span>
    </div>
  );
}

type CostRowProps = {
  agent: 'claude' | 'codex';
  /** The first prompt, the title or the short id: one line, the whole of it in the title. */
  text: string;
  cost: number | null;
  /** The cost basis: `otel_reported` goes without «≈». */
  basis: string | null;
  onOpen: () => void;
};

// A row of «Самые дорогие сессии»: the whole row is a button that opens the session feed.
export function CostRow({ agent, text, cost, basis, onOpen }: CostRowProps) {
  return (
    <button type="button" onClick={onOpen}>
      <Tag tone={agent}>{agent === 'claude' ? 'CC' : 'CX'}</Tag>
      <span className="t" title={text}>
        {text}
      </span>
      <span className="num">{estMoney(cost, basis)}</span>
    </button>
  );
}

// The list the CostRow buttons stand in.
export function CostList({ children }: { children: ReactNode }) {
  return <div className="top6">{children}</div>;
}
