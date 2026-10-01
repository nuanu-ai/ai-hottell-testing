import { formatAgo, formatDateTime, fullMoment } from './time';

// Local constructors: the formats use the browser's zone, so the tests do not depend on TZ.
const now = new Date(2026, 8, 30, 14, 7, 30);
const secondsBefore = (seconds: number) => new Date(now.getTime() - seconds * 1000);

describe('formatDateTime', () => {
  it('writes DD.MM.YYYY HH:MM with leading zeros', () => {
    expect(formatDateTime(new Date(2026, 0, 5, 9, 3))).toBe('05.01.2026 09:03');
    expect(formatDateTime(new Date(2026, 11, 31, 23, 59, 59))).toBe('31.12.2026 23:59');
  });
});

describe('formatAgo', () => {
  it.each([
    [0, 'только что'],
    [59, 'только что'],
    [60, '1 мин назад'],
    [3599, '59 мин назад'],
    [3600, '1 ч назад'],
    [86399, '23 ч назад'],
  ])('%i s before is «%s»', (seconds, text) => {
    expect(formatAgo(secondsBefore(seconds), now)).toBe(text);
  });

  it('from a day on writes the date and time', () => {
    expect(formatAgo(secondsBefore(86400), now)).toBe('29.09.2026 14:07');
  });

  it('calls a moment ahead of the clock «только что»', () => {
    expect(formatAgo(new Date(now.getTime() + 5000), now)).toBe('только что');
  });
});

describe('fullMoment', () => {
  it('is the ru-RU full date and time', () => {
    const moment = new Date(2026, 8, 30, 14, 7, 30);
    expect(fullMoment(moment)).toBe(moment.toLocaleString('ru-RU'));
  });
});
