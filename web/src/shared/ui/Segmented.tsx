import type { ReactNode } from 'react';

type SegmentedProps<T extends string> = {
  label: string;
  options: readonly { value: T; label: ReactNode; title?: string }[];
  value: T;
  onChange: (value: T) => void;
};

// The v3 .seg: one pressed button out of a few.
export function Segmented<T extends string>({
  label,
  options,
  value,
  onChange,
}: SegmentedProps<T>) {
  return (
    <div className="seg" role="group" aria-label={label}>
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={option.value === value}
          title={option.title}
          onClick={() => {
            onChange(option.value);
          }}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
