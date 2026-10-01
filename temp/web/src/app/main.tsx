import { RouterProvider } from '@tanstack/react-router';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import '../shared/ui/styles/deploy.css';
import '../shared/ui/styles/app.css';

import { createApp } from './createApp';
import { AppProviders } from './providers';

const root = document.getElementById('root');
if (!root) {
  throw new Error('root element #root is missing');
}

const { queryClient, router } = createApp();

createRoot(root).render(
  <StrictMode>
    <AppProviders client={queryClient}>
      <RouterProvider router={router} />
    </AppProviders>
  </StrictMode>,
);
