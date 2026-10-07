import type { QueryClient } from '@tanstack/react-query';
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  lazyRouteComponent,
  redirect,
  type RouteComponent,
  type RouterHistory,
} from '@tanstack/react-router';
import {
  deliveryQueryOptions,
  FixPage,
  FrictionPage,
  OverviewPage,
  SessionPage,
  SessionsPage,
  SkillsPage,
  TeamPage,
  ToolsPage,
  validateAnalyticsSearch,
  validateFixSearch,
  validateSessionSearch,
  validateSessionsSearch,
} from '../features/analytics';
import { LoginPage } from '../features/auth';
import { ConnectPage } from '../features/connect';
import { InvitePage, ResetPage } from '../features/onboarding';
import { ProfilePage } from '../features/profile';
import { StoryPage } from '../features/story';
import {
  TelemetrySettingsPage,
  validateTelemetrySettingsSearch,
} from '../features/telemetry-settings';
import { UsersPage } from '../features/users';
import { isUnauthorized, meQueryOptions } from '../shared/api';
import { appTitle } from './appTitle';
import { RootLayout, SignedInShell } from './layout';

// One description per route; the head tabs and the browser tab title are built from it.
declare module '@tanstack/react-router' {
  interface StaticDataRouteOption {
    /** Page title: the browser tab reads «<title> · hottell». */
    title: string;
    /** Present only on routes that appear as a tab: «Аналитика» in the head, «Настройки» inside settings. */
    menu?: { group: 'Аналитика' | 'Настройки' };
    /** The page is about everyone («Команда»): the head hides «Человек» and reads all people's data. */
    everyone?: true;
  }
}

type RouterContext = { queryClient: QueryClient };

// ensureQueryData in its non-deprecated form: the cached user, or one request for it.
function ensureMe(queryClient: QueryClient) {
  return queryClient.query({ ...meQueryOptions, staleTime: 'static' });
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  staticData: { title: appTitle },
  component: RootLayout,
});

// Open pages: they live outside the shell and need no session.

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  staticData: { title: 'Вход' },
  validateSearch: (search: Record<string, unknown>): { next?: string } =>
    typeof search.next === 'string' ? { next: search.next } : {},
  beforeLoad: async ({ context }) => {
    // Any failure, 401 included, leaves the visitor on the sign-in page.
    const me = await ensureMe(context.queryClient).catch(() => undefined);
    if (me) {
      redirect({ to: '/', throw: true });
    }
  },
  component: LoginPage,
});

const inviteRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/invite/$token',
  staticData: { title: 'Приглашение' },
  component: InvitePage,
});

const resetRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/reset/$token',
  staticData: { title: 'Новый пароль' },
  component: ResetPage,
});

// Closed pages: children of the shell, which lets in only a live session.

const shellRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'shell',
  staticData: { title: appTitle },
  beforeLoad: async ({ context, location }) => {
    try {
      await ensureMe(context.queryClient);
    } catch (error) {
      if (isUnauthorized(error)) {
        redirect({ to: '/login', search: { next: location.href }, throw: true });
      }
      throw error;
    }
  },
  component: SignedInShell,
});

const indexRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/',
  staticData: { title: appTitle },
  // A person with a binary connected starts on «Что исправить»; one without sees only our settings.
  // A failed status read counts as not connected: settings always open, analytics may not.
  beforeLoad: async ({ context }) => {
    const delivery = await context.queryClient
      .query({ ...deliveryQueryOptions(), retry: false })
      .catch(() => undefined);
    const connected = delivery !== undefined && delivery.state !== 'not_configured';
    redirect({ to: connected ? '/fix' : '/users', throw: true });
  },
});

// The analytics tabs in the order of design v5.1; a session's timeline lights «Сессии».
const analyticsRoute = createRoute({
  getParentRoute: () => shellRoute,
  id: 'analytics',
  staticData: { title: appTitle },
  // Period, agent, project, service sessions and person: the same filters on every tab.
  validateSearch: validateAnalyticsSearch,
});

const analyticsTab = <TPath extends string>(
  path: TPath,
  title: string,
  component?: RouteComponent,
  extra?: { everyone?: true },
) =>
  createRoute({
    getParentRoute: () => analyticsRoute,
    path,
    staticData: { title, menu: { group: 'Аналитика' }, ...extra },
    component,
  });

const fixRoute = createRoute({
  getParentRoute: () => analyticsRoute,
  path: '/fix',
  staticData: { title: 'Что исправить', menu: { group: 'Аналитика' } },
  validateSearch: validateFixSearch,
  component: FixPage,
});
const overviewRoute = analyticsTab('/overview', 'Обзор', OverviewPage);
const teamRoute = analyticsTab('/team', 'Команда', TeamPage, { everyone: true });
// «Сессии» is a layout over the list and one session, so both light the tab.
const sessionsRoute = analyticsTab('/sessions', 'Сессии');
const sessionListRoute = createRoute({
  getParentRoute: () => sessionsRoute,
  path: '/',
  staticData: { title: 'Сессии' },
  validateSearch: validateSessionsSearch,
  component: SessionsPage,
});
const sessionRoute = createRoute({
  getParentRoute: () => sessionsRoute,
  path: '$id',
  staticData: { title: 'Сессия' },
  validateSearch: validateSessionSearch,
  component: SessionPage,
});
const frictionRoute = analyticsTab('/friction', 'Трение', FrictionPage);
const toolsRoute = analyticsTab('/tools', 'Инструменты и MCP', ToolsPage);
const skillsRoute = analyticsTab('/skills', 'Skills', SkillsPage);

// «Как это было» (HT-535): the last head tab, outside the analytics filters.
const storyRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/story',
  staticData: { title: 'Как это было', menu: { group: 'Аналитика' } },
  component: StoryPage,
});

const usersRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/users',
  staticData: { title: 'Пользователи', menu: { group: 'Настройки' } },
  component: UsersPage,
});

const profileRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/profile',
  staticData: { title: 'Профиль', menu: { group: 'Настройки' } },
  component: ProfilePage,
});

const connectRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/connect',
  staticData: { title: 'Подключение', menu: { group: 'Настройки' } },
  component: ConnectPage,
});

const telemetrySettingsRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/telemetry',
  staticData: { title: 'Что отправлять', menu: { group: 'Настройки' } },
  validateSearch: validateTelemetrySettingsSearch,
  component: TelemetrySettingsPage,
});

// The component showcase exists only in a dev build (HT-118): it needs no session and no menu entry.
// Built inside the DEV branch so a production bundle drops the page and everything it imports.
const devRoutes = import.meta.env.DEV
  ? [
      createRoute({
        getParentRoute: () => rootRoute,
        path: '/design',
        staticData: { title: 'Витрина' },
        // A dynamic import: the showcase modules stay out of a production bundle entirely.
        component: lazyRouteComponent(() => import('./design'), 'DesignPage'),
      }),
    ]
  : [];

const routeTree = rootRoute.addChildren([
  ...devRoutes,
  loginRoute,
  inviteRoute,
  resetRoute,
  shellRoute.addChildren([
    indexRoute,
    analyticsRoute.addChildren([
      fixRoute,
      overviewRoute,
      teamRoute,
      sessionsRoute.addChildren([sessionListRoute, sessionRoute]),
      frictionRoute,
      toolsRoute,
      skillsRoute,
    ]),
    storyRoute,
    usersRoute,
    profileRoute,
    connectRoute,
    telemetrySettingsRoute,
  ]),
]);

export function createAppRouter(queryClient: QueryClient, history?: RouterHistory) {
  return createRouter({ routeTree, history, context: { queryClient } });
}

export type AppRouter = ReturnType<typeof createAppRouter>;

/**
 * The app-wide answer to an API 401: on a closed page the session is gone, so the cache
 * is dropped and the visitor goes to sign in with the way back. On an open page a 401 is
 * an ordinary answer. While a navigation is loading, the shell's beforeLoad redirects
 * on its own.
 */
export function leaveClosedPage(router: AppRouter, queryClient: QueryClient) {
  const { status, matches, location } = router.state;
  if (status !== 'idle' || !matches.some((match) => match.routeId === shellRoute.id)) {
    return;
  }
  queryClient.clear();
  void router.navigate({ to: '/login', search: { next: location.href } });
}

declare module '@tanstack/react-router' {
  interface Register {
    router: AppRouter;
  }
}
