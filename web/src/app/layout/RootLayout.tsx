import { Outlet } from '@tanstack/react-router';

import { DocumentTitle } from './DocumentTitle';

// Every page, open or closed: keeps the browser tab title in step with the route.
export function RootLayout() {
  return (
    <>
      <DocumentTitle />
      <Outlet />
    </>
  );
}
