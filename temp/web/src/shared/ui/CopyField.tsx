import { useEffect, useRef, useState } from 'react';

import { Button } from './Button';
import { classNames } from './classNames';

const COPIED_FOR_MS = 1500;

// multiline keeps the line breaks of a command or a config block as they will be pasted.
export function CopyField({ value, multiline }: { value: string; multiline?: boolean }) {
  const codeRef = useRef<HTMLElement>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  const [copied, setCopied] = useState(false);

  useEffect(
    () => () => {
      clearTimeout(timerRef.current);
    },
    [],
  );

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      // Each copy restarts the 1.5 s, so a second click is confirmed in full too.
      clearTimeout(timerRef.current);
      timerRef.current = setTimeout(() => {
        setCopied(false);
      }, COPIED_FOR_MS);
    } catch {
      // Clipboard refused: select the text so the user can copy it by hand.
      if (codeRef.current) window.getSelection()?.selectAllChildren(codeRef.current);
    }
  }

  return (
    <div className={classNames('код', multiline && 'код-строки')}>
      <code ref={codeRef} className="mono">
        {value}
      </code>
      <Button size="sm" onClick={() => void copy()}>
        {copied ? 'Скопировано' : 'Скопировать'}
      </Button>
    </div>
  );
}
