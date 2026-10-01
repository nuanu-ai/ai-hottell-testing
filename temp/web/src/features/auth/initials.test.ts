import { initials } from './initials';

describe('initials', () => {
  it('takes the first letters of the first two words', () => {
    expect(initials('Анна Петрова')).toBe('АП');
    expect(initials('анна мария петрова')).toBe('АМ');
  });

  it('takes one letter of a one-word name', () => {
    expect(initials('alva')).toBe('A');
  });

  it('ignores extra spaces', () => {
    expect(initials('  Анна   Петрова ')).toBe('АП');
  });
});
