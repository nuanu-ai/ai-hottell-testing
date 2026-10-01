import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';

import { server } from './shared/api/test/server';

// Node's Request rejects relative URLs; the browser resolves them against the page,
// so tests do the same and the client keeps its production baseUrl /api.
class PageRelativeRequest extends Request {
  constructor(input: RequestInfo | URL, init?: RequestInit) {
    super(typeof input === 'string' ? new URL(input, window.location.origin) : input, init);
  }
}
globalThis.Request = PageRelativeRequest;

// jsdom has no scrolling; the router restores scroll on every navigation.
window.scrollTo = () => undefined;

// Listen before any test module loads: openapi-fetch keeps the fetch it sees at createClient.
server.listen({ onUnhandledFrame: 'error' });

afterEach(() => {
  cleanup();
  server.resetHandlers();
});

afterAll(() => {
  server.close();
});
