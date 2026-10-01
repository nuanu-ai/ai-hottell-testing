import { http, HttpResponse } from 'msw';

import { ApiError, apiClient, isUnauthorized, unwrap, unwrapEmpty } from './client';
import { handlers, server } from './test/server';

async function failure(request: Promise<unknown>) {
  try {
    await request;
  } catch (error) {
    return error;
  }
  throw new Error('expected the request to fail');
}

describe('unwrap', () => {
  it('returns the success body', async () => {
    await expect(unwrap(apiClient.GET('/version'))).resolves.toEqual({ version: 'dev' });
  });

  it('turns a server error body into ApiError', async () => {
    server.use(handlers.getVersionError(503, { code: 'unavailable', message: 'try later' }));

    const error = await failure(unwrap(apiClient.GET('/version')));

    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 503, code: 'unavailable', message: 'try later' });
  });

  it('turns a body outside the contract into ApiError with code unknown', async () => {
    server.use(
      http.get(
        '/api/version',
        () => new HttpResponse('Bad Gateway', { status: 502, statusText: 'Bad Gateway' }),
      ),
    );

    const error = await failure(unwrap(apiClient.GET('/version')));

    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 502, code: 'unknown', message: 'Bad Gateway' });
  });
});

describe('unwrapEmpty', () => {
  it('resolves on 204', async () => {
    server.use(handlers.logout());

    await expect(unwrapEmpty(apiClient.POST('/auth/logout'))).resolves.toBeUndefined();
  });

  it('throws ApiError on a failure', async () => {
    server.use(
      http.post('/api/auth/logout', () =>
        HttpResponse.json({ code: 'internal', message: 'boom' }, { status: 500 }),
      ),
    );

    const error = await failure(unwrapEmpty(apiClient.POST('/auth/logout')));

    expect(error).toMatchObject({ status: 500, code: 'internal', message: 'boom' });
  });
});

describe('isUnauthorized', () => {
  it('is true only for ApiError 401', () => {
    expect(isUnauthorized(new ApiError(401, 'unauthenticated', 'Войдите, чтобы продолжить'))).toBe(
      true,
    );
    expect(isUnauthorized(new ApiError(403, 'forbidden', 'no'))).toBe(false);
    expect(isUnauthorized(new Error('401'))).toBe(false);
  });
});
