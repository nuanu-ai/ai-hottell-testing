import type { ReactNode } from 'react';

// Paths and sizes are 1:1 with the icons of deploy's shell.html
// (https://git.alva.dev/alva/deploy, commit 31e42ec), except where noted.

type IconProps = { size: number; children: ReactNode };

function Icon({ size, children }: IconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {children}
    </svg>
  );
}

export function Moon() {
  return (
    <Icon size={16}>
      <path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z" />
    </Icon>
  );
}

export function Sun() {
  return (
    <Icon size={16}>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
    </Icon>
  );
}

export function Burger() {
  return (
    <Icon size={18}>
      <path d="M4 7h16M4 12h16M4 17h16" />
    </Icon>
  );
}

// The arrow shows what a click does: left collapses the menu, right expands it.
export function Collapse({ direction }: { direction: 'left' | 'right' }) {
  return (
    <Icon size={18}>
      <path d={direction === 'left' ? 'M15 6l-6 6 6 6' : 'M9 6l6 6-6 6'} />
    </Icon>
  );
}

export function Users() {
  return (
    <Icon size={18}>
      <path d="M16 19v-1a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v1M9 10a3 3 0 1 0 0-6 3 3 0 0 0 0 6M22 19v-1a4 4 0 0 0-3-3.9M16 4.1a3 3 0 0 1 0 5.8" />
    </Icon>
  );
}

export function Profile() {
  return (
    <Icon size={18}>
      <path d="M20 21a8 8 0 0 0-16 0M12 13a4 4 0 1 0 0-8 4 4 0 0 0 0 8" />
    </Icon>
  );
}

// Not from deploy: it has no key icon at 31e42ec. This is Lucide's key-round.
export function Key() {
  return (
    <Icon size={18}>
      <path d="M2.586 17.414A2 2 0 0 0 2 18.828V21a1 1 0 0 0 1 1h3a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h1a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h.172a2 2 0 0 0 1.414-.586l.814-.814a6.5 6.5 0 1 0-4-4z" />
      <path d="M16.5 7.5h.01" />
    </Icon>
  );
}

// Not from deploy: it has no such icon at 31e42ec. This is Lucide's sliders-horizontal.
export function Sliders() {
  return (
    <Icon size={18}>
      <path d="M21 4h-7M10 4H3M21 12h-9M8 12H3M21 20h-5M12 20H3M14 2v4M8 10v4M16 18v4" />
    </Icon>
  );
}

// Not from deploy's icon set: Lucide's fingerprint (ISC), paths of lucide-static 1.49.0.
// Markup follows deploy's styles.css «вход по отпечатку»: .pk-line with pathLength=1 is drawn on hover.
const fingerprintPaths = [
  'M12 10a2 2 0 0 0-2 2c0 1.02-.1 2.51-.26 4',
  'M14 13.12c0 2.38 0 6.38-1 8.88',
  'M17.29 21.02c.12-.6.43-2.3.5-3.02',
  'M2 12a10 10 0 0 1 18-6',
  'M2 16h.01',
  'M21.8 16c.2-2 .131-5.354 0-6',
  'M5 19.5C5.5 18 6 15 6 12a6 6 0 0 1 .34-2',
  'M8.65 22c.21-.66.45-1.32.57-2',
  'M9 6.8a6 6 0 0 1 9 5.2v2',
];

export function Fingerprint() {
  return (
    <span className="pk-icon">
      <svg
        className="pk-print"
        width={24}
        height={24}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        {fingerprintPaths.map((d) => (
          <path key={d} className="pk-line" pathLength={1} d={d} />
        ))}
      </svg>
    </span>
  );
}
