import { http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';

import type { components, paths } from '../schema.gen';

type Schemas = components['schemas'];

// Handlers are typed by the schema: a path or body the API cannot return does not compile.
// A path parameter {name} becomes MSW's :name, which matches any value.
const url = (path: keyof paths) => `/api${path.replace(/\{(\w+)\}/g, ':$1')}`;

export const me: Schemas['Me'] = {
  id: '6f1c2a3e-8a47-4d8e-9a0b-1f2e3d4c5b6a',
  email: 'anna@example.test',
  name: 'Анна Петрова',
};

// The signed-in user as a row of GET /users.
export const meRow: Schemas['UserListItem'] = {
  ...me,
  status: 'active',
  createdAt: '2026-09-01T08:00:00Z',
  lastLoginAt: '2026-09-30T08:00:00Z',
  inviteExpiresAt: null,
  isMe: true,
};

export const noSession: Schemas['Error'] = {
  code: 'unauthenticated',
  message: 'Войдите, чтобы продолжить',
};

// A user without keys: nothing issued yet, the binary has not asked for a token.
export const noKeys: Schemas['KeysStatus'] = {
  mcp: { active: false, createdAt: null, lastUsedAt: null },
  ingest: { active: false, createdAt: null, lastUsedAt: null },
};

// A synthetic key: the shape of the real ones, [A-Za-z0-9_-]+, and nothing else.
export const issuedMcpKey: Schemas['IssuedMcpKey'] = {
  key: 'ht_mcp_0123456789abcdef',
  mcpUrl: 'http://localhost:8080/mcp',
  serverName: 'hottell',
};

export const handlers = {
  getVersion: (body: Schemas['Version'] = { version: 'dev' }) =>
    http.get(url('/version'), () => HttpResponse.json(body)),
  getVersionError: (status: number, body: Schemas['Error']) =>
    http.get(url('/version'), () => HttpResponse.json(body, { status })),
  getMe: (body: Schemas['Me'] = me) => http.get(url('/me'), () => HttpResponse.json(body)),
  getMeError: (status: number, body: Schemas['Error'] = noSession) =>
    http.get(url('/me'), () => HttpResponse.json(body, { status })),
  login: () => http.post(url('/auth/login'), () => new HttpResponse(null, { status: 204 })),
  loginError: (status: number, body: Schemas['Error']) =>
    http.post(url('/auth/login'), () => HttpResponse.json(body, { status })),
  passkeyLoginBegin: (
    options: Schemas['PasskeyOptions']['options'] = { challenge: 'Y2hhbGxlbmdl' },
  ) => http.post(url('/auth/passkey/login/begin'), () => HttpResponse.json({ options })),
  passkeyLoginBeginError: (status: number, body: Schemas['Error']) =>
    http.post(url('/auth/passkey/login/begin'), () => HttpResponse.json(body, { status })),
  passkeyLoginFinishError: (status: number, body: Schemas['Error']) =>
    http.post(url('/auth/passkey/login/finish'), () => HttpResponse.json(body, { status })),
  getUsers: (items: Schemas['UserListItem'][] = [meRow]) =>
    http.get(url('/users'), () => HttpResponse.json({ items })),
  getUsersError: (status: number, body: Schemas['Error']) =>
    http.get(url('/users'), () => HttpResponse.json(body, { status })),
  inviteUser: (body: Schemas['InviteUserResponse']) =>
    http.post(url('/users'), () => HttpResponse.json(body, { status: 201 })),
  inviteUserError: (status: number, body: Schemas['Error']) =>
    http.post(url('/users'), () => HttpResponse.json(body, { status })),
  getInvite: (body: Schemas['LinkHolder']) =>
    http.get(url('/invites/{token}'), () => HttpResponse.json(body)),
  getInviteError: (status: number, body: Schemas['Error']) =>
    http.get(url('/invites/{token}'), () => HttpResponse.json(body, { status })),
  acceptInviteError: (status: number, body: Schemas['Error']) =>
    http.post(url('/invites/{token}/accept'), () => HttpResponse.json(body, { status })),
  getPasswordReset: (body: Schemas['LinkHolder']) =>
    http.get(url('/password-resets/{token}'), () => HttpResponse.json(body)),
  getPasswordResetError: (status: number, body: Schemas['Error']) =>
    http.get(url('/password-resets/{token}'), () => HttpResponse.json(body, { status })),
  completePasswordResetError: (status: number, body: Schemas['Error']) =>
    http.post(url('/password-resets/{token}/complete'), () => HttpResponse.json(body, { status })),
  changePassword: () =>
    http.post(url('/me/password'), () => new HttpResponse(null, { status: 204 })),
  changePasswordError: (status: number, body: Schemas['Error']) =>
    http.post(url('/me/password'), () => HttpResponse.json(body, { status })),
  getPasskeys: (items: Schemas['PasskeyItem'][] = []) =>
    http.get(url('/me/passkeys'), () => HttpResponse.json({ items })),
  getPasskeysError: (status: number, body: Schemas['Error']) =>
    http.get(url('/me/passkeys'), () => HttpResponse.json(body, { status })),
  passkeyRegisterBegin: (
    options: Schemas['PasskeyOptions']['options'] = { challenge: 'Y2hhbGxlbmdl' },
  ) => http.post(url('/me/passkeys/register/begin'), () => HttpResponse.json({ options })),
  passkeyRegisterBeginError: (status: number, body: Schemas['Error']) =>
    http.post(url('/me/passkeys/register/begin'), () => HttpResponse.json(body, { status })),
  passkeyRegisterFinishError: (status: number, body: Schemas['Error']) =>
    http.post(url('/me/passkeys/register/finish'), () => HttpResponse.json(body, { status })),
  deletePasskeyError: (status: number, body: Schemas['Error']) =>
    http.delete(url('/me/passkeys/{id}'), () => HttpResponse.json(body, { status })),
  getKeys: (body: Schemas['KeysStatus'] = noKeys) =>
    http.get(url('/me/keys'), () => HttpResponse.json(body)),
  logout: () => http.post(url('/auth/logout'), () => new HttpResponse(null, { status: 204 })),
};

export const server = setupServer(
  handlers.getVersion(),
  handlers.getUsers(),
  handlers.getPasskeys(),
  handlers.getKeys(),
);
