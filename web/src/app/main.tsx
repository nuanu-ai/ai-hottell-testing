import { RouterProvider } from '@tanstack/react-router';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import '@fontsource/ibm-plex-sans/400.css';
import '@fontsource/ibm-plex-sans/500.css';
import '@fontsource/ibm-plex-sans/600.css';
import '@fontsource/ibm-plex-mono/400.css';
import '@fontsource/ibm-plex-mono/500.css';
import '../shared/ui/styles/index.css';

import { followSystemTheme } from '../shared/lib/theme';

import { createApp } from './createApp';
import { AppProviders } from './providers';

const root = document.getElementById('root');
if (!root) {
  throw new Error('root element #root is missing');
}

// Without an explicit choice the theme follows the system scheme, also while the page is open.
followSystemTheme();

const { queryClient, router } = createApp();

createRoot(root).render(
  <StrictMode>
    <AppProviders client={queryClient}>
      <RouterProvider router={router} />
    </AppProviders>
  </StrictMode>,
);
