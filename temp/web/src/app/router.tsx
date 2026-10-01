import type { QueryClient } from '@tanstack/react-query';
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  redirect,
  type RouterHistory,
} from '@tanstack/react-router';
import type { ComponentType } from 'react';

import { LoginPage } from '../features/auth';
import { ConnectPage } from '../features/connect';
import { InvitePage, ResetPage } from '../features/onboarding';
import { ProfilePage } from '../features/profile';
import { TelemetrySettingsPage } from '../features/telemetry-settings';
import { UsersPage } from '../features/users';
import { isUnauthorized, meQueryOptions } from '../shared/api';
import { Key, Profile, Sliders, Users } from '../shared/ui/icons';
import { appTitle } from './appTitle';
import { SignedInShell } from './layout';

// One description per route; the menu and the breadcrumbs are built from it.
declare module '@tanstack/react-router' {
  interface StaticDataRouteOption {
    /** Breadcrumb title. */
    title: string;
    /** Present only on routes that appear in the menu. */
    menu?: { group: string; icon: ComponentType };
  }
}

type RouterContext = { queryClient: QueryClient };

// ensureQueryData in its non-deprecated form: the cached user, or one request for it.
function ensureMe(queryClient: QueryClient) {
  return queryClient.query({ ...meQueryOptions, staleTime: 'static' });
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  staticData: { title: appTitle },
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
      redirect({ to: '/users', throw: true });
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
  beforeLoad: () => {
    redirect({ to: '/users', throw: true });
  },
});

const usersRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/users',
  staticData: { title: 'Пользователи', menu: { group: 'Настройки', icon: Users } },
  component: UsersPage,
});

const profileRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/profile',
  staticData: { title: 'Профиль', menu: { group: 'Настройки', icon: Profile } },
  component: ProfilePage,
});

const connectRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/connect',
  staticData: { title: 'Подключение', menu: { group: 'Настройки', icon: Key } },
  component: ConnectPage,
});

const telemetrySettingsRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/telemetry',
  staticData: { title: 'Что отправлять', menu: { group: 'Настройки', icon: Sliders } },
  component: TelemetrySettingsPage,
});

const routeTree = rootRoute.addChildren([
  loginRoute,
  inviteRoute,
  resetRoute,
  shellRoute.addChildren([
    indexRoute,
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
