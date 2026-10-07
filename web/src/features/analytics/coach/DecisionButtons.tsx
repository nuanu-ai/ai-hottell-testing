import { Button, Notice } from '../../../shared/ui';
import { type Decision, type Proposal, useDecideProposal } from './api';

const BUTTONS: readonly (readonly [Decision, string])[] = [
  ['accepted', 'Принять'],
  ['rejected', 'Отклонить'],
  ['revision_requested', 'На доработку'],
];

// The owner's decision on a proposal. «Отклонить» is a decision, not a deletion, so it has no
// danger tone. After the change is applied the decision is history: the buttons give way to
// the applied version.
export function DecisionButtons({ proposal }: { proposal: Proposal }) {
  const decide = useDecideProposal();
  const { execution, decision } = proposal.axes;
  if (execution.status === 'applied') {
    return (
      <p className="muted">
        {execution.version ? `применено, версия ${execution.version}` : 'применено'}
      </p>
    );
  }
  return (
    <div>
      <div className="acts">
        {BUTTONS.map(([status, label]) => (
          <Button
            key={status}
            size="sm"
            variant={status === 'accepted' ? 'primary' : undefined}
            disabled={decide.isPending || decision.status === status}
            onClick={() => {
              decide.mutate({ proposal, status });
            }}
          >
            {label}
          </Button>
        ))}
      </div>
      {decide.isError && <Notice tone="err">Решение не записано: {decide.error.message}</Notice>}
    </div>
  );
}
