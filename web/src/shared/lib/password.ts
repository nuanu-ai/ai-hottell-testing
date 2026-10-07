const minLength = 8;
const maxLength = 128;

export type PasswordError = { field: 'password' | 'repeat'; message: string };

/**
 * The server's rule for a new password — 8 to 128 characters, counted as the server
 * counts them, by code point — plus the repeat matching it. Null when both hold.
 */
export function validateNewPassword(password: string, repeat: string): PasswordError | null {
  const length = Array.from(password).length;
  if (length < minLength) {
    return { field: 'password', message: 'Не короче 8 символов' };
  }
  if (length > maxLength) {
    return { field: 'password', message: 'Не длиннее 128 символов' };
  }
  if (password !== repeat) {
    return { field: 'repeat', message: 'Пароли не совпадают' };
  }
  return null;
}
