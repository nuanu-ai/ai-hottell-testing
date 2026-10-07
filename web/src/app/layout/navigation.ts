import type { AnyRoute, StaticDataRouteOption } from '@tanstack/react-router';

// The head tabs and the settings tabs are both read from the routes' staticData,
// so a page cannot appear in one place under a different name than in another.

export type MenuGroupName = NonNullable<StaticDataRouteOption['menu']>['group'];
export type MenuItem = { to: string; title: string; on: boolean };
export type MenuGroup = { title: MenuGroupName; items: MenuItem[] };

/** The part of a route match the shell needs. */
export type ShellMatch = { routeId: string; pathname: string; staticData: StaticDataRouteOption };

function childrenOf(route: AnyRoute): AnyRoute[] {
  const children: unknown = route.children;
  return Array.isArray(children) ? (children as AnyRoute[]) : [];
}

/**
 * Groups the menu routes in the order they are declared. An item is on
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
      const item: MenuItem = { to: fullPath, title: staticData.title, on: active.has(id) };
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

export function menuGroup(groups: readonly MenuGroup[], title: MenuGroupName): MenuItem[] {
  return groups.find((group) => group.title === title)?.items ?? [];
}
