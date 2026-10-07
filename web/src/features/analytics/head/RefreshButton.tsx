import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useState } from 'react';

import { Button } from '../../../shared/ui';
import { datasetQuery, teamQuery } from '../api/queries';
import { useEveryonePage, usePerson } from '../api/useDataset';
import { useAnalyticsFilters } from '../model/filters';

type RefreshState = 'idle' | 'busy' | 'same' | 'fail';

const labels: Record<RefreshState, string> = {
  idle: 'Обновить',
  busy: 'Обновляю…',
  same: 'Без изменений',
  fail: 'Не удалось',
};

// How long «Без изменений» and «Не удалось» stay before the button reads «Обновить» again.
export const refreshFlashMs = 2200;

// «Обновить» (README v5.1 «Шапка, строка 2, п. 3»): asks for the dataset now. New data replace the
// old in place; an answer equal but for generated_at keeps the screen and says «Без изменений».
export function RefreshButton() {
  const queryClient = useQueryClient();
  const { filters } = useAnalyticsFilters();
  const person = usePerson();
  // «Команда» reads its own answer, so that is the one to ask for again.
  const everyone = useEveryonePage();
  const [state, setState] = useState<RefreshState>('idle');

  useEffect(() => {
    if (state !== 'same' && state !== 'fail') {
      return;
    }
    const timer = setTimeout(() => {
      setState('idle');
    }, refreshFlashMs);
    return () => {
      clearTimeout(timer);
    };
  }, [state]);

  const refresh = async () => {
    const { queryKey } = everyone
      ? teamQuery(filters)
      : datasetQuery({ days: filters.days, user: person });
    const before = queryClient.getQueryData(queryKey);
    setState('busy');
    try {
      await queryClient.refetchQueries({ queryKey, exact: true }, { throwOnError: true });
    } catch {
      setState('fail');
      return;
    }
    setState(queryClient.getQueryData(queryKey) === before ? 'same' : 'idle');
  };

  return (
    <Button
      size="sm"
      disabled={state === 'busy'}
      onClick={() => {
        void refresh();
      }}
    >
      {labels[state]}
    </Button>
  );
}
