import { useState } from 'react';

import { readStorage } from '../../shared/lib/storage';
import { applyTheme, initialTheme, type Theme } from '../../shared/lib/theme';
import { Moon, Sun } from '../../shared/ui/icons';

const options: { theme: Theme; label: string; Icon: typeof Moon }[] = [
  { theme: 'dark', label: 'Тёмная', Icon: Moon },
  { theme: 'light', label: 'Светлая', Icon: Sun },
];

const storage = { getItem: readStorage };

export function ThemeSwitch() {
  const [theme, setTheme] = useState(() => initialTheme(storage));

  return (
    <div className="theme-switch" role="group" aria-label="Тема оформления">
      {options.map(({ theme: value, label, Icon }) => (
        <button
          key={value}
          type="button"
          data-theme={value}
          aria-label={label}
          title={label}
          className={value === theme ? 'on' : undefined}
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
