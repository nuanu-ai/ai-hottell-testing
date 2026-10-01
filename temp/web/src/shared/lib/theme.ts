import { writeStorage } from './storage';

export type Theme = 'dark' | 'light';

export const THEME_STORAGE_KEY = 'ht-theme';

// The inline script in index.html is a copy of this function: it has to set
// data-theme before the bundle loads. theme.test.ts fails when they diverge.
export function initialTheme(storage: Pick<Storage, 'getItem'>): Theme {
  try {
    return storage.getItem(THEME_STORAGE_KEY) === 'light' ? 'light' : 'dark';
  } catch {
    return 'dark';
  }
}

// Applies the chosen theme to <html> and remembers it for the inline script.
export function applyTheme(theme: Theme): void {
  document.documentElement.setAttribute('data-theme', theme);
  writeStorage(THEME_STORAGE_KEY, theme);
}
