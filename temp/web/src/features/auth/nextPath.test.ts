import { nextPath } from './nextPath';

describe('nextPath', () => {
  it('keeps a path of this site', () => {
    expect(nextPath('/profile')).toBe('/profile');
    expect(nextPath('/users?page=2')).toBe('/users?page=2');
  });

  it.each([undefined, '', 'users', 'https://evil.example', '//evil.example', '/\\evil.example'])(
    'sends %j to /users',
    (next) => {
      expect(nextPath(next)).toBe('/users');
    },
  );
});
