import { canAnimate, EASE } from '../lib/motion';

// Plays keyframes through the Web Animations API where the browser has it and motion is allowed.
export function play(element: Element | null, keyframes: Keyframe[], duration: number): void {
  if (!element || !canAnimate() || typeof element.animate !== 'function') return;
  element.animate(keyframes, { duration, easing: EASE });
}
