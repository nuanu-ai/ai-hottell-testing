import { useNavigate, useSearch } from '@tanstack/react-router';
import { useCallback } from 'react';

// The analytics filters live in the URL: a link keeps them, a reload restores them.
// Defaults follow v5.1 «State Management» and are never written into the URL.

export const periods = [7, 14, 30, 'all'] as const;
export type Period = (typeof periods)[number];
export type AgentFilter = 'all' | 'claude' | 'codex';
/** work hides the service sessions; all shows them too. */
export type KindFilter = 'work' | 'all';

export type AnalyticsFilters = {
  days: Period;
  agent: AgentFilter;
  /** all, or a project name as the dataset gives it. */
  project: string;
  kind: KindFilter;
  /** A user id, or all for everyone; absent means the signed-in person. */
  user: string | undefined;
};

/** The filters as the URL carries them: only the values that differ from the defaults. */
export type AnalyticsSearch = {
  days?: Exclude<Period, 7>;
  agent?: Exclude<AgentFilter, 'all'>;
  project?: string;
  kind?: Exclude<KindFilter, 'work'>;
  user?: string;
};

export const defaultFilters: AnalyticsFilters = {
  days: 7,
  agent: 'all',
  project: 'all',
  kind: 'work',
  user: undefined,
};

/** The user filter's value for «Все люди». */
export const allPeople = 'all';

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function parseDays(value: unknown): Period {
  if (value === 'all') {
    return 'all';
  }
  const days = typeof value === 'string' ? Number(value) : value;
  return periods.find((period) => period === days) ?? defaultFilters.days;
}

/** Reads any search object; an unknown or malformed value falls back to its default. */
export function parseAnalyticsSearch(search: Record<string, unknown>): AnalyticsFilters {
  const { agent, project, kind, user } = search;
  return {
    days: parseDays(search.days),
    agent: agent === 'claude' || agent === 'codex' ? agent : 'all',
    project: typeof project === 'string' && project !== '' ? project : 'all',
    kind: kind === 'all' ? 'all' : 'work',
    user: typeof user === 'string' && (user === allPeople || uuid.test(user)) ? user : undefined,
  };
}

export function filtersToSearch(filters: AnalyticsFilters): AnalyticsSearch {
  const search: AnalyticsSearch = {};
  if (filters.days !== 7) {
    search.days = filters.days;
  }
  if (filters.agent !== 'all') {
    search.agent = filters.agent;
  }
  if (filters.project !== 'all') {
    search.project = filters.project;
  }
  if (filters.kind !== 'work') {
    search.kind = filters.kind;
  }
  if (filters.user !== undefined) {
    search.user = filters.user;
  }
  return search;
}

/** The validateSearch of the analytics routes: the URL keeps only non-default filters. */
export function validateAnalyticsSearch(search: Record<string, unknown>): AnalyticsSearch {
  return filtersToSearch(parseAnalyticsSearch(search));
}

/**
 * The person the data is asked for: the one in the URL, else the signed-in person; undefined
 * under «Все люди». Every signed-in person sees everyone; the filter is not an access check.
 */
export function personOf(filters: Pick<AnalyticsFilters, 'user'>, meId: string | undefined) {
  if (filters.user === allPeople) {
    return undefined;
  }
  return filters.user ?? meId;
}

/** Whose data the page shows: the viewer's own, another person's or everyone's. */
export type Whose = 'own' | 'other' | 'all';

/** Whose data the filter asks for: no ?user= or the viewer's id is their own (HT-444). */
export function whoseOf(filters: Pick<AnalyticsFilters, 'user'>, meId: string | undefined): Whose {
  if (filters.user === allPeople) return 'all';
  return filters.user === undefined || filters.user === meId ? 'own' : 'other';
}

/**
 * Whose a session is, by its owner and not by the filter: a session without an owner predates
 * per-person data and is the viewer's; while the viewer is unknown, nobody's session is theirs.
 */
export function whoseSession(userId: string | null | undefined, meId: string | undefined): Whose {
  if (!userId) return 'own';
  return userId === meId ? 'own' : 'other';
}

/**
 * The owner of the session a timeline route shows: the dataset's session, else the link's
 * ?owner= (evidence) or ?session_user= (sessionKeys, as Pulse gives).
 */
export function sessionOwner(
  session: { user_id?: string | null } | undefined,
  search: Pick<SessionSearch, 'owner' | 'session_user'>,
): string | null | undefined {
  return session ? session.user_id : (search.owner ?? search.session_user);
}

/** The filters of the current page and a setter that writes one of them into the URL. */
export function useAnalyticsFilters() {
  const search = useSearch({ strict: false });
  const navigate = useNavigate();
  const filters = parseAnalyticsSearch(search);
  const setFilter = useCallback(
    <K extends keyof AnalyticsFilters>(key: K, value: AnalyticsFilters[K]) => {
      void navigate({
        to: '.',
        search: (prev: Record<string, unknown>) => ({
          ...prev,
          ...emptyFilters,
          ...filtersToSearch({ ...parseAnalyticsSearch(prev), [key]: value }),
        }),
      });
    },
    [navigate],
  );
  return { filters, setFilter };
}

// Clears every filter key, so a value set back to its default leaves the URL.
const emptyFilters: Record<keyof AnalyticsSearch, undefined> = {
  days: undefined,
  agent: undefined,
  project: undefined,
  kind: undefined,
  user: undefined,
};

/** The search of «Что исправить»: ?open=<topic id> opens that topic and brings it into view. */
export function validateFixSearch(search: Record<string, unknown>): { open?: string } {
  return typeof search.open === 'string' && search.open !== '' ? { open: search.open } : {};
}

/** The search of a session timeline (its own keys, the filters ride next to them). */
export type SessionSearch = {
  /** ?line=<N> brings the event N into view. */
  line?: number;
  /** ?src=<L>: a Deep L<n>, a line of the main transcript; brings the event built from it into view. */
  src?: number;
  /** The session's agent and person: a session is (user, agent, id), the id alone may repeat. */
  session_agent?: string;
  session_user?: string;
  /** ?owner=<user id>: whose session it is, when it may be outside the page's dataset (HT-405). */
  owner?: string;
};

export function validateSessionSearch(search: Record<string, unknown>): SessionSearch {
  const out: SessionSearch = {};
  const line = typeof search.line === 'string' ? Number(search.line) : search.line;
  if (typeof line === 'number' && Number.isInteger(line) && line > 0) out.line = line;
  const src = typeof search.src === 'string' ? Number(search.src) : search.src;
  if (typeof src === 'number' && Number.isInteger(src) && src > 0) out.src = src;
  if (typeof search.session_agent === 'string' && search.session_agent !== '') {
    out.session_agent = search.session_agent;
  }
  if (typeof search.session_user === 'string' && search.session_user !== '') {
    out.session_user = search.session_user;
  }
  if (typeof search.owner === 'string' && search.owner !== '') out.owner = search.owner;
  return out;
}

type SessionKey = { id: string; agent: string; user_id?: string | null };

/** The keys a link to a session's timeline carries besides its id. */
export function sessionKeys(
  session: Pick<SessionKey, 'agent' | 'user_id'>,
): Record<string, string> {
  return {
    session_agent: session.agent,
    ...(session.user_id ? { session_user: session.user_id } : {}),
  };
}

/** The session of a timeline route: the id plus the agent and person when the link gave them. */
export function findSession<S extends SessionKey>(
  sessions: readonly S[],
  id: string,
  search: SessionSearch,
): S | undefined {
  return sessions.find(
    (s) =>
      s.id === id &&
      (search.session_agent === undefined || s.agent === search.session_agent) &&
      (search.session_user === undefined || s.user_id === search.session_user),
  );
}

/** The search of «Сессии»: ?flag=<signal key> keeps the sessions with that signal. */
export function validateSessionsSearch(search: Record<string, unknown>): { flag?: string } {
  return typeof search.flag === 'string' && search.flag !== '' ? { flag: search.flag } : {};
}

/** A path with the page's filters, so a link from one screen keeps the sample of the next. */
export function withFilters(
  path: string,
  filters: AnalyticsFilters,
  extra: Record<string, string | number | undefined> = {},
): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filtersToSearch(filters))) {
    params.set(key, String(value));
  }
  for (const [key, value] of Object.entries(extra)) {
    if (value !== undefined) params.set(key, String(value));
  }
  const query = params.toString();
  return query ? `${path}?${query}` : path;
}

/** An in-app href with the page's filters added next to its own keys (a Gantt mark, a pulse row). */
export function keepFilters(href: string, filters: AnalyticsFilters): string {
  const [path = href, query = ''] = href.split('?', 2);
  return withFilters(path, filters, Object.fromEntries(new URLSearchParams(query)));
}
