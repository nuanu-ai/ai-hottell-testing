import { useEffect, useRef, useState } from 'react';

import { Button } from './Button';
import { classNames } from './classNames';

const COPIED_FOR_MS = 1500;

type CopyFieldProps = {
  value: string;
  // multiline keeps the line breaks of a command or a config block as they will be pasted.
  multiline?: boolean;
  // How long «Скопировано» stays; the v5.1 command row keeps it for 1.6 s.
  copiedForMs?: number;
  // A tooltip on the value, for a row that cuts a long value with an ellipsis.
  title?: string;
};

export function CopyField({
  value,
  multiline,
  copiedForMs = COPIED_FOR_MS,
  title,
}: CopyFieldProps) {
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
      // Each copy restarts the timer, so a second click is confirmed in full too.
      clearTimeout(timerRef.current);
      timerRef.current = setTimeout(() => {
        setCopied(false);
      }, copiedForMs);
    } catch {
      // Clipboard refused: select the text so the user can copy it by hand.
      if (codeRef.current) window.getSelection()?.selectAllChildren(codeRef.current);
    }
  }

  return (
    <div className={classNames('copy', multiline && 'lines')}>
      <code ref={codeRef} title={title}>
        {value}
      </code>
      <Button size="sm" onClick={() => void copy()}>
        {copied ? 'Скопировано' : 'Скопировать'}
      </Button>
    </div>
  );
}
