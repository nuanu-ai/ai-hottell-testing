import type { AnyRoute, StaticDataRouteOption } from '@tanstack/react-router';
import type { ComponentType } from 'react';

// The menu and the breadcrumbs are both read from the routes' staticData,
// so a page cannot appear in one under a different name than in the other.

export type MenuItem = { to: string; title: string; icon: ComponentType; on: boolean };
export type MenuGroup = { title: string; items: MenuItem[] };

/** The part of a route match the shell needs. */
export type ShellMatch = { routeId: string; pathname: string; staticData: StaticDataRouteOption };

export type Crumbs = {
  /** Link to the parent page, shown instead of the group on nested pages. */
  parent?: { to: string; title: string };
  group?: string;
  current: string;
};

function childrenOf(route: AnyRoute): AnyRoute[] {
  const children: unknown = route.children;
  return Array.isArray(children) ? (children as AnyRoute[]) : [];
}

/**
 * Groups the menu routes in the order they are declared. A menu item is on
 * when its route is among the current matches: a nested page lights its parent.
 */
export function buildMenu(root: AnyRoute, matches: readonly ShellMatch[]): MenuGroup[] {
  const active = new Set(matches.map((match) => match.routeId));
  const groups: MenuGroup[] = [];
  const visit = (route: AnyRoute) => {
    // AnyRoute types these as any; the router fills them for every route.
    const { id, fullPath } = route as { id: string; fullPath: string };
    const { staticData } = route.options as { staticData?: StaticDataRouteOption };
    if (staticData?.menu) {
      const item: MenuItem = {
        to: fullPath,
        title: staticData.title,
        icon: staticData.menu.icon,
        on: active.has(id),
      };
      const group = groups.find((g) => g.title === staticData.menu?.group);
      if (group) {
        group.items.push(item);
      } else {
        groups.push({ title: staticData.menu.group, items: [item] });
      }
    }
    childrenOf(route).forEach(visit);
  };
  visit(root);
  return groups;
}

/**
 * Breadcrumbs of the current page: parent link (or menu group) / title.
 * The shell's own route and the routes above it are not pages.
 */
export function buildCrumbs(matches: readonly ShellMatch[], shellRouteId: string): Crumbs {
  const pages = matches.slice(matches.findIndex((match) => match.routeId === shellRouteId) + 1);
  const leaf = pages.at(-1);
  if (!leaf) {
    return { current: matches.at(-1)?.staticData.title ?? '' };
  }
  const above = pages.at(-2);
  const group = pages.findLast((match) => match.staticData.menu)?.staticData.menu?.group;
  return {
    parent: above && { to: above.pathname, title: above.staticData.title },
    group,
    current: leaf.staticData.title,
  };
}
