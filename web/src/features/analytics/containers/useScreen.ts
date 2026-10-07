import { useQuery } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { useCallback } from 'react';

import { meQueryOptions } from '../../../shared/api';
import { hiddenTopicsQuery } from '../api/hiddenTopics';
import { useDataset } from '../api/useDataset';
import { useAnalyticsFilters, type AnalyticsFilters } from '../model/filters';

export { withFilters } from '../model/filters';
import { sample } from '../model/sample';

const noneHidden: ReadonlySet<string> = new Set();

/** «Всё» is 0 days for the screens that count in days. */
export const daysOf = (filters: Pick<AnalyticsFilters, 'days'>) =>
  filters.days === 'all' ? 0 : filters.days;

/**
 * What every analytics screen starts from: the dataset of the URL's filters, the page's sample
 * (README v5.1 «База подсчётов»), the hidden topics and a navigation by href.
 */
export function useScreen() {
  const { filters, setFilter } = useAnalyticsFilters();
  const { data, error } = useDataset();
  const { data: hidden = noneHidden } = useQuery(hiddenTopicsQuery);
  const { data: meData } = useQuery(meQueryOptions);
  const navigate = useNavigate();
  const go = useCallback(
    (href: string) => {
      void navigate({ href });
    },
    [navigate],
  );
  const X = data ? sample(data, filters) : undefined;
  return { filters, setFilter, data, error, X, hidden, go, me: meData?.id };
}
