import createClient from 'openapi-fetch';

import type { components, paths } from './schema.gen';

// Every non-2xx answer reaches callers as this one shape.
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

type ServerError = components['schemas']['Error'];

export const apiClient = createClient<paths>({
  baseUrl: '/api',
  credentials: 'same-origin',
});

function isServerError(body: unknown): body is ServerError {
  return (
    typeof body === 'object' &&
    body !== null &&
    typeof (body as Partial<ServerError>).code === 'string' &&
    typeof (body as Partial<ServerError>).message === 'string'
  );
}

export function toApiError(response: Response, body: unknown): ApiError {
  if (isServerError(body)) {
    return new ApiError(response.status, body.code, body.message);
  }
  return new ApiError(response.status, 'unknown', response.statusText);
}

interface FetchResult<T> {
  data?: T;
  error?: unknown;
  response: Response;
}

// Returns the success body or throws ApiError, so queries see one failure shape.
export async function unwrap<T>(request: Promise<FetchResult<T>>): Promise<T> {
  const { data, error, response } = await request;
  if (!response.ok || data === undefined) {
    throw toApiError(response, error);
  }
  return data;
}

// For answers without a body (204): resolves on success or throws ApiError.
export async function unwrapEmpty(request: Promise<FetchResult<unknown>>): Promise<void> {
  const { error, response } = await request;
  if (!response.ok) {
    throw toApiError(response, error);
  }
}

/** True for an API answer 401: the request has no live session. */
export function isUnauthorized(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401;
}
