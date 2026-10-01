import { Link, Outlet, useMatch, useMatches, useRouter } from '@tanstack/react-router';
import { useState, type ReactNode } from 'react';

import { readStorage, writeStorage } from '../../shared/lib/storage';
import { Burger, Collapse } from '../../shared/ui/icons';
import { appTitle } from '../appTitle';
import { buildCrumbs, buildMenu } from './navigation';
import { ThemeSwitch } from './ThemeSwitch';

export const MENU_COLLAPSED_KEY = 'ht-menu-collapsed';

// Markup, classes and behaviour are 1:1 with deploy's shell.html
// (https://git.alva.dev/alva/deploy, commit 31e42ec).
export function AppShell({ headRight }: { headRight?: ReactNode }) {
  const router = useRouter();
  const matches = useMatches();
  const own = useMatch({ strict: false });
  const [collapsed, setCollapsed] = useState(() => readStorage(MENU_COLLAPSED_KEY) === '1');
  const [open, setOpen] = useState(false);

  const menu = buildMenu(router.routeTree, matches);
  const crumbs = buildCrumbs(matches, own.routeId);
  // The arrow and the label say what a click does, not the current state.
  const collapseLabel = collapsed ? 'Развернуть меню' : 'Свернуть меню';
  const close = () => {
    setOpen(false);
  };

  return (
    <div className={collapsed ? 'shell collapsed' : 'shell'} id="каркас">
      <div className="backdrop" hidden={!open} onClick={close} />
      <aside className={open ? 'sidebar open' : 'sidebar'}>
        <div className="sidebar-in">
          <div className="sidebar-head">
            <Link className="brand" to="/" title="На главную" onClick={close}>
              {appTitle}
            </Link>
            <button
              className="btn btn-link btn-sm collapse-btn"
              type="button"
              title={collapseLabel}
              aria-label={collapseLabel}
              onClick={() => {
                const next = !collapsed;
                writeStorage(MENU_COLLAPSED_KEY, next ? '1' : '0');
                setCollapsed(next);
              }}
            >
              <Collapse direction={collapsed ? 'right' : 'left'} />
            </button>
          </div>
          <nav className="sidebar-nav">
            {menu.map((group) => (
              <div className="nav-group" key={group.title}>
                <div className="nav-group-title">{group.title}</div>
                {group.items.map(({ to, title, icon: Icon, on }) => (
                  <Link
                    key={to}
                    className={on ? 'nav-item on' : 'nav-item'}
                    to={to}
                    onClick={close}
                  >
                    <span className="nav-icon">
                      <Icon />
                    </span>
                    <span className="nav-text">{title}</span>
                  </Link>
                ))}
              </div>
            ))}
          </nav>
        </div>
      </aside>

      <main className="work">
        <header className="work-head">
          <button
            className="btn btn-sm burger"
            type="button"
            aria-label="Меню"
            onClick={() => {
              setOpen(true);
            }}
          >
            <Burger />
          </button>
          <nav className="crumbs" aria-label="Хлебные крошки">
            {crumbs.parent ? (
              <Link className="crumb-group" to={crumbs.parent.to}>
                {crumbs.parent.title}
              </Link>
            ) : (
              crumbs.group && <span className="crumb-group">{crumbs.group}</span>
            )}
            {(crumbs.parent ?? crumbs.group) && <span className="crumb-sep">/</span>}
            <span className="crumb-current">{crumbs.current}</span>
          </nav>
          <div className="spacer"></div>
          <div className="head-right">
            {headRight}
            <ThemeSwitch />
          </div>
        </header>
        <div className="work-body">
          <Outlet />
        </div>
      </main>
    </div>
  );
}
