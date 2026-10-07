import { useId, useState, type ReactNode } from 'react';

import { SevDot } from './Bits';
import { classNames } from './classNames';
import { Tag } from './Tag';

type TopicCardProps = {
  // The topic's id: the card becomes the anchor `topic-<id>` a link scrolls to.
  id?: string;
  sev: 'bad' | 'warn' | 'info';
  // The kind of the topic as a short tag; none leaves the tag out.
  kind?: ReactNode;
  title: string;
  // The price on the right: `lead` in bold, then the rest as is, e.g. « · 8 сессий · ≈ $7.86».
  price: { lead: string; rest?: string };
  // Why the topic matters, on one line; the full text goes to the title.
  why: string;
  // Buttons in the second row («Обсудить…», «Не проблема»).
  actions?: ReactNode;
  // A full-width row under the buttons, as the command line of «Обсудить» (E4).
  command?: ReactNode;
  // The open evidence: the wider left column and the right one.
  left?: ReactNode;
  right?: ReactNode;
  // Whether the evidence is open. Left out, the card keeps it itself; given, the screen does
  // and hears each click through onToggle.
  open?: boolean;
  onToggle?: () => void;
  // A 1.2 s accent bar on the left: the card the user has just been brought to.
  flash?: boolean;
};

// A topic of «Что исправить» (README v5.1 «4. Темы — карточка»), collapsed to two rows; the
// toggle opens the evidence and the state underneath.
export function TopicCard({
  id,
  sev,
  kind,
  title,
  price,
  why,
  actions,
  command,
  left,
  right,
  open,
  onToggle,
  flash,
}: TopicCardProps) {
  const openId = useId();
  const [ownOpen, setOwnOpen] = useState(false);
  const isOpen = open ?? ownOpen;

  function toggle() {
    if (open === undefined) setOwnOpen((value) => !value);
    onToggle?.();
  }

  return (
    <article className={classNames('topic', flash && 'flash')} id={id ? `topic-${id}` : undefined}>
      <div className="t-top">
        <div className="t-h">
          <SevDot tone={sev} />
          {kind != null && <Tag tone="acc">{kind}</Tag>}
          <span className="ttl" title={title}>
            {title}
          </span>
        </div>
        <span className="t-price">
          <b>{price.lead}</b>
          {price.rest}
        </span>
        <span className="t-why" title={why}>
          {why}
        </span>
        <div className="t-acts">{actions}</div>
        {command != null && <div className="cmdslot">{command}</div>}
      </div>
      <button
        type="button"
        className="t-tog"
        aria-expanded={isOpen}
        aria-controls={isOpen ? openId : undefined}
        onClick={toggle}
      >
        {isOpen ? '▾ Свернуть' : '▸ Доказательства и состояние'}
      </button>
      {isOpen && (
        <div className="t-open" id={openId}>
          <div className="t-l">{left}</div>
          <div className="t-r">{right}</div>
        </div>
      )}
    </article>
  );
}
