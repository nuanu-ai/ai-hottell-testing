// The time formats of deploy's shell.html (локализоватьВремя), in the browser's zone.

const pad = (value: number) => String(value).padStart(2, '0');

/** «дата-время»: DD.MM.YYYY HH:MM. */
export function formatDateTime(moment: Date): string {
  return (
    `${pad(moment.getDate())}.${pad(moment.getMonth() + 1)}.${String(moment.getFullYear())} ` +
    `${pad(moment.getHours())}:${pad(moment.getMinutes())}`
  );
}

/**
 * «назад»: for columns where freshness matters more than the moment. Beyond a day
 * the relative form stops helping, and the date and time are written instead.
 */
export function formatAgo(moment: Date, now: Date = new Date()): string {
  const seconds = Math.floor((now.getTime() - moment.getTime()) / 1000);
  if (seconds < 60) return 'только что';
  if (seconds < 3600) return `${String(Math.floor(seconds / 60))} мин назад`;
  if (seconds < 86400) return `${String(Math.floor(seconds / 3600))} ч назад`;
  return formatDateTime(moment);
}

/** The full moment for a title: a short form hides the day or the seconds. */
export function fullMoment(moment: Date): string {
  return moment.toLocaleString('ru-RU');
}
