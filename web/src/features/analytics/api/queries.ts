import { keepPreviousData, queryOptions, replaceEqualDeep } from '@tanstack/react-query';

import { apiClient, unwrap, type components } from '../../../shared/api';
import { browserZone } from '../../../shared/lib/time';
import { allPeople, type AnalyticsFilters, type Period } from '../model/filters';

type TeamFilters = Pick<AnalyticsFilters, 'days' | 'agent' | 'project' | 'kind'>;

type Schemas = components['schemas'];
export type Dataset = Schemas['AnalyticsDataset'];
export type Session = Schemas['AnalyticsSession'];
export type Friction = Schemas['AnalyticsFriction'];
export type Finding = Schemas['AnalyticsFinding'];
export type SessionDetail = Schemas['AnalyticsTimeline'];
export type AnalyticsEvent = Schemas['AnalyticsEvent'];
export type Agent = Schemas['AnalyticsAgent'];
export type Team = Schemas['AnalyticsTeam'];
export type TeamPerson = Schemas['AnalyticsTeamPerson'];

export const analyticsKeys = {
  all: ['analytics'] as const,
  dataset: (days: number, user: string | undefined, tz: string) =>
    [...analyticsKeys.all, 'dataset', days, user ?? 'all', tz] as const,
  team: (filters: TeamFilters) =>
    [
      ...analyticsKeys.all,
      'team',
      filters.days,
      filters.agent,
      filters.project,
      filters.kind,
    ] as const,
  session: (id: string, agent?: Agent, user?: string) =>
    [...analyticsKeys.all, 'session', id, agent ?? '', user ?? ''] as const,
};

// The widest window the API serves; «Всё» asks for it.
export const allDays = 365;

/**
 * The days the dataset is asked for: twice the period, so the page also has the previous
 * period for «к прошлым 7 дн». Agent, project and service sessions are filtered in the browser
 * (epic decision 3), so one answer serves every value of them.
 */
export function requestDays(days: Period): number {
  return days === 'all' ? allDays : Math.min(days * 2, allDays);
}

// Once a minute while the tab is visible; a hidden tab does not poll.
export const datasetRefetchMs = 60_000;

/**
 * The dataset is built on request, so generated_at changes on every answer. An answer equal to the
 * one in the cache but for generated_at keeps the cached object: the screen does not redraw and
 * «Обновить» can tell «Без изменений».
 */
export function shareDataset(previous: unknown, next: unknown): unknown {
  if (isDataset(previous) && isDataset(next) && sameButGeneratedAt(previous, next)) {
    return previous;
  }
  return replaceEqualDeep(previous, next);
}

function isDataset(value: unknown): value is Dataset {
  return typeof value === 'object' && value !== null && 'generated_at' in value;
}

// replaceEqualDeep returns its first argument when both are deeply equal.
function sameButGeneratedAt(previous: Dataset, next: Dataset): boolean {
  const base = { ...previous, generated_at: '' };
  return replaceEqualDeep(base, { ...next, generated_at: '' }) === base;
}

/** user is one person; undefined or allPeople asks for everyone, with no user in the request. */
export const datasetQuery = (filters: Pick<AnalyticsFilters, 'days' | 'user'>) => {
  const days = requestDays(filters.days);
  const user = filters.user === allPeople ? undefined : filters.user;
  // The server lays each session's daily out by the browser's days (HT-514).
  const tz = browserZone();
  return queryOptions({
    queryKey: analyticsKeys.dataset(days, user, tz),
    queryFn: ({ signal }) =>
      unwrap(
        apiClient.GET('/analytics/dataset', {
          params: {
            query: {
              days,
              kind: ['system', 'agent', 'automation', 'user'],
              tz,
              ...(user ? { user } : {}),
            },
          },
          // The contract takes kind as one comma-separated value (style form, explode false).
          querySerializer: { array: { style: 'form', explode: false } },
          signal,
        }),
      ),
    refetchInterval: datasetRefetchMs,
    refetchIntervalInBackground: false,
    // A change of period keeps the old numbers on screen until the new ones arrive.
    placeholderData: keepPreviousData,
    structuralSharing: shareDataset,
  });
};

/**
 * «Команда» (HT-531): each person's numbers for the period, counted on the server from the dataset
 * of everyone; agent, project and kind choose the sessions there, so each value is its own request.
 */
export const teamQuery = (filters: TeamFilters) =>
  queryOptions({
    queryKey: analyticsKeys.team(filters),
    queryFn: ({ signal }) =>
      unwrap(
        apiClient.GET('/analytics/team', {
          params: {
            query: {
              ...(filters.days === 'all' ? {} : { days: filters.days }),
              ...(filters.agent === 'all' ? {} : { agent: filters.agent }),
              ...(filters.project === 'all' ? {} : { project: filters.project }),
              ...(filters.kind === 'all' ? { kind: 'all' as const } : {}),
            },
          },
          signal,
        }),
      ),
    refetchInterval: datasetRefetchMs,
    refetchIntervalInBackground: false,
    placeholderData: keepPreviousData,
    structuralSharing: shareDataset,
  });

export const sessionQuery = (id: string, scope: { agent?: Agent; user?: string } = {}) =>
  queryOptions({
    queryKey: analyticsKeys.session(id, scope.agent, scope.user),
    queryFn: ({ signal }) =>
      unwrap(
        apiClient.GET('/analytics/sessions/{id}', {
          params: {
            path: { id },
            query: {
              ...(scope.agent ? { agent: scope.agent } : {}),
              ...(scope.user ? { user: scope.user } : {}),
            },
          },
          signal,
        }),
      ),
  });

// The head pulse: every 5 s while the tab is visible; a poll replaces a failed one, so no retry.
export const pulseRefetchMs = 5_000;

export const pulseQuery = (user: string | undefined) =>
  queryOptions({
    queryKey: [...analyticsKeys.all, 'pulse', user ?? 'all'] as const,
    queryFn: ({ signal }) =>
      unwrap(
        apiClient.GET('/analytics/pulse', {
          params: { query: user ? { user } : {} },
          signal,
        }),
      ),
    refetchInterval: pulseRefetchMs,
    refetchIntervalInBackground: false,
    retry: false,
  });
