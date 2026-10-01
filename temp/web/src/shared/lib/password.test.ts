import { validateNewPassword } from './password';

describe('validateNewPassword', () => {
  it('accepts 8 and 128 characters that match', () => {
    expect(validateNewPassword('12345678', '12345678')).toBeNull();
    const longest = 'я'.repeat(128);
    expect(validateNewPassword(longest, longest)).toBeNull();
  });

  it('rejects fewer than 8 characters on the password field', () => {
    expect(validateNewPassword('1234567', '1234567')).toEqual({
      field: 'password',
      message: 'Не короче 8 символов',
    });
  });

  it('rejects more than 128 characters on the password field', () => {
    const tooLong = 'a'.repeat(129);
    expect(validateNewPassword(tooLong, tooLong)).toEqual({
      field: 'password',
      message: 'Не длиннее 128 символов',
    });
  });

  it('counts characters, not UTF-16 units, as the server does', () => {
    // Eight emoji are sixteen UTF-16 units but eight characters.
    const emoji = '😀'.repeat(8);
    expect(validateNewPassword(emoji, emoji)).toBeNull();
    const tooLong = '😀'.repeat(129);
    expect(validateNewPassword(tooLong, tooLong)?.message).toBe('Не длиннее 128 символов');
  });

  it('rejects a repeat that does not match on the repeat field', () => {
    expect(validateNewPassword('12345678', '12345679')).toEqual({
      field: 'repeat',
      message: 'Пароли не совпадают',
    });
  });
});
