import { useEffect, useState } from 'react';

import { Panel } from '../../../shared/ui';

const TOKENS = [
  '--bg',
  '--surface',
  '--surface-2',
  '--hover',
  '--line',
  '--ink',
  '--ink-2',
  '--ink-3',
  '--accent',
  '--accent-hover',
  '--accent-soft',
  '--on-accent',
  '--m1',
  '--m2',
  '--m3',
  '--m4',
  '--ok',
  '--ok-soft',
  '--warn',
  '--warn-soft',
  '--bad',
  '--bad-soft',
];

function read(): Record<string, string> {
  const style = getComputedStyle(document.documentElement);
  return Object.fromEntries(TOKENS.map((t) => [t, style.getPropertyValue(t).trim()]));
}

export function TokensSection() {
  const [values, setValues] = useState(read);
  useEffect(() => {
    // The theme switch rewrites data-theme on <html>: read the values again.
    const observer = new MutationObserver(() => {
      setValues(read());
    });
    observer.observe(document.documentElement, { attributeFilter: ['data-theme'] });
    return () => {
      observer.disconnect();
    };
  }, []);

  return (
    <Panel title="Токены" sub="цвета текущей темы">
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))',
          gap: 10,
        }}
      >
        {TOKENS.map((token) => (
          <div key={token} className="acts">
            <span
              style={{
                width: 32,
                height: 32,
                background: `var(${token})`,
                border: '1px solid var(--line)',
              }}
            />
            <span className="stack g4">
              <span className="mono">{token}</span>
              <span className="muted mono">{values[token]}</span>
            </span>
          </div>
        ))}
      </div>
    </Panel>
  );
}
