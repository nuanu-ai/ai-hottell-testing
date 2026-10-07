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

// The analytics screens (README v5.1: «Время — местное, UTC — в title»), ported from the
// reference page's hhmm, utc, when, ddmm and ago. An empty or broken ISO is «—».

const DASH = '—';

function parse(iso: string | null | undefined): Date | undefined {
  if (!iso) return undefined;
  const moment = new Date(iso);
  return Number.isNaN(moment.getTime()) ? undefined : moment;
}

/** «часы»: local HH:MM. */
export function formatClock(moment: Date): string {
  return `${pad(moment.getHours())}:${pad(moment.getMinutes())}`;
}

/** The title of a local time: YYYY-MM-DD HH:MM UTC, empty for an invalid moment. */
export function utcTitle(moment: Date): string {
  if (Number.isNaN(moment.getTime())) return '';
  return `${moment.toISOString().replace('T', ' ').slice(0, 16)} UTC`;
}

/** «когда»: today as «14:22», an earlier day as «30.09 14:22»; UTC goes to the title. */
export function formatWhen(
  iso: string | null | undefined,
  now: Date = new Date(),
): { text: string; title: string } {
  const moment = parse(iso);
  if (!moment) return { text: DASH, title: '' };
  const clock = formatClock(moment);
  const text =
    moment.toDateString() === now.toDateString()
      ? clock
      : `${pad(moment.getDate())}.${pad(moment.getMonth() + 1)} ${clock}`;
  return { text, title: utcTitle(moment) };
}

/** DD.MM of the browser's date: the days of every chart are the browser's days. */
export function formatDayMonth(iso: string | null | undefined): string {
  const moment = parse(iso);
  if (!moment) return DASH;
  return `${pad(moment.getDate())}.${pad(moment.getMonth() + 1)}`;
}

// The days of the by-day charts are the browser's days, as the session list shows its times:
// at +08:00 the work of 02:00 on 02.10 is a bar of 02.10, not of the UTC 01.10.

const DAY = 86_400_000;

/** yyyy-mm-dd of the browser's day of a moment: the key of a day in every by-day chart. */
export function localDay(moment: Date | number): string {
  const m = new Date(moment);
  return `${String(m.getFullYear())}-${pad(m.getMonth() + 1)}-${pad(m.getDate())}`;
}

/** The browser's calendar days from the day of FIRST to the day of END, both counted. */
export function localDaySpan(first: Date | number, end: Date | number): number {
  // Calendar arithmetic on the local date parts: a DST day is not 24 hours long.
  const day = (moment: Date | number) => {
    const m = new Date(moment);
    return Date.UTC(m.getFullYear(), m.getMonth(), m.getDate());
  };
  return Math.round((day(end) - day(first)) / DAY) + 1;
}

/** N browser's days up to the day of END, oldest first, as yyyy-mm-dd. */
export function localDaysTo(end: Date | number, n: number): string[] {
  const e = new Date(end);
  return Array.from({ length: n }, (_, i) =>
    localDay(new Date(e.getFullYear(), e.getMonth(), e.getDate() - (n - 1 - i))),
  );
}

/**
 * The browser's zone as the server reads it (tz of the dataset): its IANA name, or its offset
 * now as ±HH:MM when the browser names none.
 */
export function browserZone(): string {
  const name = Intl.DateTimeFormat().resolvedOptions().timeZone;
  if (name) return name;
  const offset = -new Date().getTimezoneOffset();
  const abs = Math.abs(offset);
  return `${offset < 0 ? '-' : '+'}${pad(Math.floor(abs / 60))}:${pad(abs % 60)}`;
}

/** «назад» of the analytics: minutes and hours rounded, beyond a day as formatWhen writes it. */
export function formatAgoShort(iso: string | null | undefined, now: Date = new Date()): string {
  const moment = parse(iso);
  if (!moment) return DASH;
  const minutes = (now.getTime() - moment.getTime()) / 60_000;
  if (minutes < 1) return 'только что';
  if (minutes < 60) return `${String(Math.round(minutes))} мин назад`;
  if (minutes < 1440) return `${String(Math.round(minutes / 60))} ч назад`;
  return formatWhen(iso, now).text;
}
