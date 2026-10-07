// Motion rules of the v5.1 dashboard (ai-hottell@c3c8357, ui/static/index.html): only transform and
// opacity move, on one curve, and nothing moves for a reader who asked the system for less motion.

/** The curve of every movement: `cubic-bezier(.2,0,0,1)`. */
export const EASE = 'cubic-bezier(.2,0,0,1)';

/**
 * Whether the reader asked for reduced motion. The CSS rule in `styles/base.css` stops
 * transitions and CSS animations; movement driven from script checks this instead.
 */
export function prefersReducedMotion(): boolean {
  return (
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  );
}

/** A hidden tab draws no frames, so movement there is skipped and the end state shown at once. */
export function canAnimate(): boolean {
  return !prefersReducedMotion() && document.visibilityState !== 'hidden';
}
