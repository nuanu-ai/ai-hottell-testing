import { plural } from '../../../shared/lib/format';

/** «4 темы стоит обсудить»; none — «Тем для обсуждения нет». */
export function topicsHeadline(n: number): string {
  return n > 0
    ? `${String(n)} ${plural(n, 'тема', 'темы', 'тем')} стоит обсудить`
    : 'Тем для обсуждения нет';
}

/** «по 5 вашим сессиям и 1 по расписанию за 7 дней, без глубокого разбора». */
export function heroSub(yours: number, scheduled: number, period: string): string {
  return (
    `по ${String(yours)} ${plural(yours, 'вашей сессии', 'вашим сессиям', 'вашим сессиям')}` +
    (scheduled > 0 ? ` и ${String(scheduled)} по расписанию` : '') +
    ` ${period}, без глубокого разбора`
  );
}

// The page's own notes in «Ограничения данных» (the reference, L619).
export const fixLimits = [
  'Темы строят детекторы по записанным событиям Hooks и OTel, без глубокого разбора. Все темы — гипотезы: причину выясняет коуч вместе с вами.',
  'Правки не применяются из интерфейса: решение, внедрение и проверка записываются в журнал коуча.',
];
