import { createMemoryHistory, RouterProvider } from '@tanstack/react-router';
import { act, render, screen, waitFor, within } from '@testing-library/react';
import { http, HttpResponse } from 'msw';

import { meQueryOptions } from '../shared/api';
import { handlers, noSession, server } from '../shared/api/test/server';
import { appTitle } from './appTitle';
import { createApp } from './createApp';
import { AppProviders } from './providers';

function renderAt(path: string) {
  const app = createApp(createMemoryHistory({ initialEntries: [path] }));
  render(
    <AppProviders client={app.queryClient}>
      <RouterProvider router={app.router} />
    </AppProviders>,
  );
  return app;
}

const href = (app: ReturnType<typeof createApp>) => app.router.state.location.href;
const heading = (name: string) => screen.findByRole('heading', { level: 1, name });
// The users page has no h1: its title is deploy's span.крупно.
const usersTitle = () => screen.findByText('Пользователи', { selector: 'span.крупно' });
// Nor does the profile page: its password card stands for it.
const profileCard = () => screen.findByRole('heading', { level: 2, name: 'Пароль' });
const person = () => screen.findByRole('button', { name: /Анна Петрова/ });

describe('without a session', () => {
  beforeEach(() => {
    server.use(handlers.getMeError(401));
  });

  it('sends a closed page to /login with the way back', async () => {
    const app = renderAt('/users');

    expect(await heading(appTitle)).toBeInTheDocument();
    expect(href(app)).toBe('/login?next=%2Fusers');
    expect(document.getElementById('каркас')).toBeNull();
  });

  it('sends / to /login with the way back to /', async () => {
    const app = renderAt('/');

    expect(await heading(appTitle)).toBeInTheDocument();
    expect(href(app)).toBe('/login?next=%2F');
  });

  it.each([
    ['/login', appTitle],
    ['/invite/abc', appTitle],
    ['/reset/abc', appTitle],
  ])('opens %s without the shell', async (path, title) => {
    const app = renderAt(path);

    expect(await heading(title)).toBeInTheDocument();
    expect(href(app)).toBe(path);
    expect(document.getElementById('каркас')).toBeNull();
  });
});

describe('with a session', () => {
  beforeEach(() => {
    server.use(handlers.getMe());
  });

  it('sends /login to /users', async () => {
    const app = renderAt('/login');

    expect(await usersTitle()).toBeInTheDocument();
    expect(href(app)).toBe('/users');
  });

  it('sends / to /users', async () => {
    const app = renderAt('/');

    expect(await usersTitle()).toBeInTheDocument();
    expect(href(app)).toBe('/users');
  });

  it('shows the pages inside the shell with the Settings menu', async () => {
    renderAt('/profile');

    const card = await profileCard();
    const shell = document.getElementById('каркас');
    expect(shell?.querySelector('.work-body')).toContainElement(card);
    const groups = [...document.querySelectorAll('nav.sidebar-nav > .nav-group')];
    expect(groups.map((g) => g.querySelector('.nav-group-title')?.textContent)).toEqual([
      'Настройки',
    ]);
    const items = [...(groups[0]?.querySelectorAll('a.nav-item') ?? [])];
    expect(
      items.map((a) => [a.querySelector('.nav-text')?.textContent, a.getAttribute('href')]),
    ).toEqual([
      ['Пользователи', '/users'],
      ['Профиль', '/profile'],
      ['Подключение', '/connect'],
      ['Что отправлять', '/telemetry'],
    ]);
    const crumbs = screen.getByRole('navigation', { name: 'Хлебные крошки' });
    expect(crumbs.querySelector('span.crumb-group')).toHaveTextContent('Настройки');
    expect(crumbs.querySelector('.crumb-current')).toHaveTextContent('Профиль');
  });

  it('shows the person card in head-right: initials, name, sign-out title', async () => {
    renderAt('/users');

    const card = await person();
    expect(card).toHaveClass('человек');
    expect(card).toHaveAttribute('title', 'Анна Петрова — выйти');
    expect(card.querySelector('span.человек-знак')).toHaveTextContent('АП');
    expect(card.querySelector('span.человек-имя')).toHaveTextContent('Анна Петрова');
    expect(card.parentElement).toHaveClass('head-right');
  });

  it('signs out on a click: POST /auth/logout, empty cache, /login', async () => {
    let loggedOut = false;
    server.use(
      http.post('/api/auth/logout', () => {
        loggedOut = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const app = renderAt('/users');

    const card = await person();
    server.use(handlers.getMeError(401));
    act(() => {
      card.click();
    });

    expect(await heading(appTitle)).toBeInTheDocument();
    expect(loggedOut).toBe(true);
    expect(href(app)).toBe('/login');
    expect(app.queryClient.getQueryData(meQueryOptions.queryKey)).toBeUndefined();
  });
});

describe('an API 401 anywhere', () => {
  it('on a closed page drops the cache and goes to /login with the way back', async () => {
    server.use(handlers.getMe());
    const app = renderAt('/profile');
    await profileCard();

    server.use(handlers.getMeError(401));
    await act(() => app.queryClient.refetchQueries({ queryKey: meQueryOptions.queryKey }));

    expect(await heading(appTitle)).toBeInTheDocument();
    expect(href(app)).toBe('/login?next=%2Fprofile');
    expect(app.queryClient.getQueryCache().find({ queryKey: ['version'] })).toBeUndefined();
  });

  it('from a mutation on a closed page does the same', async () => {
    server.use(
      handlers.getMe(),
      http.post('/api/auth/logout', () => HttpResponse.json(noSession, { status: 401 })),
    );
    const app = renderAt('/users');

    const card = await person();
    server.use(handlers.getMeError(401));
    act(() => {
      card.click();
    });

    await waitFor(() => {
      expect(href(app)).toBe('/login?next=%2Fusers');
    });
    expect(await heading(appTitle)).toBeInTheDocument();
  });

  it('on an open page changes nothing', async () => {
    server.use(handlers.getMeError(401));
    const app = renderAt('/login?next=%2Fusers');
    await heading(appTitle);

    await act(() =>
      app.queryClient.refetchQueries({ queryKey: meQueryOptions.queryKey }).catch(() => undefined),
    );

    expect(href(app)).toBe('/login?next=%2Fusers');
    expect(within(document.body).getByRole('heading', { level: 1 })).toHaveTextContent(appTitle);
  });
});
