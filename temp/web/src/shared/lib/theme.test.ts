import indexHtml from '../../../index.html?raw';

import { initialTheme, THEME_STORAGE_KEY, type Theme } from './theme';

type Storage = Pick<globalThis.Storage, 'getItem'>;

const storageWith = (value: string | null): Storage => ({
  getItem: (key) => (key === THEME_STORAGE_KEY ? value : null),
});

const throwingStorage: Storage = {
  getItem: () => {
    throw new DOMException('denied', 'SecurityError');
  },
};

const cases: [name: string, storage: Storage, want: Theme][] = [
  ['no saved value', storageWith(null), 'dark'],
  ['explicit light', storageWith('light'), 'light'],
  ['explicit dark', storageWith('dark'), 'dark'],
  ['garbage value', storageWith('Light '), 'dark'],
  ['storage throws', throwingStorage, 'dark'],
];

// Runs the inline <head> script of index.html against a fake storage and root element.
function runInlineScript(storage: Storage): string | null {
  const script = /<script>([\s\S]*?)<\/script>/.exec(indexHtml)?.[1];
  if (script === undefined) {
    throw new Error('index.html has no inline theme script');
  }
  const root = document.createElement('html');
  // eslint-disable-next-line @typescript-eslint/no-implied-eval
  const run = new Function('localStorage', 'document', script) as (
    localStorage: Storage,
    document: { documentElement: Element },
  ) => void;
  run(storage, { documentElement: root });
  return root.getAttribute('data-theme');
}

describe('initialTheme', () => {
  it.each(cases)('%s → %s', (_, storage, want) => {
    expect(initialTheme(storage)).toBe(want);
  });
});

describe('index.html inline theme script', () => {
  it.each(cases)('%s → same theme as initialTheme', (_, storage, want) => {
    expect(runInlineScript(storage)).toBe(want);
  });

  it('runs before the bundle is loaded', () => {
    const head = /<head>([\s\S]*?)<\/head>/.exec(indexHtml)?.[1] ?? '';
    expect(head).toContain('<script>');
    expect(indexHtml.indexOf('<script>')).toBeLessThan(indexHtml.indexOf('<script type="module"'));
  });
});
