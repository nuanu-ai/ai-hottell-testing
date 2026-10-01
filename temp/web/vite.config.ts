import react from '@vitejs/plugin-react';
import { configDefaults, defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [react()],
  build: {
    // dist/.keep stays in Git for go:embed, so the build must not wipe dist/ itself.
    outDir: 'dist/app',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test-setup.ts'],
    // e2e/ is Playwright's, run by `task e2e` against a running stand.
    exclude: [...configDefaults.exclude, 'e2e/**'],
  },
});
