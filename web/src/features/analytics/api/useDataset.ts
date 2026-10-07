import { useQuery } from '@tanstack/react-query';
import { useMatches } from '@tanstack/react-router';

import { meQueryOptions } from '../../../shared/api';
import { personOf, useAnalyticsFilters } from '../model/filters';
import { projectOf } from '../model/sample';
import { datasetQuery, teamQuery } from './queries';

/** The person the page is about: the URL's, the signed-in one by default, undefined for everyone. */
export function usePerson() {
  return usePersonState().user;
}

// known is false only while the signed-in person is still loading and the URL names nobody:
// asking then would fetch everyone's data first and the person's right after.
export function usePersonState() {
  const { filters } = useAnalyticsFilters();
  const { data: me } = useQuery(meQueryOptions);
  return { user: personOf(filters, me?.id), known: filters.user !== undefined || me !== undefined };
}

/** The page is about everyone («Команда»): its route says so, and ?user= does not count there. */
export function useEveryonePage() {
  return useMatches({ select: (matches) => matches.some((match) => match.staticData.everyone) });
}

/** The dataset for the filters in the URL: one request serves every tab and the head. */
export function useDataset({ enabled = true }: { enabled?: boolean } = {}) {
  const { filters } = useAnalyticsFilters();
  const { user, known } = usePersonState();
  return useQuery({ ...datasetQuery({ days: filters.days, user }), enabled: enabled && known });
}

/** «Команда» for the filters in the URL, whatever person ?user= names. */
export function useTeam({ enabled = true }: { enabled?: boolean } = {}) {
  const { filters } = useAnalyticsFilters();
  return useQuery({ ...teamQuery(filters), enabled });
}

/** What the filters of the head offer on the current page. */
export type PageFacets = {
  /** The projects of the page's data, unsorted with repeats; undefined until it arrives. */
  projects: string[] | undefined;
  /** The data has a service session, so their filter matters. */
  hasSystem: boolean;
  /** The data on screen is the previous request's while the current one is on its way. */
  isPlaceholderData: boolean;
};

/** The facets of the current page: «Команда»'s from its answer, the person's from their dataset. */
export function usePageFacets(): PageFacets {
  const everyone = useEveryonePage();
  const personal = useDataset({ enabled: !everyone });
  const team = useTeam({ enabled: everyone });
  if (everyone) {
    return {
      projects: team.data?.projects,
      hasSystem: team.data?.has_system ?? false,
      isPlaceholderData: team.isPlaceholderData,
    };
  }
  const sessions = personal.data?.sessions;
  return {
    projects: sessions?.map(projectOf),
    hasSystem: sessions?.some((s) => s.kind === 'system') ?? false,
    isPlaceholderData: personal.isPlaceholderData,
  };
}
