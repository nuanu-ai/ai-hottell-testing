import { Axes } from '../../../shared/ui';
import type { Proposal } from './api';
import { axesOf } from './model';

// The four axes of a proposal in Russian: Готовность, Решение, Применение, Эффект.
export function ProposalAxes({ proposal }: { proposal: Proposal }) {
  return <Axes items={axesOf(proposal)} />;
}
