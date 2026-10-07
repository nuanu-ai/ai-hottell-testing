import { Axes, EvidenceRows, Label, Snippet, type EvidenceItem } from '../../../shared/ui';
import type { Proposal } from './api';
import { lineOf } from '../deep/model';
import { DecisionButtons } from './DecisionButtons';
import { axesOf } from './model';

// L<n> is a transcript line, not a feed event: a row opens the feed by the source line of its
// events (HT-410).
function evidenceOf(p: Proposal): EvidenceItem[] {
  return p.sources.flatMap((s) => {
    const lines = s.evidence.map(lineOf).filter((line): line is number => line !== null);
    return lines.length > 0
      ? lines.map((src) => ({ sid: s.session_id, src, text: s.task_id }))
      : [{ sid: s.session_id, text: s.task_id }];
  });
}

// The open proposal (README v5.1 «Раскрытие»), left: what and where it is seen, what to check
// before the change.
export function ProposalEvidence({
  proposal: p,
  onOpenLine,
}: {
  proposal: Proposal;
  // Opens the session feed at a transcript line of the evidence (L<n>), or from the start.
  onOpenLine: (sid: string, src: number | undefined, owner?: string) => void;
}) {
  const evidence = evidenceOf(p);
  return (
    <>
      {p.cause && <p className="muted">{p.cause}</p>}
      {p.expected_effect && <p className="muted">Ожидаемый эффект: {p.expected_effect}</p>}
      <Label>Где видно · {evidence.length}</Label>
      <EvidenceRows
        items={evidence}
        onOpen={(sid, _line, src) => {
          onOpenLine(sid, src, p.user_id);
        }}
      />
      {p.preconditions.length > 0 && (
        <>
          <Label>Проверить до правки</Label>
          <ul className="pre-list">
            {p.preconditions.map((item, index) => (
              <li key={index}>{item}</li>
            ))}
          </ul>
        </>
      )}
    </>
  );
}

// Right: the four axes, the owner's decision, the change candidate, how to check the benefit and
// the history of the axes.
export function ProposalState({ proposal: p, owner }: { proposal: Proposal; owner: boolean }) {
  return (
    <>
      <Axes items={axesOf(p)} />
      {owner && <DecisionButtons proposal={p} />}
      <Label>Кандидат правки</Label>
      <Snippet where={p.target.locator || undefined} text={p.change ?? undefined} />
      <Label>Как проверить пользу</Label>
      <p>{p.verification || '—'}</p>
      {p.rollback && <p className="muted">Откат: {p.rollback}</p>}
      {p.history_review.length > 0 && (
        <div className="history">
          <Label>История</Label>
          <ul>
            {p.history_review.map((item, index) => (
              <li key={index}>{item}</li>
            ))}
          </ul>
        </div>
      )}
    </>
  );
}
