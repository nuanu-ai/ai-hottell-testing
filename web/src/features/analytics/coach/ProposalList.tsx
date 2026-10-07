import { useState, type ReactNode } from 'react';

import { discussActions } from '../../../shared/lib/discuss';
import { Button, DiscussActions, Empty, TopicCard } from '../../../shared/ui';
import type { Proposal } from './api';
import { ProposalAxes } from './ProposalAxes';
import {
  kindLabel,
  recurrenceText,
  sessionsOf,
  sessionsText,
  severityOf,
  sortProposals,
  titleOf,
} from './model';

const FOLDED = 3;

type Details = { left?: ReactNode; right?: ReactNode };

// The registry as v5.1 topics: important first, then by sessions; three cards, the rest behind
// «Ещё N». «Обсудить» asks the coach about the proposal's p2:… id over the filter's period.
export function ProposalList({
  proposals,
  days,
  details,
  canDiscuss = () => true,
}: {
  proposals: readonly Proposal[];
  // The period of the filter; null is the whole period.
  days: number | null;
  // The open card's left and right columns; without it the right one shows the axes.
  details?: (p: Proposal) => Details;
  // «Обсудить» runs the coach on the viewer's machine: only on their own proposals (HT-444).
  canDiscuss?: (p: Proposal) => boolean;
}) {
  const [all, setAll] = useState(false);
  if (proposals.length === 0) {
    return (
      <Empty title="Предложений пока нет">
        <p>Они появляются после опубликованного глубокого разбора сессии.</p>
      </Empty>
    );
  }
  const sorted = sortProposals(proposals);
  const shown = all ? sorted : sorted.slice(0, FOLDED);
  return (
    <div className="topics">
      {shown.map((p) => {
        const open: Details = details?.(p) ?? { right: <ProposalAxes proposal={p} /> };
        const recurrence = recurrenceText(p);
        return (
          <TopicCard
            key={`${p.user_id}:${p.id}`}
            id={p.id}
            sev={severityOf(p)}
            kind={kindLabel(p.kind)}
            title={titleOf(p)}
            price={{ lead: sessionsText(sessionsOf(p)), rest: ` · ${p.user_name}` }}
            why={recurrence ?? p.cause ?? ''}
            actions={
              canDiscuss(p) && (
                <DiscussActions
                  size="sm"
                  actions={discussActions({ mode: 'coach', subject: p.id, days })}
                />
              )
            }
            left={open.left}
            right={open.right}
          />
        );
      })}
      {sorted.length > FOLDED && (
        <Button
          variant="link"
          onClick={() => {
            setAll((value) => !value);
          }}
        >
          {all ? `Свернуть до ${String(FOLDED)}` : `Ещё ${String(sorted.length - FOLDED)}`}
        </Button>
      )}
    </div>
  );
}
