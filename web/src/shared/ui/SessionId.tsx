import { useEffect, useRef, useState } from 'react';

import { shortId } from '../lib/format';

const COPIED_FOR_MS = 1600;

// A session id as 8 + 4 signs in mono (README v5.1); a click copies the full id. CopyField shows
// the whole value, which a table cell or a topic line has no room for.
export function SessionId({ id }: { id: string }) {
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
      await navigator.clipboard.writeText(id);
    } catch {
      // Clipboard refused or absent: the label stays, nothing claims a copy that did not happen.
      return;
    }
    setCopied(true);
    // Each copy restarts the 1.6 s, so a second click is confirmed in full too.
    clearTimeout(timerRef.current);
    timerRef.current = setTimeout(() => {
      setCopied(false);
    }, COPIED_FOR_MS);
  }

  return (
    <button type="button" className="idc" title="Скопировать полный ID" onClick={() => void copy()}>
      {copied ? 'ID скопирован' : shortId(id)}
    </button>
  );
}
