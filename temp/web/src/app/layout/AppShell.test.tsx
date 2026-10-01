import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router';
import { act, render, screen, within } from '@testing-library/react';
import type { ReactNode } from 'react';

import { THEME_STORAGE_KEY } from '../../shared/lib/theme';
import { Profile, Users } from '../../shared/ui/icons';
import { AppShell, MENU_COLLAPSED_KEY } from './AppShell';

// A route tree of its own: the app has no menu pages yet, the shell has to show them anyway.
function renderShell(path: string, headRight?: ReactNode) {
  const root = createRootRoute({
    staticData: { title: 'Телеметрия агентов' },
    component: () => <AppShell headRight={headRight} />,
  });
  const home = createRoute({
    getParentRoute: () => root,
    path: '/',
    staticData: { title: 'Главная' },
    component: () => <p>главная</p>,
  });
  const users = createRoute({
    getParentRoute: () => root,
    path: '/users',
    staticData: { title: 'Пользователи', menu: { group: 'Доступ', icon: Users } },
  });
  const usersList = createRoute({
    getParentRoute: () => users,
    path: '/',
    staticData: { title: 'Пользователи' },
    component: () => <p>список</p>,
  });
  const user = createRoute({
    getParentRoute: () => users,
    path: '$id',
    staticData: { title: 'Карточка пользователя' },
    component: () => <p>карточка</p>,
  });
  const profile = createRoute({
    getParentRoute: () => root,
    path: '/profile',
    staticData: { title: 'Профиль', menu: { group: 'Доступ', icon: Profile } },
    component: () => <p>профиль</p>,
  });
  const router = createRouter({
    routeTree: root.addChildren([home, users.addChildren([usersList, user]), profile]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  const result = render(<RouterProvider router={router} />);
  return { router, ...result };
}

const shell = () => document.getElementById('каркас');
const sidebar = () => document.querySelector('.sidebar');
const backdrop = () => document.querySelector<HTMLElement>('.backdrop');
const menuItem = (name: string) =>
  within(document.querySelector<HTMLElement>('nav.sidebar-nav') ?? document.body).getByRole(
    'link',
    {
      name,
    },
  );

beforeEach(() => {
  localStorage.clear();
  document.documentElement.setAttribute('data-theme', 'dark');
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('AppShell layout', () => {
  it('renders deploy markup: sidebar, header with crumbs, body with the page', async () => {
    renderShell('/');
    expect(await screen.findByText('главная')).toBeInTheDocument();

    const el = shell();
    expect(el).toHaveClass('shell');
    expect(el?.querySelector(':scope > .backdrop')).not.toBeVisible();
    expect(
      el?.querySelector(':scope > aside.sidebar > .sidebar-in > .sidebar-head > a.brand'),
    ).toHaveTextContent('Телеметрия агентов');
    expect(el?.querySelector('.sidebar-head > button.collapse-btn')).toBeInTheDocument();
    expect(el?.querySelector('main.work > header.work-head > button.burger')).toBeInTheDocument();
    expect(
      el?.querySelector('header.work-head > nav.crumbs + .spacer + .head-right'),
    ).not.toBeNull();
    expect(el?.querySelector('main.work > .work-body')).toHaveTextContent('главная');
  });

  it('builds the menu groups and items from route static data', async () => {
    renderShell('/');
    await screen.findByText('главная');

    const groups = [...document.querySelectorAll('nav.sidebar-nav > .nav-group')];
    expect(groups).toHaveLength(1);
    expect(groups[0]?.querySelector('.nav-group-title')).toHaveTextContent('Доступ');
    const items = [...(groups[0]?.querySelectorAll('a.nav-item') ?? [])];
    expect(items.map((a) => a.querySelector('.nav-text')?.textContent)).toEqual([
      'Пользователи',
      'Профиль',
    ]);
    expect(items.map((a) => a.getAttribute('href'))).toEqual(['/users', '/profile']);
    expect(items[0]?.querySelector('.nav-icon > svg')).not.toBeNull();
  });

  it('shows only the title in the crumbs of a page outside the menu', async () => {
    renderShell('/');
    await screen.findByText('главная');

    const crumbs = screen.getByRole('navigation', { name: 'Хлебные крошки' });
    expect(crumbs.querySelector('.crumb-group')).toBeNull();
    expect(crumbs.querySelector('.crumb-sep')).toBeNull();
    expect(crumbs.querySelector('.crumb-current')).toHaveTextContent('Главная');
  });
});

describe('active menu item and crumbs', () => {
  it('marks the current page on and shows its group in the crumbs', async () => {
    renderShell('/profile');
    await screen.findByText('профиль');

    expect(menuItem('Профиль')).toHaveClass('nav-item', 'on');
    expect(menuItem('Пользователи')).not.toHaveClass('on');
    const crumbs = screen.getByRole('navigation', { name: 'Хлебные крошки' });
    expect(crumbs.querySelector('span.crumb-group')).toHaveTextContent('Доступ');
    expect(crumbs.querySelector('.crumb-sep')).toHaveTextContent('/');
    expect(crumbs.querySelector('.crumb-current')).toHaveTextContent('Профиль');
  });

  it('lights the parent item on a nested page and links the parent in the crumbs', async () => {
    renderShell('/users/42');
    await screen.findByText('карточка');

    expect(menuItem('Пользователи')).toHaveClass('on');
    expect(menuItem('Профиль')).not.toHaveClass('on');
    const crumbs = screen.getByRole('navigation', { name: 'Хлебные крошки' });
    expect(crumbs.querySelector('a.crumb-group')).toHaveAttribute('href', '/users');
    expect(crumbs.querySelector('a.crumb-group')).toHaveTextContent('Пользователи');
    expect(crumbs.querySelector('.crumb-current')).toHaveTextContent('Карточка пользователя');
  });
});

describe('theme switch', () => {
  it('marks the active theme and switches data-theme and storage on click', async () => {
    renderShell('/');
    await screen.findByText('главная');

    const dark = screen.getByRole('button', { name: 'Тёмная' });
    const light = screen.getByRole('button', { name: 'Светлая' });
    expect(dark).toHaveClass('on');
    expect(light).not.toHaveClass('on');

    act(() => {
      light.click();
    });
    expect(document.documentElement).toHaveAttribute('data-theme', 'light');
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light');
    expect(light).toHaveClass('on');
    expect(dark).not.toHaveClass('on');

    act(() => {
      dark.click();
    });
    expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark');
  });

  it('starts from the saved theme', async () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'light');
    renderShell('/');
    await screen.findByText('главная');

    expect(screen.getByRole('button', { name: 'Светлая' })).toHaveClass('on');
  });

  it('puts the head-right slot before the theme switch', async () => {
    renderShell('/', <span className="slot">слот</span>);
    await screen.findByText('главная');

    const children = [...(document.querySelector('.head-right')?.children ?? [])];
    expect(children.map((c) => c.className)).toEqual(['slot', 'theme-switch']);
  });
});

describe('collapsing the menu', () => {
  it('toggles the collapsed class, arrow and label, and survives a reload', async () => {
    const first = renderShell('/');
    await screen.findByText('главная');

    const button = screen.getByRole('button', { name: 'Свернуть меню' });
    expect(button).toHaveAttribute('title', 'Свернуть меню');
    expect(button.querySelector('path')).toHaveAttribute('d', 'M15 6l-6 6 6 6');

    act(() => {
      button.click();
    });
    expect(shell()).toHaveClass('collapsed');
    expect(localStorage.getItem(MENU_COLLAPSED_KEY)).toBe('1');
    expect(button).toHaveAttribute('title', 'Развернуть меню');
    expect(button).toHaveAccessibleName('Развернуть меню');
    expect(button.querySelector('path')).toHaveAttribute('d', 'M9 6l6 6-6 6');

    first.unmount();
    renderShell('/');
    await screen.findByText('главная');
    expect(shell()).toHaveClass('collapsed');

    act(() => {
      screen.getByRole('button', { name: 'Развернуть меню' }).click();
    });
    expect(shell()).not.toHaveClass('collapsed');
    expect(localStorage.getItem(MENU_COLLAPSED_KEY)).toBe('0');
  });
});

describe('burger on a narrow screen', () => {
  it('opens the menu with the backdrop; a backdrop click closes it', async () => {
    renderShell('/');
    await screen.findByText('главная');
    expect(sidebar()).not.toHaveClass('open');
    expect(backdrop()?.hidden).toBe(true);

    act(() => {
      screen.getByRole('button', { name: 'Меню' }).click();
    });
    expect(sidebar()).toHaveClass('open');
    expect(backdrop()?.hidden).toBe(false);

    act(() => {
      backdrop()?.click();
    });
    expect(sidebar()).not.toHaveClass('open');
    expect(backdrop()?.hidden).toBe(true);
  });

  it('closes the menu when a menu link is followed', async () => {
    renderShell('/');
    await screen.findByText('главная');

    act(() => {
      screen.getByRole('button', { name: 'Меню' }).click();
    });
    act(() => {
      menuItem('Профиль').click();
    });
    expect(await screen.findByText('профиль')).toBeInTheDocument();
    expect(sidebar()).not.toHaveClass('open');
    expect(backdrop()?.hidden).toBe(true);
  });
});

describe('without localStorage', () => {
  it('renders and still switches theme and menu when storage throws', async () => {
    const denied = () => {
      throw new DOMException('denied', 'SecurityError');
    };
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(denied);
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(denied);

    renderShell('/');
    await screen.findByText('главная');
    expect(shell()).not.toHaveClass('collapsed');
    expect(screen.getByRole('button', { name: 'Тёмная' })).toHaveClass('on');

    act(() => {
      screen.getByRole('button', { name: 'Светлая' }).click();
    });
    expect(document.documentElement).toHaveAttribute('data-theme', 'light');

    act(() => {
      screen.getByRole('button', { name: 'Свернуть меню' }).click();
    });
    expect(shell()).toHaveClass('collapsed');
  });
});
