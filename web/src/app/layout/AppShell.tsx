import { Link, Outlet, useMatches, useRouter } from '@tanstack/react-router';
import type { ReactNode } from 'react';

import { validateAnalyticsSearch } from '../../features/analytics';
import { appTitle } from '../appTitle';
import { buildMenu, menuGroup } from './navigation';
import { ThemeSwitch } from './ThemeSwitch';

// The v3 product shell (HT-118): a top bar with the brand, the analytics tabs on the left,
// settings, the person and the theme on the right; the head's second row holds the settings
// tabs inside settings and headBar (the filters) on the analytics tabs.
export function AppShell({ headRight, headBar }: { headRight?: ReactNode; headBar?: ReactNode }) {
  const router = useRouter();
  const matches = useMatches();
  const menu = buildMenu(router.routeTree, matches);
  const analytics = menuGroup(menu, 'Аналитика');
  const settings = menuGroup(menu, 'Настройки');
  const inSettings = settings.some((item) => item.on);
  const firstSettings = settings[0];
  // Inside settings the second row of the head holds the settings tabs, as the filters
  // do on the analytics tabs.
  const row2 = inSettings ? (
    <nav className="tabs" aria-label="Настройки">
      {settings.map(({ to, title, on }) => (
        <Link key={to} className="tab" to={to} aria-current={on ? 'page' : undefined}>
          {title}
        </Link>
      ))}
    </nav>
  ) : (
    headBar
  );

  return (
    <>
      <header className="top">
        <div className="top-in">
          <Link className="brand" to="/" title="На главную" activeOptions={{ exact: true }}>
            <i />
            {appTitle}
          </Link>
          {analytics.length > 0 && (
            <nav className="tabs" aria-label="Разделы">
              {analytics.map(({ to, title, on }) => (
                <Link
                  key={to}
                  className="tab"
                  to={to}
                  // The filters travel between the tabs; a page's own keys (?line=, ?flag=) stay behind.
                  search={(prev: Record<string, unknown>) => validateAnalyticsSearch(prev)}
                  aria-current={on ? 'page' : undefined}
                >
                  {title}
                </Link>
              ))}
            </nav>
          )}
          <div className="top-right">
            {firstSettings && (
              <Link
                className="tab"
                to={firstSettings.to}
                aria-current={inSettings ? 'page' : undefined}
              >
                Настройки
              </Link>
            )}
            {headRight}
            <ThemeSwitch />
          </div>
        </div>
        {row2 && (
          <div className="top-row2">
            <div className="top-row2-in">{row2}</div>
          </div>
        )}
      </header>
      <main className="page">
        <Outlet />
      </main>
    </>
  );
}
