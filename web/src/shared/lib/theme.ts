import { readStorage, writeStorage } from './storage';

// «system» follows prefers-color-scheme; dark and light are an explicit choice.
export type Theme = 'system' | 'dark' | 'light';

export const THEME_STORAGE_KEY = 'ht-theme';

const DARK_QUERY = '(prefers-color-scheme: dark)';

// The inline script in index.html is a copy of initialTheme + resolveTheme: it has to set
// data-theme before the bundle loads. theme.test.ts fails when they diverge.
export function initialTheme(storage: Pick<Storage, 'getItem'>): Theme {
  try {
    const saved = storage.getItem(THEME_STORAGE_KEY);
    return saved === 'light' || saved === 'dark' ? saved : 'system';
  } catch {
    return 'system';
  }
}

export function resolveTheme(theme: Theme, prefersDark: boolean): 'dark' | 'light' {
  if (theme === 'system') {
    return prefersDark ? 'dark' : 'light';
  }
  return theme;
}

// Without matchMedia the page stays dark, as the :root tokens are.
function darkQuery(): MediaQueryList | undefined {
  return typeof window.matchMedia === 'function' ? window.matchMedia(DARK_QUERY) : undefined;
}

function setDataTheme(theme: Theme): void {
  const prefersDark = darkQuery()?.matches ?? true;
  document.documentElement.setAttribute('data-theme', resolveTheme(theme, prefersDark));
}

// The choice of this page. Storage only carries it over to the next load: it may be
// blocked, and a system change must not undo a choice it failed to save.
let current: Theme | undefined;

// The current choice: the one made on this page, else the saved one.
export function currentTheme(): Theme {
  current ??= initialTheme({ getItem: readStorage });
  return current;
}

// Applies the chosen theme to <html> and remembers it for the inline script.
export function applyTheme(theme: Theme): void {
  current = theme;
  setDataTheme(theme);
  writeStorage(THEME_STORAGE_KEY, theme);
}

// Re-applies the theme when the system scheme changes; an explicit choice stays as it is.
// Returns the unsubscribe function.
export function followSystemTheme(): () => void {
  setDataTheme(currentTheme());
  const query = darkQuery();
  if (query === undefined) {
    return () => undefined;
  }
  const onChange = () => {
    setDataTheme(currentTheme());
  };
  query.addEventListener('change', onChange);
  return () => {
    query.removeEventListener('change', onChange);
  };
}
