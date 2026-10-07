import { Cycle } from '../../../shared/ui';
import type { ImprovementCycle } from './api';
import { EMPTY_HINT, journalCycleSteps } from './journal';

// Steps 4–6 of the improvement cycle on their own; «Что исправить» puts journalCycleSteps after
// its steps 1–3 in one Cycle.
export function CycleSteps({
  cycle,
  selected,
  onSelect,
}: {
  cycle: ImprovementCycle;
  selected: string | null;
  onSelect: (key: string) => void;
}) {
  return (
    <Cycle
      steps={journalCycleSteps(cycle)}
      selected={selected}
      onSelect={onSelect}
      hint={cycle.empty ? EMPTY_HINT : undefined}
    />
  );
}
