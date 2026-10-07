import { useState } from 'react';

import { applyTheme, currentTheme, type Theme } from '../../shared/lib/theme';
import { Monitor, Moon, Sun } from '../../shared/ui/icons';

const options: { theme: Theme; label: string; Icon: typeof Moon }[] = [
  { theme: 'system', label: 'Как в системе', Icon: Monitor },
  { theme: 'dark', label: 'Тёмная', Icon: Moon },
  { theme: 'light', label: 'Светлая', Icon: Sun },
];

// Three choices as a v3 segmented control: the pressed one is the current choice,
// «Как в системе» until the person picks a scheme explicitly.
export function ThemeSwitch() {
  const [theme, setTheme] = useState(currentTheme);

  return (
    <div className="seg" role="group" aria-label="Тема оформления">
      {options.map(({ theme: value, label, Icon }) => (
        <button
          key={value}
          type="button"
          data-theme={value}
          aria-label={label}
          title={label}
          aria-pressed={value === theme}
          onClick={() => {
            applyTheme(value);
            setTheme(value);
          }}
        >
          <Icon />
        </button>
      ))}
    </div>
  );
}
