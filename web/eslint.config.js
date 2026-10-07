import js from '@eslint/js';
import boundaries from 'eslint-plugin-boundaries';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import { defineConfig, globalIgnores } from 'eslint/config';
import globals from 'globals';
import tseslint from 'typescript-eslint';

// Layer direction: app -> features -> shared. Every arrow the other way,
// and every sideways import between features, is a deny policy below.
const layerPolicies = [
  {
    from: { element: { type: 'shared' } },
    disallow: { to: { element: { types: { anyOf: ['app', 'feature'] } } } },
  },
  {
    from: { element: { type: 'feature' } },
    disallow: { to: { element: { type: 'app' } } },
  },
  {
    from: { element: { type: 'feature' } },
    disallow: {
      to: {
        element: {
          type: 'feature',
          captured: { feature: '!{{ from.element.captured.feature }}' },
        },
      },
    },
  },
  {
    // A feature is reachable from outside only through its index.ts.
    from: { element: { type: 'app' } },
    disallow: { to: { element: { type: 'feature', internalPath: '!index.ts' } } },
  },
];

export default defineConfig([
  globalIgnores(['dist', 'test-results', 'src/shared/api/schema.gen.ts']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.strictTypeChecked,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2023,
      globals: globals.browser,
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  {
    files: ['src/**/*.{ts,tsx}'],
    plugins: { boundaries },
    settings: {
      'import/resolver': { typescript: { project: './tsconfig.app.json' } },
      'boundaries/elements': [
        { type: 'app', pattern: 'src/app' },
        { type: 'feature', pattern: 'src/features/*', capture: ['feature'] },
        { type: 'shared', pattern: 'src/shared/*', capture: ['segment'] },
      ],
    },
    rules: {
      'boundaries/dependencies': [2, { default: 'allow', policies: layerPolicies }],
    },
  },
]);
