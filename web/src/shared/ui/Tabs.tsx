import { useRef, type KeyboardEvent, type ReactNode } from 'react';

import { tabIds } from './tabIds';

// A number, or 'dot': a change not saved yet.
export type TabBadge = number | 'dot';

export type TabItem<T extends string> = { id: T; label: ReactNode; badge?: TabBadge };

type TabsProps<T extends string> = {
  // The base of the tab and panel ids, unique on the page; TabPanel takes the same.
  id: string;
  label: string;
  tabs: readonly TabItem<T>[];
  value: T;
  onChange: (value: T) => void;
};

// The index a key moves to from `index` out of `count`, or null for a key tabs don't handle.
function target(key: string, index: number, count: number): number | null {
  switch (key) {
    case 'ArrowRight':
      return (index + 1) % count;
    case 'ArrowLeft':
      return (index - 1 + count) % count;
    case 'Home':
      return 0;
    case 'End':
      return count - 1;
    default:
      return null;
  }
}

// The leading space keeps the tab's accessible name «Хуки 3», not «Хуки3».
function Badge({ badge }: { badge: TabBadge }) {
  if (badge === 'dot') {
    return (
      <>
        {' '}
        <span className="tab-dot" aria-hidden="true" />
        <span className="sr-only">не сохранено</span>
      </>
    );
  }
  return (
    <>
      {' '}
      <span className="tab-badge num">{badge}</span>
    </>
  );
}

// In-page tabs on the v3 .subtabs: a WAI-ARIA tablist with automatic activation —
// the arrows, Home and End move the focus and select the tab they land on, and only
// the selected tab stays in the Tab order.
export function Tabs<T extends string>({ id, label, tabs, value, onChange }: TabsProps<T>) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);

  function onKeyDown(event: KeyboardEvent<HTMLButtonElement>, index: number) {
    const next = target(event.key, index, tabs.length);
    const tab = next === null ? undefined : tabs[next];
    if (next === null || !tab) return;
    event.preventDefault();
    onChange(tab.id);
    refs.current[next]?.focus();
  }

  return (
    <div className="tabs subtabs" role="tablist" aria-label={label}>
      {tabs.map((tab, index) => {
        const ids = tabIds(id, tab.id);
        const selected = tab.id === value;
        return (
          <button
            key={tab.id}
            ref={(node) => {
              refs.current[index] = node;
            }}
            type="button"
            className="tab"
            role="tab"
            id={ids.tab}
            aria-selected={selected}
            aria-controls={ids.panel}
            tabIndex={selected ? 0 : -1}
            onClick={() => {
              onChange(tab.id);
            }}
            onKeyDown={(event) => {
              onKeyDown(event, index);
            }}
          >
            {tab.label}
            {tab.badge !== undefined && <Badge badge={tab.badge} />}
          </button>
        );
      })}
    </div>
  );
}

type TabPanelProps = {
  tabsId: string;
  // The id of the tab this panel belongs to; render the panel of the selected tab.
  tab: string;
  children: ReactNode;
};

// The content of one tab, named by it; focusable so the keyboard reaches a panel without controls.
export function TabPanel({ tabsId, tab, children }: TabPanelProps) {
  const ids = tabIds(tabsId, tab);
  return (
    <div role="tabpanel" id={ids.panel} aria-labelledby={ids.tab} tabIndex={0}>
      {children}
    </div>
  );
}
