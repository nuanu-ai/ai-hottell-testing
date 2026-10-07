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

// Not from deploy's icon set: Lucide's monitor (ISC), for the «Как в системе» theme.
export function Monitor() {
  return (
    <Icon size={16}>
      <rect x="2" y="3" width="20" height="14" rx="2" />
      <path d="M8 21h8M12 17v4" />
    </Icon>
  );
}

// Not from deploy's icon set: Lucide's fingerprint (ISC), paths of lucide-static 1.49.0.
// .pk-line with pathLength=1 is drawn on hover (styles/auth.css).
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
