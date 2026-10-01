import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';

import { handlers, server } from '../../shared/api/test/server';
import { VersionLabel } from './VersionLabel';

function renderLabel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <VersionLabel />
    </QueryClientProvider>,
  );
}

describe('VersionLabel', () => {
  it('shows the version answered by GET /api/version', async () => {
    renderLabel();

    expect(await screen.findByText('Версия: dev')).toBeInTheDocument();
  });

  it('says the version is unavailable when the API fails', async () => {
    server.use(handlers.getVersionError(500, { code: 'internal', message: 'boom' }));
    renderLabel();

    expect(await screen.findByText('Версия: недоступна')).toBeInTheDocument();
  });
});
