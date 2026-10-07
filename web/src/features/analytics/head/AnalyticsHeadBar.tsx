import { useEveryonePage } from '../api/useDataset';
import { FiltersBar } from './FiltersBar';
import { PersonHead } from './PersonHead';
import { Pulse } from './Pulse';
import { RefreshButton } from './RefreshButton';
import { SendStatus } from './SendStatus';

// The head's second row on the analytics tabs: another person's name, the filters on the left, the
// live state on the right.
export function AnalyticsHeadBar() {
  const everyone = useEveryonePage();
  return (
    <>
      {!everyone && <PersonHead />}
      <FiltersBar hidePerson={everyone} />
      <div className="row2-right">
        <Pulse />
        <SendStatus />
        <RefreshButton />
      </div>
    </>
  );
}
