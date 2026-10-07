import { useId } from 'react';

import { Checkbox } from '../../shared/ui';
import type { Note } from './model';

type ToggleProps = {
  label: string;
  checked: boolean;
  // A short line above the note, such as when the box takes effect.
  caption?: string;
  note: Note;
  onChange: (on: boolean) => void;
};

const noteMarks = { warn: '⚠ ', off: '✗ ' } as const;

export function Toggle({ label, checked, caption, note, onChange }: ToggleProps) {
  const noteId = useId();
  const captionId = useId();

  return (
    <div>
      <Checkbox
        label={label}
        checked={checked}
        aria-describedby={caption ? `${captionId} ${noteId}` : noteId}
        onChange={(event) => {
          onChange(event.target.checked);
        }}
      />
      {caption && (
        <p id={captionId} className="check-note">
          {caption}
        </p>
      )}
      <p id={noteId} className="check-note">
        {note.tone && noteMarks[note.tone]}
        {note.text}
      </p>
    </div>
  );
}
